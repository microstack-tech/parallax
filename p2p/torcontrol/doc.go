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

// Package torcontrol maintains a Tor v3 onion service for the node's
// P2P listener via the Tor control protocol (PIP-0007 §3). It is a
// port of Bitcoin Core's TorController (src/torcontrol.cpp): connect
// to the control port, authenticate (HASHEDPASSWORD when a password is
// supplied, otherwise NULL, otherwise SAFECOOKIE), issue ADD_ONION
// with the persisted ed25519 service key (or NEW:ED25519-V3 on first
// run), cache the returned key to disk with 0600 permissions, and
// reconnect with exponential backoff when the control connection
// drops — re-issuing ADD_ONION each time, since Tor forgets ephemeral
// services on controller disconnect.
//
// The command subset spoken here is PROTOCOLINFO, AUTHCHALLENGE,
// AUTHENTICATE, and ADD_ONION, per the control-spec. The controller
// never subscribes to events, so unsolicited 6xx lines are skipped.
//
// Pinned reference: Bitcoin Core tag v31.0.
package torcontrol
