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

package bip324handshake

import (
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"crypto/sha256"
)

// VersionMagic is the first byte an initiator writes on a v2
// connection. The listener's first-byte peek branches on this value;
// legacy RLPx starts with a byte in the ECIES magic range, which is
// disjoint from 0xA0. See version_negotiate.go for the dispatcher.
const VersionMagic byte = 0xA0

// KeyLen is the wire length of an X25519 public key.
const KeyLen = 32

// NonceLen is the ChaCha20-Poly1305 nonce length (12 bytes = 96 bits).
const NonceLen = chacha20poly1305.NonceSize

// MaxFrameLen caps the size of a single post-handshake frame. Matches
// the rest of the p2p stack's MaxMessageSize (~16 MiB is the legacy
// RLPx cap; we pick a tighter 1 MiB for v2 since parallax-disc/1 and
// parallax/* messages don't need more).
const MaxFrameLen = 1 << 20 // 1 MiB

// Conn is a v2-handshake-authenticated bidirectional session. After
// Handshake succeeds, callers use Read/Write for length-prefixed AEAD
// frames. The v2 session is NOT wire-compatible with legacy rlpx.Conn;
// the outer p2p.Server dispatches based on the version-negotiation
// byte and wraps whichever one wins into a MsgReadWriter at a higher
// layer.
type Conn struct {
	conn net.Conn

	// Populated after Handshake.
	sendAEAD cipher.AEAD
	recvAEAD cipher.AEAD

	sendMu    sync.Mutex
	sendNonce uint64

	recvMu    sync.Mutex
	recvNonce uint64

	// Session identity material. Set during Dial/AcceptHandshake;
	// exposed via SessionKeys so upstream transports can derive a
	// peer identity from the 32-byte X25519 keys.
	localEphem  []byte
	remoteEphem []byte
}

// NewConn wraps a net.Conn. Call DialHandshake (initiator) or
// AcceptHandshake (responder) before Read/Write.
func NewConn(c net.Conn) *Conn {
	return &Conn{conn: c}
}

// Initiator errors — exported so callers can branch cleanly.
var (
	ErrWrongMagic    = errors.New("bip324handshake: wrong version magic byte")
	ErrShortRead     = errors.New("bip324handshake: short read during handshake")
	ErrInvalidKey    = errors.New("bip324handshake: invalid ephemeral public key")
	ErrHandshakeDone = errors.New("bip324handshake: Handshake already completed")
	ErrNotEstablished = errors.New("bip324handshake: Read/Write before Handshake")
	ErrFrameTooLarge = errors.New("bip324handshake: frame exceeds MaxFrameLen")
	ErrBadFrame      = errors.New("bip324handshake: frame authentication failed")
)

// DialHandshake performs the v2 handshake as the initiator. The caller
// has already written the TCP SYN and is holding a raw net.Conn.
//
// Protocol:
//  1. We write VersionMagic || initiator_pub.
//  2. Peer writes responder_pub.
//  3. Both compute the X25519 shared secret and derive session keys.
func (c *Conn) DialHandshake() error {
	if c.sendAEAD != nil {
		return ErrHandshakeDone
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("bip324handshake: keygen: %w", err)
	}

	out := make([]byte, 1+KeyLen)
	out[0] = VersionMagic
	copy(out[1:], priv.PublicKey().Bytes())
	if _, err := c.conn.Write(out); err != nil {
		return fmt.Errorf("bip324handshake: write init: %w", err)
	}

	var peerPub [KeyLen]byte
	if _, err := io.ReadFull(c.conn, peerPub[:]); err != nil {
		return fmt.Errorf("bip324handshake: read peer pub: %w", err)
	}
	peer, err := ecdh.X25519().NewPublicKey(peerPub[:])
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	shared, err := priv.ECDH(peer)
	if err != nil {
		return fmt.Errorf("bip324handshake: ecdh: %w", err)
	}

	// Direction labels include both pubkeys so the key derivation is
	// binding on the exact session (a key cannot be replayed against
	// a different handshake round).
	initPub := priv.PublicKey().Bytes()
	c.sendAEAD, err = deriveAEAD(shared, "i2r", initPub, peerPub[:])
	if err != nil {
		return err
	}
	c.recvAEAD, err = deriveAEAD(shared, "r2i", initPub, peerPub[:])
	if err != nil {
		return err
	}
	c.localEphem = append([]byte(nil), initPub...)
	c.remoteEphem = append([]byte(nil), peerPub[:]...)
	return nil
}

// AcceptHandshake performs the v2 handshake as the responder. The
// caller has already peeked and consumed the VersionMagic byte via
// version_negotiate.go.
func (c *Conn) AcceptHandshake() error {
	if c.sendAEAD != nil {
		return ErrHandshakeDone
	}
	// Read the initiator's pubkey (VersionMagic already consumed by
	// the dispatcher).
	var initPub [KeyLen]byte
	if _, err := io.ReadFull(c.conn, initPub[:]); err != nil {
		return fmt.Errorf("bip324handshake: read init pub: %w", err)
	}
	peer, err := ecdh.X25519().NewPublicKey(initPub[:])
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("bip324handshake: keygen: %w", err)
	}
	if _, err := c.conn.Write(priv.PublicKey().Bytes()); err != nil {
		return fmt.Errorf("bip324handshake: write pub: %w", err)
	}
	shared, err := priv.ECDH(peer)
	if err != nil {
		return fmt.Errorf("bip324handshake: ecdh: %w", err)
	}

	respPub := priv.PublicKey().Bytes()
	// Responder reads what the initiator labelled "i2r"; writes "r2i".
	c.recvAEAD, err = deriveAEAD(shared, "i2r", initPub[:], respPub)
	if err != nil {
		return err
	}
	c.sendAEAD, err = deriveAEAD(shared, "r2i", initPub[:], respPub)
	if err != nil {
		return err
	}
	c.localEphem = append([]byte(nil), respPub...)
	c.remoteEphem = append([]byte(nil), initPub[:]...)
	return nil
}

// SessionKeys returns (localEphem, remoteEphem) — the 32-byte X25519
// public keys exchanged during the handshake. Valid only after a
// successful Dial/AcceptHandshake. Both slices are copies; callers
// may modify or retain them freely.
func (c *Conn) SessionKeys() ([]byte, []byte) {
	return append([]byte(nil), c.localEphem...), append([]byte(nil), c.remoteEphem...)
}

// Write sends one length-prefixed AEAD frame. Thread-safe.
func (c *Conn) Write(plaintext []byte) error {
	if c.sendAEAD == nil {
		return ErrNotEstablished
	}
	if len(plaintext) > MaxFrameLen {
		return ErrFrameTooLarge
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	nonce := make([]byte, NonceLen)
	binary.BigEndian.PutUint64(nonce[4:], c.sendNonce)
	ct := c.sendAEAD.Seal(nil, nonce, plaintext, nil)

	// Wire frame: 3-byte big-endian length (matches legacy rlpx
	// 24-bit) || AEAD ciphertext. 3-byte length gives us 16 MiB cap
	// structurally, enforced tighter by MaxFrameLen.
	if len(ct) > 0xFFFFFF {
		return ErrFrameTooLarge
	}
	header := []byte{byte(len(ct) >> 16), byte(len(ct) >> 8), byte(len(ct))}
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if _, err := c.conn.Write(ct); err != nil {
		return err
	}
	c.sendNonce++
	return nil
}

// Read returns the next plaintext frame. Thread-safe.
func (c *Conn) Read() ([]byte, error) {
	if c.recvAEAD == nil {
		return nil, ErrNotEstablished
	}
	c.recvMu.Lock()
	defer c.recvMu.Unlock()

	var header [3]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return nil, err
	}
	n := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
	if n < c.recvAEAD.Overhead() {
		return nil, fmt.Errorf("%w: frame shorter than AEAD tag (%d)", ErrBadFrame, n)
	}
	if n > MaxFrameLen+c.recvAEAD.Overhead() {
		return nil, ErrFrameTooLarge
	}
	ct := make([]byte, n)
	if _, err := io.ReadFull(c.conn, ct); err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceLen)
	binary.BigEndian.PutUint64(nonce[4:], c.recvNonce)
	pt, err := c.recvAEAD.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadFrame, err)
	}
	c.recvNonce++
	return pt, nil
}

// Close closes the underlying net.Conn.
func (c *Conn) Close() error { return c.conn.Close() }

// Underlying returns the wrapped net.Conn. Caller must not read or
// write to it without coordinating with the Conn's session state.
func (c *Conn) Underlying() net.Conn { return c.conn }

// deriveAEAD returns a ChaCha20-Poly1305 AEAD keyed by
// HKDF-SHA256(shared, direction_label || initPub || respPub).
// Including both pubkeys in the info string binds the key to the
// specific handshake transcript.
func deriveAEAD(shared []byte, label string, initPub, respPub []byte) (cipher.AEAD, error) {
	info := make([]byte, 0, len(label)+KeyLen*2)
	info = append(info, label...)
	info = append(info, initPub...)
	info = append(info, respPub...)
	kdf := hkdf.New(sha256.New, shared, nil, info)
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(kdf, key); err != nil {
		return nil, err
	}
	return chacha20poly1305.New(key)
}
