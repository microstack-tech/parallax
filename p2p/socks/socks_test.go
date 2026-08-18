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
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// connectReq is what the fake server parsed out of one CONNECT exchange.
// Mirrors the fields Bitcoin Core's test/functional/socks5.py records and
// feature_proxy.py asserts on.
type connectReq struct {
	atyp     byte
	dest     string
	port     uint16
	username string
	password string
	authed   bool
}

// fakeServer is a single-shot scripted SOCKS5 server on 127.0.0.1.
type fakeServer struct {
	ln   net.Listener
	reqC chan connectReq
	errC chan error
}

type serverOpts struct {
	requireAuth bool // demand USER_PASS, fail if client can't
	rejectAuth  bool // fail the RFC 1929 subnegotiation
	replyCode   byte // REP code for the CONNECT reply
	replyAtyp   byte // ATYP of the bound address in the reply
	badVersion  bool // answer the greeting with a non-SOCKS5 version
	echo        bool // after success, echo one payload read back
}

func startFakeServer(t *testing.T, opts serverOpts) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, reqC: make(chan connectReq, 1), errC: make(chan error, 1)}
	t.Cleanup(func() { ln.Close() })
	go s.serveOne(opts)
	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) serveOne(opts serverOpts) {
	conn, err := s.ln.Accept()
	if err != nil {
		s.errC <- err
		return
	}
	defer conn.Close()
	var req connectReq

	fail := func(err error) { s.errC <- err }

	// Greeting.
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		fail(err)
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		fail(err)
		return
	}
	if opts.badVersion {
		conn.Write([]byte{0x04, methodNoAuth})
		fail(nil)
		return
	}
	offersUserPass := false
	for _, m := range methods {
		if m == methodUserPass {
			offersUserPass = true
		}
	}
	switch {
	case opts.requireAuth && !offersUserPass:
		conn.Write([]byte{socksVersion, methodNoAcceptable})
		fail(nil)
		return
	case opts.requireAuth:
		conn.Write([]byte{socksVersion, methodUserPass})
		// RFC 1929 subnegotiation.
		var ver [2]byte
		if _, err := io.ReadFull(conn, ver[:]); err != nil {
			fail(err)
			return
		}
		ulen := int(ver[1])
		ubuf := make([]byte, ulen)
		if _, err := io.ReadFull(conn, ubuf); err != nil {
			fail(err)
			return
		}
		var plenb [1]byte
		if _, err := io.ReadFull(conn, plenb[:]); err != nil {
			fail(err)
			return
		}
		pbuf := make([]byte, int(plenb[0]))
		if _, err := io.ReadFull(conn, pbuf); err != nil {
			fail(err)
			return
		}
		req.username, req.password, req.authed = string(ubuf), string(pbuf), true
		if opts.rejectAuth {
			conn.Write([]byte{authVersion, 0x01})
			s.reqC <- req
			fail(nil)
			return
		}
		conn.Write([]byte{authVersion, 0x00})
	default:
		conn.Write([]byte{socksVersion, methodNoAuth})
	}

	// CONNECT request.
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		fail(err)
		return
	}
	req.atyp = head[3]
	switch head[3] {
	case atypDomainName:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			fail(err)
			return
		}
		d := make([]byte, int(l[0]))
		if _, err := io.ReadFull(conn, d); err != nil {
			fail(err)
			return
		}
		req.dest = string(d)
	default:
		fail(errors.New("fake server: expected DOMAINNAME"))
		return
	}
	var portb [2]byte
	if _, err := io.ReadFull(conn, portb[:]); err != nil {
		fail(err)
		return
	}
	req.port = uint16(portb[0])<<8 | uint16(portb[1])
	s.reqC <- req

	// Reply.
	switch opts.replyAtyp {
	case atypDomainName:
		bound := "resolved.example"
		msg := append([]byte{socksVersion, opts.replyCode, 0x00, atypDomainName, byte(len(bound))}, bound...)
		conn.Write(append(msg, 0x1f, 0x90))
	case atypIPv6:
		msg := append([]byte{socksVersion, opts.replyCode, 0x00, atypIPv6}, make([]byte, 18)...)
		conn.Write(msg)
	default:
		conn.Write([]byte{socksVersion, opts.replyCode, 0x00, atypIPv4, 127, 0, 0, 1, 0x1f, 0x90})
	}
	if opts.replyCode != 0x00 {
		fail(nil)
		return
	}
	if opts.echo {
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil {
			fail(err)
			return
		}
		conn.Write(buf[:n])
	}
	fail(nil)
}

func (s *fakeServer) request(t *testing.T) connectReq {
	t.Helper()
	// Both channels may be ready (the server records the request and
	// then finishes); prefer the request.
	select {
	case r := <-s.reqC:
		return r
	default:
	}
	select {
	case r := <-s.reqC:
		return r
	case err := <-s.errC:
		t.Fatalf("server finished without a request: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for CONNECT request")
	}
	panic("unreachable")
}

func TestDialNoAuth(t *testing.T) {
	s := startFakeServer(t, serverOpts{echo: true})
	p := &Proxy{Addr: s.addr()}
	conn, err := p.Dial(context.Background(), "example.onion", 32110)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := s.request(t)
	if req.authed {
		t.Error("server saw auth for an unauthenticated proxy")
	}
	if req.atyp != atypDomainName || req.dest != "example.onion" || req.port != 32110 {
		t.Errorf("connect request = %+v", req)
	}
	// The stream must be transparent after negotiation.
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo = %q, err = %v", buf, err)
	}
}

func TestDialStreamIsolation(t *testing.T) {
	gen, err := NewIsolationGenerator()
	if err != nil {
		t.Fatal(err)
	}
	var seen []connectReq
	for range 2 {
		s := startFakeServer(t, serverOpts{requireAuth: true})
		p := &Proxy{Addr: s.addr(), Isolation: gen}
		conn, err := p.Dial(context.Background(), "203.0.113.7", 8333)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
		seen = append(seen, s.request(t))
	}
	for _, r := range seen {
		if !r.authed {
			t.Fatal("isolation proxy connected without credentials")
		}
		if r.username != r.password {
			t.Errorf("username %q != password %q (Core sets them identical)", r.username, r.password)
		}
	}
	if seen[0].username == seen[1].username {
		t.Errorf("consecutive connections shared credentials %q — circuits not isolated", seen[0].username)
	}
	prefix := seen[0].username[:strings.IndexByte(seen[0].username, '-')+1]
	if !strings.HasPrefix(seen[1].username, prefix) {
		t.Errorf("credentials %q and %q don't share the per-process prefix", seen[0].username, seen[1].username)
	}
}

func TestDialReplyError(t *testing.T) {
	for _, tc := range []struct {
		code byte
		want string
	}{
		{0x04, "host unreachable"},
		{0xf0, "onion service descriptor"},
	} {
		s := startFakeServer(t, serverOpts{replyCode: tc.code})
		p := &Proxy{Addr: s.addr()}
		_, err := p.Dial(context.Background(), "example.onion", 32110)
		var rep *ReplyError
		if !errors.As(err, &rep) || rep.Code != tc.code {
			t.Fatalf("code 0x%02x: got err %v", tc.code, err)
		}
		if !strings.Contains(rep.Error(), tc.want) {
			t.Errorf("error %q missing %q", rep.Error(), tc.want)
		}
	}
}

func TestDialAuthRejected(t *testing.T) {
	gen, err := NewIsolationGenerator()
	if err != nil {
		t.Fatal(err)
	}
	s := startFakeServer(t, serverOpts{requireAuth: true, rejectAuth: true})
	p := &Proxy{Addr: s.addr(), Isolation: gen}
	if _, err := p.Dial(context.Background(), "example.com", 80); !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("got %v, want ErrAuthRejected", err)
	}
}

func TestDialBadProxyVersion(t *testing.T) {
	s := startFakeServer(t, serverOpts{badVersion: true})
	p := &Proxy{Addr: s.addr()}
	if _, err := p.Dial(context.Background(), "example.com", 80); !errors.Is(err, ErrProxyProtocol) {
		t.Fatalf("got %v, want ErrProxyProtocol", err)
	}
}

func TestDialDomainBoundAddr(t *testing.T) {
	// A reply carrying a variable-length DOMAINNAME bound address must
	// parse; same for IPv6.
	for _, atyp := range []byte{atypDomainName, atypIPv6} {
		s := startFakeServer(t, serverOpts{replyAtyp: atyp})
		p := &Proxy{Addr: s.addr()}
		conn, err := p.Dial(context.Background(), "example.com", 80)
		if err != nil {
			t.Fatalf("atyp 0x%02x: %v", atyp, err)
		}
		conn.Close()
	}
}

func TestDialHostTooLong(t *testing.T) {
	s := startFakeServer(t, serverOpts{})
	p := &Proxy{Addr: s.addr()}
	long := strings.Repeat("a", 256)
	if _, err := p.Dial(context.Background(), long, 80); !errors.Is(err, ErrHostTooLong) {
		t.Fatalf("got %v, want ErrHostTooLong", err)
	}
}

func TestDialContextCancel(t *testing.T) {
	// A server that accepts and then goes silent: cancellation must
	// unblock the negotiation read.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn) // read forever, answer nothing
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	p := &Proxy{Addr: ln.Addr().String()}
	start := time.Now()
	_, err = p.Dial(ctx, "example.com", 80)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %v — watcher not closing the conn", elapsed)
	}
}
