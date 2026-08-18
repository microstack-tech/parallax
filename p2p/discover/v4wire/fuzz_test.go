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
	"net"
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/crypto"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enode"
	"github.com/ParallaxProtocol/parallax/v2/p2p/enr"
)

// FuzzV4WireDecode feeds raw bytes to Decode. Decode handles packets
// received straight off the UDP socket, so any panic here is a remote
// DoS. When Decode succeeds, the recovered sender key must be a valid
// curve point and the packet must survive a re-Encode/re-Decode round
// trip with the same Kind.
func FuzzV4WireDecode(f *testing.F) {
	key := genTestKey(f)

	var record enr.Record
	record.Set(enr.IPv4(net.ParseIP("10.0.0.1").To4()))
	record.Set(enr.UDP(30303))
	if err := enode.SignV4(&record, key); err != nil {
		f.Fatalf("cannot sign node record: %v", err)
	}

	// Seed with valid encodings of all six packet kinds.
	packets := []Packet{
		&Ping{
			Version:    4,
			From:       Endpoint{IP: net.ParseIP("127.0.0.1").To4(), UDP: 3322, TCP: 5544},
			To:         Endpoint{IP: net.ParseIP("::1"), UDP: 2222, TCP: 3333},
			Expiration: 1136239445,
			ENRSeq:     1,
		},
		&Pong{
			To:         Endpoint{IP: net.ParseIP("192.168.1.1").To4(), UDP: 30303, TCP: 30303},
			ReplyTok:   []byte{0x01, 0x02, 0x03, 0x04},
			Expiration: 1136239445,
			ENRSeq:     2,
		},
		&Findnode{
			Target:     EncodePubkey(&key.PublicKey),
			Expiration: 1136239445,
		},
		&Neighbors{
			Nodes: []Node{
				{IP: net.ParseIP("10.0.0.1").To4(), UDP: 30303, TCP: 30304, ID: EncodePubkey(&key.PublicKey)},
				{IP: net.ParseIP("2001:db8::1"), UDP: 3333, TCP: 4444, ID: EncodePubkey(&key.PublicKey)},
			},
			Expiration: 1136239445,
		},
		&ENRRequest{Expiration: 1136239445},
		&ENRResponse{ReplyTok: []byte{0xaa, 0xbb, 0xcc}, Record: record},
	}
	for _, p := range packets {
		f.Add(encodeTestPacket(f, key, p))
	}

	// Truncations around the minimum frame size: empty input, exactly
	// headSize (97, still too small), and headSize+1 (98, the minimum
	// that reaches hash/signature checking).
	frame := encodeTestPacket(f, key, &ENRRequest{Expiration: 1136239445})
	f.Add(frame[:0])
	f.Add(frame[:headSize])
	f.Add(frame[:headSize+1])

	// A validly framed packet with an unknown type byte.
	f.Add(signTestPacket(f, key, 0xff, []byte{0xc0}))

	f.Fuzz(func(t *testing.T, data []byte) {
		pkt, fromKey, hash, err := Decode(data)
		if err != nil {
			return
		}
		if len(hash) != macSize {
			t.Fatalf("Decode returned hash of length %d, want %d", len(hash), macSize)
		}
		// The recovered public key must decode to a valid curve point.
		if _, err := DecodePubkey(crypto.S256(), fromKey); err != nil {
			t.Fatalf("recovered pubkey %x is not decodable: %v", fromKey, err)
		}
		// Re-encoding with a test key and decoding again must preserve
		// the packet kind.
		reenc, _, err := Encode(key, pkt)
		if err != nil {
			t.Fatalf("cannot re-encode decoded %s packet: %v", pkt.Name(), err)
		}
		pkt2, _, _, err := Decode(reenc)
		if err != nil {
			t.Fatalf("cannot re-decode re-encoded %s packet: %v", pkt.Name(), err)
		}
		if pkt2.Kind() != pkt.Kind() {
			t.Fatalf("packet kind changed in round trip: got %d, want %d", pkt2.Kind(), pkt.Kind())
		}
	})
}
