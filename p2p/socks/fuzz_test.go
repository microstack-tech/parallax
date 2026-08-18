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
	"io"
	"net"
	"testing"
	"time"
)

// FuzzNegotiate — arbitrary bytes as the proxy's side of the SOCKS5
// negotiation. The client must never panic and must always terminate:
// the fuzz server writes the payload and closes, so every read either
// completes or errors immediately.
func FuzzNegotiate(f *testing.F) {
	// Happy path: no-auth accepted, CONNECT succeeded, IPv4 bound.
	f.Add([]byte{0x05, 0x00, 0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x1f, 0x90}, false)
	// USER_PASS selected, auth ok, DOMAINNAME bound address.
	f.Add([]byte{0x05, 0x02, 0x01, 0x00, 0x05, 0x00, 0x00, 0x03, 0x04, 't', 'e', 's', 't', 0x00, 0x50}, true)
	// Reply error + Tor extension code.
	f.Add([]byte{0x05, 0x00, 0x05, 0xf0, 0x00, 0x01, 0, 0, 0, 0, 0, 0}, false)
	// Truncations and garbage.
	f.Add([]byte{0x05}, false)
	f.Add([]byte{0x04, 0x00}, true)
	f.Add([]byte{}, false)

	f.Fuzz(func(t *testing.T, script []byte, withAuth bool) {
		client, server := net.Pipe()
		defer client.Close()
		go func() {
			defer server.Close()
			// Drain whatever the client writes while feeding the
			// scripted reply bytes, then close.
			go io.Copy(io.Discard, server)
			server.Write(script)
		}()

		var auth *Credentials
		if withAuth {
			auth = &Credentials{Username: "u", Password: "p"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = negotiate(ctx, client, "example.onion", 32110, auth)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("negotiate failed to terminate on closed input")
		}
	})
}
