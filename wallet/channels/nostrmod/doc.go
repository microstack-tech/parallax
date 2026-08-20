// Package nostrmod implements the Nostr transport for Parallax channels
// (Parallax Channels spec, Part 2): relay pool, NIP-44 v2 encryption,
// NIP-59 gift wrapping, subscriptions, and retransmission.
//
// Library vetting (spec Part 3 §1 requires NIP-44 v2 and NIP-59 to be
// verified against reference vectors before use):
//
//   - NIP-44 v2: github.com/nbd-wtf/go-nostr/nip44 (pinned v0.52.3) passes
//     all 72 official reference vectors from paulmillr/nip44 — 35 valid
//     conversation keys, 10 encrypt/decrypt with fixed nonces (byte-exact
//     payloads), 3 long-message digests, and 24 invalid inputs rejected.
//     The vectors are pinned in testdata/ and re-verified by
//     nip44_vectors_test.go on every CI run, so a dependency bump that
//     regresses the implementation fails loudly. USED AS-IS.
//
//   - NIP-59: go-nostr's nip59 package backdates wrap/seal timestamps only
//     up to ~10 hours using math/rand, while spec Part 2 §2 requires
//     randomization up to 48 hours. REIMPLEMENTED HERE (wrap.go) on top of
//     the vetted nip44 primitives and go-nostr's event types; go-nostr's
//     ephemeral wrap keys (crypto/rand) remain in use via
//     nostr.GeneratePrivateKey.
package nostrmod
