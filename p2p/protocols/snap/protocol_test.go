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

package snap

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/p2p/rlpx/bip324handshake"
)

// TestMaxMessageFitsV2Frame pins maxMessageSize to the v2 transport's
// frame cap. The v2 transport writes each message as a single AEAD
// frame (no fragmentation), so any message the protocol permits must
// fit in one frame or WriteMsg fails and the connection is torn down.
// The 16-byte allowance covers the RLP-encoded message code prefix.
func TestMaxMessageFitsV2Frame(t *testing.T) {
	if maxMessageSize+16 > bip324handshake.MaxFrameLen {
		t.Fatalf("maxMessageSize %d does not fit v2 MaxFrameLen %d",
			maxMessageSize, bip324handshake.MaxFrameLen)
	}
}
