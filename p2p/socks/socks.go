// Copyright 2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

package socks

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

// Wire constants from RFC 1928 / RFC 1929. Values match Bitcoin Core's
// SOCKSVersion / SOCKS5Method / SOCKS5Command / SOCKS5Atyp enums
// (src/netbase.cpp:244-292).
const (
	socksVersion = 0x05

	methodNoAuth       = 0x00
	methodUserPass     = 0x02
	methodNoAcceptable = 0xff

	cmdConnect = 0x01

	atypIPv4       = 0x01
	atypDomainName = 0x03
	atypIPv6       = 0x04

	authVersion = 0x01 // RFC 1929 subnegotiation version
)

// recvTimeout bounds the negotiation reads. Bitcoin Core's
// g_socks5_recv_timeout (DEFAULT_SOCKS5_RECV_TIMEOUT = 20s). The overall
// attempt is additionally bounded by the caller's context. The size
// matters for Tor: a CONNECT reply routinely takes >15s while the
// daemon retries the stream across circuits hunting for an exit whose
// policy carries the port.
const recvTimeout = 20 * time.Second

// proxyConnectTimeout bounds the TCP connect to the proxy itself —
// Core's DEFAULT_CONNECT_TIMEOUT (5s). The proxy is almost always
// loopback; a slow connect here means it's down, not far away.
const proxyConnectTimeout = 5 * time.Second

// ReplyError is a non-success REP code from the proxy's CONNECT response.
// Code values are RFC 1928 plus the Tor extension range 0xf0-0xf7.
type ReplyError struct {
	Code uint8
}

// replyString mirrors Core's Socks5ErrorString (src/netbase.cpp:352).
func (e *ReplyError) Error() string {
	switch e.Code {
	case 0x01:
		return "socks: general failure"
	case 0x02:
		return "socks: connection not allowed"
	case 0x03:
		return "socks: network unreachable"
	case 0x04:
		return "socks: host unreachable"
	case 0x05:
		return "socks: connection refused"
	case 0x06:
		return "socks: TTL expired"
	case 0x07:
		return "socks: protocol error"
	case 0x08:
		return "socks: address type not supported"
	case 0xf0:
		return "socks: onion service descriptor can not be found"
	case 0xf1:
		return "socks: onion service descriptor is invalid"
	case 0xf2:
		return "socks: onion service introduction failed"
	case 0xf3:
		return "socks: onion service rendezvous failed"
	case 0xf4:
		return "socks: onion service missing client authorization"
	case 0xf5:
		return "socks: onion service wrong client authorization"
	case 0xf6:
		return "socks: onion service invalid address"
	case 0xf7:
		return "socks: onion service introduction timed out"
	}
	return fmt.Sprintf("socks: unknown reply (0x%02x)", e.Code)
}

// Negotiation failures that are the proxy's fault rather than the
// destination's. Callers use this distinction the way Core uses
// proxy_connection_failed: a destination failure marks the address bad in
// addrman, a proxy failure must not.
var (
	ErrHostTooLong   = errors.New("socks: destination hostname exceeds 255 bytes")
	ErrAuthTooLong   = errors.New("socks: username or password exceeds 255 bytes")
	ErrProxyProtocol = errors.New("socks: proxy speaks no acceptable SOCKS5")
	ErrAuthRejected  = errors.New("socks: proxy rejected authentication")
)

// Credentials are RFC 1929 username/password pairs. Under Tor, distinct
// credentials select distinct circuits (stream isolation).
type Credentials struct {
	Username string
	Password string
}

// IsolationGenerator yields a unique Credentials per call. Port of Core's
// TorStreamIsolationCredentialsGenerator (src/netbase.cpp:753): an 8-byte
// random per-process prefix plus a counter, so neither successive nor
// parallel node instances share circuits.
type IsolationGenerator struct {
	prefix  string
	counter atomic.Uint64
}

// NewIsolationGenerator seeds a generator with a fresh random prefix.
func NewIsolationGenerator() (*IsolationGenerator, error) {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return nil, err
	}
	return &IsolationGenerator{prefix: hex.EncodeToString(b[:]) + "-"}, nil
}

// Generate returns the next unique credentials. Username and password are
// identical, as in Core.
func (g *IsolationGenerator) Generate() Credentials {
	s := fmt.Sprintf("%s%d", g.prefix, g.counter.Add(1))
	return Credentials{Username: s, Password: s}
}

// Proxy is a SOCKS5 proxy endpoint plus its connection policy.
type Proxy struct {
	// Addr is the proxy's TCP address, e.g. "127.0.0.1:9050".
	Addr string
	// Isolation, when non-nil, supplies fresh credentials for every
	// connection (Tor stream isolation). Nil connects unauthenticated.
	Isolation *IsolationGenerator
}

// Dial connects to dest:port through the proxy. dest is passed to the
// proxy verbatim as a DOMAINNAME target — an IP literal, a hostname, or a
// .onion address — and is never resolved locally. The returned conn is the
// proxied stream, ready for the caller's protocol.
//
// A *ReplyError return means the proxy itself was healthy and the
// destination failed; any other error means the proxy connection or
// negotiation failed.
func (p *Proxy) Dial(ctx context.Context, dest string, port uint16) (net.Conn, error) {
	var auth *Credentials
	if p.Isolation != nil {
		c := p.Isolation.Generate()
		auth = &c
	}
	d := &net.Dialer{Timeout: proxyConnectTimeout}
	conn, err := d.DialContext(ctx, "tcp", p.Addr)
	if err != nil {
		return nil, fmt.Errorf("socks: proxy %s unreachable: %w", p.Addr, err)
	}
	// Server shutdown interrupts in-flight dials by cancelling ctx;
	// negotiate itself only honors deadlines, so close the conn on
	// cancellation to unblock its reads.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-watchDone:
		}
	}()
	if err := negotiate(ctx, conn, dest, port, auth); err != nil {
		conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if ctx.Err() != nil {
		// Cancellation raced negotiate's success; the conn may already
		// be closed by the watcher.
		conn.Close()
		return nil, ctx.Err()
	}
	return conn, nil
}

// negotiate runs the SOCKS5 handshake on an established proxy conn. Port
// of Core's Socks5() (src/netbase.cpp:392); the phase structure and all
// validation checks follow it one to one.
func negotiate(ctx context.Context, conn net.Conn, dest string, port uint16, auth *Credentials) error {
	if len(dest) > 255 {
		return ErrHostTooLong
	}
	deadline := time.Now().Add(recvTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	defer conn.SetDeadline(time.Time{})

	// Phase 1: version identifier / method selection.
	greeting := []byte{socksVersion, 0x01, methodNoAuth}
	if auth != nil {
		greeting = []byte{socksVersion, 0x02, methodNoAuth, methodUserPass}
	}
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("socks: writing greeting: %w", err)
	}
	var sel [2]byte
	if _, err := io.ReadFull(conn, sel[:]); err != nil {
		return fmt.Errorf("socks: reading method selection: %w", err)
	}
	if sel[0] != socksVersion {
		return ErrProxyProtocol
	}

	// Phase 2: authentication subnegotiation (RFC 1929).
	switch {
	case sel[1] == methodUserPass && auth != nil:
		if len(auth.Username) > 255 || len(auth.Password) > 255 {
			return ErrAuthTooLong
		}
		msg := make([]byte, 0, 3+len(auth.Username)+len(auth.Password))
		msg = append(msg, authVersion, byte(len(auth.Username)))
		msg = append(msg, auth.Username...)
		msg = append(msg, byte(len(auth.Password)))
		msg = append(msg, auth.Password...)
		if _, err := conn.Write(msg); err != nil {
			return fmt.Errorf("socks: writing auth: %w", err)
		}
		var status [2]byte
		if _, err := io.ReadFull(conn, status[:]); err != nil {
			return fmt.Errorf("socks: reading auth response: %w", err)
		}
		if status[0] != authVersion || status[1] != 0x00 {
			return ErrAuthRejected
		}
	case sel[1] == methodNoAuth:
		// No authentication required.
	default:
		return ErrProxyProtocol
	}

	// Phase 3: CONNECT request. Always DOMAINNAME, per Core.
	req := make([]byte, 0, 7+len(dest))
	req = append(req, socksVersion, cmdConnect, 0x00, atypDomainName, byte(len(dest)))
	req = append(req, dest...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("socks: writing connect request: %w", err)
	}

	// Phase 4: reply header, then discard BND.ADDR/BND.PORT.
	var rep [4]byte
	if _, err := io.ReadFull(conn, rep[:]); err != nil {
		return fmt.Errorf("socks: reading connect reply: %w", err)
	}
	if rep[0] != socksVersion {
		return ErrProxyProtocol
	}
	if rep[1] != 0x00 {
		return &ReplyError{Code: rep[1]}
	}
	if rep[2] != 0x00 {
		return ErrProxyProtocol
	}
	var bndLen int
	switch rep[3] {
	case atypIPv4:
		bndLen = 4
	case atypIPv6:
		bndLen = 16
	case atypDomainName:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return fmt.Errorf("socks: reading bound address: %w", err)
		}
		bndLen = int(l[0])
	default:
		return ErrProxyProtocol
	}
	if _, err := io.CopyN(io.Discard, conn, int64(bndLen)+2); err != nil {
		return fmt.Errorf("socks: reading bound address: %w", err)
	}
	return nil
}
