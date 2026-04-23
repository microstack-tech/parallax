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

// Package bip324handshake implements Parallax's v2 RLPx handshake —
// a BIP324-inspired variant that does not depend on a pre-shared
// static peer identity. The handshake is dialed on IP:port alone and
// authenticates the transport to "whoever answered on that IP:port at
// session establishment time" — the same trust model Bitcoin adopted
// with BIP324.
//
// Pinned reference: Bitcoin Core tag v31.0, files
// `src/bip324.{h,cpp}`, `src/net.cpp`, and
// `src/crypto/chacha20poly1305.cpp`.
//
// Wire format (Parallax v2):
//
//	byte 0       : 0xA0  (version-negotiation magic, dispatched by the
//	               listener's first-byte peek; see version_negotiate.go)
//	bytes 1..32  : initiator's ephemeral X25519 public key
//	bytes 33..64 : responder's ephemeral X25519 public key (written
//	               after the listener has read the initiator key)
//
// Session keys are derived from the shared X25519 secret via
// HKDF-SHA256. Each direction gets one 32-byte ChaCha20-Poly1305 key.
// Frames are: 2-byte length (big-endian) || 12-byte nonce counter
// (implicit; not on wire) || ChaCha20-Poly1305(plaintext). The nonce
// counter is per-direction and starts at 0.
//
// Deviations from BIP324 (documented per the plan requirement):
//
//  1. No ElligatorSwift encoding. BIP324's XS-encoded pubkey makes the
//     handshake look uniform-random on wire, which matters when peers
//     cannot coordinate a version byte. Parallax uses the explicit
//     0xA0 version-negotiation byte so indistinguishability-from-random
//     is not required; we gain simpler math at the cost of a
//     protocol-header that identifies v2 traffic to a passive observer.
//  2. Framing is 2-byte length + AEAD payload, not BIP324's encrypted
//     length prefix. BIP324 encrypts the length to hide frame
//     boundaries; Parallax accepts plaintext lengths because we don't
//     advertise transport privacy as a v2.0 property, and the
//     application layer already exposes message boundaries to a
//     passive observer via idle-time analysis.
//  3. No garbage/authentication bytes. BIP324 reserves up to 4095
//     bytes of randomized garbage after the ellswift keys as an
//     additional hostile-middlebox hardening; we omit this because a
//     Parallax connection presents as RLPx (the listener's first-byte
//     peek already committed to v2), making the garbage redundant.
//  4. No session ID / peer-identified reconnect. BIP324's session
//     identifier exists because Bitcoin peers can in principle be
//     re-contacted by session-id-aware tooling; Parallax's v3.0
//     design treats every connection as brand-new and pins nothing to
//     a prior session.
//
// Security model (summarized — full argument in PIP-0006 §5.5):
//
//   - MITM between peers is not detectable at the transport layer.
//     Whoever answers on IP:port at handshake time IS the peer for
//     the duration of the session. Defenses move up the stack:
//     addrman source weighting and application-layer consensus
//     validation (blocks/headers must verify against PoW regardless
//     of who served them).
//   - Forward secrecy is provided by the ephemeral-only DH. Post-compromise
//     recovery requires no long-term private key rotation because
//     there is no long-term key in v2.
//   - Replay resistance comes from the counter-based nonce; a
//     recorded session cannot be replayed against the same peer pair
//     because the peer's ephemeral key changes on every new connection.
//
// Phase 2b scope: this package ships the handshake primitive and a
// standalone round-trip test. Listener dispatch (first-byte peek
// branching between legacy ECIES and v2) is implemented in
// version_negotiate.go. Plumbing into p2p.Server is gated behind the
// --experimental-v2-handshake flag and lands in a follow-up.
package bip324handshake
