// Copyright 2025-2026 The Parallax Protocol Authors
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

package p2p

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/p2p/rlpx/bip324handshake"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// v2Transport is the PIP-0006 Phase 2b transport. It wraps a
// bip324handshake.Conn, which provides BIP324-style authenticated
// encryption with no persistent peer identity. The identity exposed to
// the rest of p2p (so peer-dedup and the devp2p Hello round-trip keep
// working) is derived from the peer's ephemeral X25519 public key.
//
// See doc.go in p2p/rlpx/bip324handshake for the wire-format details
// and BIP324 deviations.
type v2Transport struct {
	rmu, wmu sync.Mutex
	wbuf     bytes.Buffer
	conn     *bip324handshake.Conn

	// inbound is true when this transport was dispatched from an
	// inbound first-byte peek; false for outbound dials. Determines
	// whether we call AcceptHandshake or DialHandshake in
	// doEncHandshake.
	inbound bool

	// After the handshake completes, localEphem / remoteEphem carry
	// the 32-byte X25519 pubkeys used to derive peer identity.
	// remoteEphem is used to synthesize the pubkey returned by
	// doEncHandshake so Server.nodeFromConn can compute a node.ID
	// deterministic for the session.
	localEphem  []byte
	remoteEphem []byte

	// deadline tracks the timeout set by doEncHandshake so ReadMsg
	// can reset the read deadline appropriately after the handshake.
	handshakeDeadline time.Time
}

// newV2Inbound wraps an already-peeked connection as an inbound v2
// transport. The caller must have already consumed (and verified) the
// bip324handshake.VersionMagic byte via bip324handshake.PeekVersion.
func newV2Inbound(conn net.Conn) *v2Transport {
	return &v2Transport{conn: bip324handshake.NewConn(conn), inbound: true}
}

// newV2Outbound wraps a freshly-dialed TCP connection as an outbound
// v2 transport. The caller has not sent the magic byte yet;
// doEncHandshake will do it as part of DialHandshake.
func newV2Outbound(conn net.Conn) *v2Transport {
	return &v2Transport{conn: bip324handshake.NewConn(conn), inbound: false}
}

// ----- transport interface implementation -----

func (t *v2Transport) doEncHandshake(_ *ecdsa.PrivateKey) (*ecdsa.PublicKey, error) {
	// Note: ignoring the secp256k1 private key — v2 has no persistent
	// identity. The underlying handshake generates fresh ephemeral
	// X25519 keys.
	deadline := time.Now().Add(handshakeTimeout)
	_ = t.conn.Underlying().SetDeadline(deadline)
	t.handshakeDeadline = deadline

	var err error
	if t.inbound {
		err = t.conn.AcceptHandshake()
	} else {
		err = t.conn.DialHandshake()
	}
	if err != nil {
		return nil, err
	}
	t.localEphem, t.remoteEphem = t.conn.SessionKeys()
	if len(t.remoteEphem) != 32 || len(t.localEphem) != 32 {
		return nil, errors.New("v2Transport: empty session keys after handshake")
	}
	// v2 has no persistent identity; Server.setupConn type-asserts
	// *v2Transport and calls v2NodeFromConn(t.remoteEphem, fd) to
	// build c.node, bypassing nodeFromConn entirely.
	return nil, nil
}

func (t *v2Transport) doProtoHandshake(our *protoHandshake) (*protoHandshake, error) {
	// Before writing the Hello we must rewrite our ID to be the
	// 64-byte pseudo-ID corresponding to our LOCAL ephemeral pubkey.
	// This keeps the post-handshake verify
	// (keccak(phs.ID) == node.ID for the remote) working on both
	// sides: each side's node.ID is derived from the other side's
	// phs.ID via keccak256.
	ourCopy := *our
	ourCopy.ID = localPseudoIDBytes(t.localEphem)

	werr := make(chan error, 1)
	go func() { werr <- Send(t, handshakeMsg, &ourCopy) }()
	their, err := readProtocolHandshake(t)
	if err != nil {
		<-werr
		return nil, err
	}
	if err := <-werr; err != nil {
		return nil, fmt.Errorf("write error: %v", err)
	}
	return their, nil
}

// ReadMsg reads one encrypted frame, then decodes (code, payload) from
// the plaintext using the same RLP prefix the legacy transport writes.
func (t *v2Transport) ReadMsg() (Msg, error) {
	t.rmu.Lock()
	defer t.rmu.Unlock()
	_ = t.conn.Underlying().SetReadDeadline(time.Now().Add(frameReadTimeout))

	plain, err := t.conn.Read()
	if err != nil {
		return Msg{}, err
	}
	code, data, err := rlp.SplitUint64(plain)
	if err != nil {
		return Msg{}, fmt.Errorf("v2 invalid message code: %v", err)
	}
	return Msg{
		ReceivedAt: time.Now(),
		Code:       code,
		Size:       uint32(len(data)),
		meterSize:  uint32(len(data)),
		// Copy so the caller can hold the buffer past the next Read.
		Payload: bytes.NewReader(append([]byte(nil), data...)),
	}, nil
}

// WriteMsg encodes (code, payload) as RLP(code)||payload inside a
// single AEAD frame. Matches the legacy wire format for the frame
// payload so no code downstream needs to know which transport served it.
func (t *v2Transport) WriteMsg(msg Msg) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_ = t.conn.Underlying().SetWriteDeadline(time.Now().Add(frameWriteTimeout))

	t.wbuf.Reset()
	// Prefix is RLP(code) — same encoding rlpx.Conn uses.
	if _, err := t.wbuf.Write(rlp.AppendUint64(nil, msg.Code)); err != nil {
		return err
	}
	if msg.Size > 0 {
		if _, err := io.CopyN(&t.wbuf, msg.Payload, int64(msg.Size)); err != nil {
			return err
		}
	}
	return t.conn.Write(t.wbuf.Bytes())
}

func (t *v2Transport) close(_ error) {
	// The v2 protocol has no disconnect-reason message; just close
	// the underlying connection. Callers that need to signal a reason
	// do so at the devp2p Disconnect level before close() is called.
	_ = t.conn.Close()
}

// v2SessionIDBytes produces the 64-byte identity representation for a
// given X25519 ephemeral pubkey. Used on both sides of the protocol:
//   - Sender writes its local ephem's v2SessionIDBytes as phs.ID in
//     the devp2p Hello.
//   - Receiver computes remote node.ID as keccak256(remote ephem's
//     v2SessionIDBytes). On wire, keccak256(phs.ID) matches.
//
// Layout: ephem (32 bytes) || SHA-256(ephem) (32 bytes). The hash half
// makes the ID a deterministic function of the ephem while guaranteeing
// 64 bytes for the p2p.Hello framing.
func v2SessionIDBytes(ephem []byte) []byte {
	h := sha256.Sum256(ephem)
	out := make([]byte, 64)
	copy(out[:32], ephem)
	copy(out[32:], h[:])
	return out
}

// localPseudoIDBytes is the legacy name kept for v2Transport's call
// site. Same semantics as v2SessionIDBytes.
func localPseudoIDBytes(localEphem []byte) []byte {
	return v2SessionIDBytes(localEphem)
}

// v2NodeFromConn builds an *enode.Node for a v2-authenticated peer
// without going through the secp256k1-pubkey path. Uses enode.SignNull
// to set the node.ID directly. Takes the remote's ephemeral X25519
// pubkey to derive a stable-per-session ID.
func v2NodeFromConn(remoteEphem []byte, fd net.Conn) *enode.Node {
	var r enr.Record
	if tcp, ok := fd.RemoteAddr().(*net.TCPAddr); ok {
		if ip := tcp.IP; ip != nil {
			r.Set(enr.IP(ip))
			r.Set(enr.TCP(tcp.Port))
		}
	}
	idBytes := v2SessionIDBytes(remoteEphem)
	id := enode.ID(crypto.Keccak256Hash(idBytes))
	return enode.SignNull(&r, id)
}

// Compile-time checks.
var (
	_ transport = (*v2Transport)(nil)
)
