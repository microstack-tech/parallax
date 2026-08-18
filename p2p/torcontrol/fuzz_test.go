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

package torcontrol

import (
	"bufio"
	"strings"
	"testing"
)

// FuzzReadReply — arbitrary bytes as a control-port reply stream. The
// parser must never panic; malformed input surfaces as an error, and
// success yields a 3-digit code.
func FuzzReadReply(f *testing.F) {
	f.Add("250 OK\r\n")
	f.Add("250-PROTOCOLINFO 1\r\n250-AUTH METHODS=NULL\r\n250 OK\r\n")
	f.Add("650 CIRC 1 BUILT\r\n250 OK\r\n")
	f.Add("250+data\r\nline1\r\n.\r\n250 OK\r\n")
	f.Add("2")
	f.Add("abc OK\r\n")
	f.Add("250\r\n")
	f.Add(strings.Repeat("650 X\r\n", 50))

	f.Fuzz(func(t *testing.T, stream string) {
		rep, err := readReply(bufio.NewReader(strings.NewReader(stream)))
		if err != nil {
			return
		}
		if rep.code < 100 || rep.code > 999 {
			t.Fatalf("accepted reply with out-of-range code %d", rep.code)
		}
		if len(rep.lines) == 0 {
			t.Fatal("accepted reply with no lines")
		}
	})
}

// FuzzParseMapping — the Key=Value/quoted-string argument parser must
// never panic or loop; any prefix of recognized pairs is acceptable
// output.
func FuzzParseMapping(f *testing.F) {
	f.Add(`METHODS=COOKIE,SAFECOOKIE COOKIEFILE="/x/control_auth_cookie"`)
	f.Add(`ServiceID=abcdef PrivateKey=ED25519-V3:xyz`)
	f.Add(`A="esc \"q\" \n \101 \41 \377"`)
	f.Add(`A="unterminated`)
	f.Add(`=`)
	f.Add(`A=`)
	f.Add(`A="\`)

	f.Fuzz(func(t *testing.T, s string) {
		_ = parseMapping(s)
	})
}
