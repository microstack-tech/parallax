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

package netparams_test

import (
	"testing"

	"github.com/ParallaxProtocol/parallax/v2/p2p/addrman"
	"github.com/ParallaxProtocol/parallax/v2/p2p/netparams"
)

// TestBootnodesV2Parse guards every hardcoded v2 bootstrap entry
// against typos: IP entries must be well-formed host:port and onion
// entries must carry a valid rend-spec-v3 checksum — a bad entry here
// would otherwise crash startup via logging.Crit in setBootstrapNodes.
func TestBootnodesV2Parse(t *testing.T) {
	for _, list := range [][]string{netparams.MainnetBootnodesV2, netparams.TestnetBootnodesV2} {
		for _, e := range list {
			if _, err := addrman.ParseHostPort(e); err != nil {
				t.Errorf("bootnode %q: %v", e, err)
			}
		}
	}
}
