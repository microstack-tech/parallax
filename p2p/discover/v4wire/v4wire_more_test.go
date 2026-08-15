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

package v4wire

import (
	"crypto/ecdsa"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/ParallaxProtocol/parallax/crypto"
	"github.com/ParallaxProtocol/parallax/p2p/enode"
	"github.com/ParallaxProtocol/parallax/p2p/enr"
	"github.com/ParallaxProtocol/parallax/primitives/rlp"
)

// encodeTestPacket encodes req with the given key and fails the test on error.
// It is shared with the fuzz target as the seed corpus constructor.
func encodeTestPacket(t testing.TB, key *ecdsa.PrivateKey, req Packet) []byte {
	t.Helper()
	packet, _, err := Encode(key, req)
	if err != nil {
		t.Fatalf("cannot encode %s packet: %v", req.Name(), err)
	}
	return packet
}

// signTestPacket builds a packet frame around an arbitrary type byte and
// payload, computing a valid signature and hash. This allows constructing
// packets that Encode itself cannot produce (e.g. unknown packet types).
func signTestPacket(t testing.TB, key *ecdsa.PrivateKey, ptype byte, payload []byte) []byte {
	t.Helper()
	b := make([]byte, headSize, headSize+1+len(payload))
	b = append(b, ptype)
	b = append(b, payload...)
	sig, err := crypto.Sign(crypto.Keccak256(b[headSize:]), key)
	if err != nil {
		t.Fatalf("cannot sign packet: %v", err)
	}
	copy(b[macSize:], sig)
	copy(b, crypto.Keccak256(b[macSize:]))
	return b
}

func genTestKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("cannot generate key: %v", err)
	}
	return key
}

// This test checks that Decode rejects packets which are shorter than the
// minimum frame size (hash + signature + type byte).
func TestDecodeShortPacket(t *testing.T) {
	for _, size := range []int{0, 1, macSize, headSize - 1, headSize} {
		_, _, _, err := Decode(make([]byte, size))
		if !errors.Is(err, ErrPacketTooSmall) {
			t.Errorf("length %d: got error %v, want %v", size, err, ErrPacketTooSmall)
		}
	}
	// headSize+1 is the minimum length that passes the size check.
	_, _, _, err := Decode(make([]byte, headSize+1))
	if errors.Is(err, ErrPacketTooSmall) {
		t.Errorf("length %d: got %v, want a different error", headSize+1, err)
	}
}

// This test checks that Decode rejects packets whose hash prefix does not
// match the rest of the packet.
func TestDecodeBadHash(t *testing.T) {
	key := genTestKey(t)
	packet := encodeTestPacket(t, key, &Ping{
		Version:    4,
		From:       Endpoint{IP: net.ParseIP("127.0.0.1").To4(), UDP: 3322, TCP: 5544},
		To:         Endpoint{IP: net.ParseIP("::1"), UDP: 2222, TCP: 3333},
		Expiration: 1136239445,
	})

	// Flip one bit in each byte position of the hash prefix.
	for i := range macSize {
		bad := make([]byte, len(packet))
		copy(bad, packet)
		bad[i] ^= 0x01
		_, _, _, err := Decode(bad)
		if !errors.Is(err, ErrBadHash) {
			t.Errorf("hash byte %d corrupted: got error %v, want %v", i, err, ErrBadHash)
		}
	}
}

// This test checks that a corrupted signature does not yield the signer's key.
func TestDecodeBadSignature(t *testing.T) {
	key := genTestKey(t)
	signer := EncodePubkey(&key.PublicKey)
	packet := encodeTestPacket(t, key, &ENRRequest{Expiration: 1136239445})

	// rehash fixes up the hash prefix so the corruption is not
	// caught by the hash check before signature recovery runs.
	rehash := func(p []byte) {
		copy(p, crypto.Keccak256(p[macSize:]))
	}

	// An invalid recovery ID must be rejected outright.
	bad := make([]byte, len(packet))
	copy(bad, packet)
	bad[headSize-1] = 0xff // recovery ID byte
	rehash(bad)
	if _, _, _, err := Decode(bad); err == nil {
		t.Error("packet with invalid recovery ID was accepted")
	}

	// A flipped signature byte must either fail or recover a different key.
	bad = make([]byte, len(packet))
	copy(bad, packet)
	bad[macSize] ^= 0x01 // first byte of r
	rehash(bad)
	_, fromKey, _, err := Decode(bad)
	if err == nil && fromKey == signer {
		t.Error("packet with corrupted signature recovered the signer's key")
	}
}

// This test checks that Decode rejects packet types it does not know about.
func TestDecodeUnknownPacketType(t *testing.T) {
	key := genTestKey(t)
	// 0xc0 is an empty RLP list, a plausible payload for any packet type.
	for _, ptype := range []byte{0, ENRResponsePacket + 1, 0x42, 0xff} {
		packet := signTestPacket(t, key, ptype, []byte{0xc0})
		_, _, _, err := Decode(packet)
		if err == nil {
			t.Errorf("packet type %d was accepted", ptype)
		}
	}
}

// This test checks that all packet kinds survive an Encode/Decode round trip.
func TestEncodeDecodeRoundtrip(t *testing.T) {
	key := genTestKey(t)
	wantKey := EncodePubkey(&key.PublicKey)
	expiration := uint64(time.Now().Add(20 * time.Second).Unix())

	// The decoder leaves an empty (non-nil) tail slice behind, so expected
	// packets carry an empty Rest to keep reflect.DeepEqual comparable.
	emptyRest := []rlp.RawValue{}

	var record enr.Record
	record.Set(enr.IPv4(net.ParseIP("10.0.0.1").To4()))
	record.Set(enr.UDP(30303))
	if err := enode.SignV4(&record, key); err != nil {
		t.Fatalf("cannot sign node record: %v", err)
	}

	packets := []Packet{
		&Ping{
			Version:    4,
			From:       Endpoint{IP: net.ParseIP("127.0.0.1").To4(), UDP: 3322, TCP: 5544},
			To:         Endpoint{IP: net.ParseIP("::1"), UDP: 2222, TCP: 3333},
			Expiration: expiration,
			ENRSeq:     1,
			Rest:       emptyRest,
		},
		&Pong{
			To:         Endpoint{IP: net.ParseIP("192.168.1.1").To4(), UDP: 30303, TCP: 30303},
			ReplyTok:   []byte{0x01, 0x02, 0x03, 0x04},
			Expiration: expiration,
			ENRSeq:     2,
			Rest:       emptyRest,
		},
		&Findnode{
			Target:     EncodePubkey(&key.PublicKey),
			Expiration: expiration,
			Rest:       emptyRest,
		},
		&Neighbors{
			Nodes: []Node{
				{
					IP:  net.ParseIP("10.0.0.1").To4(),
					UDP: 30303,
					TCP: 30304,
					ID:  EncodePubkey(&key.PublicKey),
				},
				{
					IP:  net.ParseIP("2001:db8::1"),
					UDP: 3333,
					TCP: 4444,
					ID:  EncodePubkey(&key.PublicKey),
				},
			},
			Expiration: expiration,
			Rest:       emptyRest,
		},
		&ENRRequest{Expiration: expiration, Rest: emptyRest},
		&ENRResponse{
			ReplyTok: []byte{0xaa, 0xbb, 0xcc},
			Record:   record,
		},
	}

	for _, want := range packets {
		t.Run(want.Name(), func(t *testing.T) {
			packet := encodeTestPacket(t, key, want)
			got, fromKey, hash, err := Decode(packet)
			if err != nil {
				t.Fatalf("cannot decode: %v", err)
			}
			if got.Kind() != want.Kind() {
				t.Errorf("got kind %d, want %d", got.Kind(), want.Kind())
			}
			if fromKey != wantKey {
				t.Errorf("got key %x, want %x", fromKey, wantKey)
			}
			if len(hash) != macSize {
				t.Errorf("got hash length %d, want %d", len(hash), macSize)
			}
			if resp, ok := want.(*ENRResponse); ok {
				// enr.Record carries unexported decoding state, so compare
				// the meaningful fields instead of the full structure.
				gotResp := got.(*ENRResponse)
				if !reflect.DeepEqual(gotResp.ReplyTok, resp.ReplyTok) {
					t.Errorf("got ReplyTok %x, want %x", gotResp.ReplyTok, resp.ReplyTok)
				}
				if gotResp.Record.Seq() != resp.Record.Seq() {
					t.Errorf("got record seq %d, want %d", gotResp.Record.Seq(), resp.Record.Seq())
				}
				var ip enr.IPv4
				if err := gotResp.Record.Load(&ip); err != nil {
					t.Errorf("cannot load IP from decoded record: %v", err)
				} else if !net.IP(ip).Equal(net.ParseIP("10.0.0.1")) {
					t.Errorf("got record IP %v, want 10.0.0.1", net.IP(ip))
				}
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip mismatch:\ngot  %#v\nwant %#v", got, want)
			}
		})
	}
}

// This test checks the expiration timestamp helper.
func TestExpired(t *testing.T) {
	now := time.Now()
	if !Expired(0) {
		t.Error("Expired(0) = false, want true")
	}
	if !Expired(uint64(now.Add(-time.Minute).Unix())) {
		t.Error("timestamp one minute in the past reported as not expired")
	}
	if Expired(uint64(now.Add(time.Hour).Unix())) {
		t.Error("timestamp one hour in the future reported as expired")
	}
}
