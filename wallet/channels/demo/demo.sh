#!/usr/bin/env bash
# End-to-end payment-channel demo on a local devnet with real Nostr relays.
#
#   ./wallet/channels/demo/demo.sh            # full automated walkthrough
#   RELAYS="wss://relay.damus.io" ./demo.sh   # override the relay set
#   KEEP=1 ./demo.sh                          # leave devnet + daemon running
#
# What it does:
#   1. builds parallaxd + parallax-wallet
#   2. starts an ephemeral devnet (parallaxd --dev, 2s blocks)
#   3. creates two wallets (alice pays, bob receives), funds them
#   4. deploys ParallaxChannelRegistry
#   5. starts bob's channel daemon (talks to the relay)
#   6. alice: opens a 10-LAX channel -> handshake travels over the relay
#   7. alice pays bob 1.5 LAX -> countersignature comes back over the relay
#   8. cooperative close settles on-chain
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../../.." && pwd)"
DEMO="${DEMO_DIR:-$(mktemp -d /tmp/plx-channel-demo.XXXXXX)}"
# Two relays by default: some public relays (relay.damus.io notably)
# rate-limit writes from fresh, unknown keys, which is exactly what this
# demo generates — a single relay makes the whole run hinge on that policy.
RELAYS="${RELAYS:-wss://nos.lol wss://relay.primal.net}"
BIN="$DEMO/bin"
LOG="$DEMO/log"
mkdir -p "$BIN" "$LOG" "$DEMO/alice" "$DEMO/bob"

say()  { printf '\n\033[1;36m== %s\033[0m\n' "$*"; }
info() { printf '   %s\n' "$*"; }

cleanup() {
  if [[ "${KEEP:-0}" != "1" ]]; then
    kill "${BOB_PID:-0}" "${NODE_PID:-0}" 2>/dev/null || true
    info "stopped devnet and daemon (KEEP=1 to keep them running)"
  else
    say "left running (KEEP=1)"
    info "devnet:     pid ${NODE_PID:-?}  (logs: $LOG/parallaxd.log)"
    info "bob daemon: pid ${BOB_PID:-?}  (logs: $LOG/bob-daemon.log)"
    info "state dir:  $DEMO"
  fi
}
trap cleanup EXIT

say "building binaries"
(cd "$REPO" && go build -o "$BIN/parallaxd" ./cmd/parallaxd)
(cd "$REPO" && go build -o "$BIN/parallax-wallet" ./cmd/parallax-wallet)

say "starting devnet (chain id 1337, 2s blocks)"
"$BIN/parallaxd" --dev --dev.period 2 \
  --datadir "$DEMO/node" \
  --http --http.api eth,net,web3 \
  --ws --ws.api eth,net,web3 \
  >"$LOG/parallaxd.log" 2>&1 &
NODE_PID=$!
for i in $(seq 1 30); do
  if curl -sf -X POST -H 'Content-Type: application/json' \
      --data '{"jsonrpc":"2.0","method":"eth_chainId","id":1}' \
      http://127.0.0.1:8545 >/dev/null 2>&1; then break; fi
  sleep 1
  [[ $i == 30 ]] && { echo "devnet did not come up"; exit 1; }
done
info "rpc up at http://127.0.0.1:8545 / ws://127.0.0.1:8546"

say "creating wallets"
echo "demo" > "$DEMO/pw.txt"
"$BIN/parallax-wallet" generate --passwordfile "$DEMO/pw.txt" "$DEMO/alice/key.json" >/dev/null
"$BIN/parallax-wallet" generate --passwordfile "$DEMO/pw.txt" "$DEMO/bob/key.json"   >/dev/null
ALICE_ADDR=$("$BIN/parallax-wallet" inspect --json --passwordfile "$DEMO/pw.txt" "$DEMO/alice/key.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Address"])')
BOB_ADDR=$("$BIN/parallax-wallet" inspect --json --passwordfile "$DEMO/pw.txt" "$DEMO/bob/key.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["Address"])')
info "alice: $ALICE_ADDR"
info "bob:   $BOB_ADDR"

say "funding wallets from the dev account (100 LAX each)"
"$BIN/parallaxd" attach --datadir "$DEMO/node" --exec "
  eth.sendTransaction({from: eth.accounts[0], to: '$ALICE_ADDR', value: web3.toWei(100, 'ether')});
  eth.sendTransaction({from: eth.accounts[0], to: '$BOB_ADDR',   value: web3.toWei(100, 'ether')});
" >/dev/null
sleep 5
"$BIN/parallaxd" attach --datadir "$DEMO/node" --exec \
  "console.log('alice balance:', web3.fromWei(eth.getBalance('$ALICE_ADDR')), 'LAX')" 2>/dev/null | grep alice || true

say "deploying ParallaxChannelRegistry (CHALLENGE_REFUND = 0.01 LAX)"
REGISTRY=$(cd "$REPO" && go run ./wallet/channels/demo/deploy \
  --rpc http://127.0.0.1:8545 --keyfile "$DEMO/alice/key.json" --password "$DEMO/pw.txt")
info "registry: $REGISTRY"

say "writing configs (relays: $RELAYS)"
relay_toml=$(python3 - "$RELAYS" <<'EOF'
import sys
print(", ".join('"%s"' % r for r in sys.argv[1].split()))
EOF
)
for who in alice bob; do
  cat > "$DEMO/$who/config.toml" <<EOF
[node]
rpc = "ws://127.0.0.1:8546"
confirmations = 3

[registries]
  [[registries.v1]]
  address = "$REGISTRY"
  chain_id = 1337

[nostr]
relays = [$relay_toml]

[channels]
default_challenge_period = 144
accept_challenge_period_min = 36
accept_challenge_period_max = 1008
# 18 blocks is ~3h at mainnet's 10-min blocks; at the devnet's 2s blocks it
# would be a 36-second signature validity, too tight for public-relay
# delivery. 300 blocks = 10 minutes.
coop_close_validity_blocks = 300
withdraw_validity_blocks = 300

[merchant]
push_payments = true
EOF
done

# urfave/cli v1 stops flag parsing at the first positional argument, so
# every flag must precede the keyfile. wflags <who> prints the common set.
wflags() {
  echo --passwordfile "$DEMO/pw.txt" --config "$DEMO/$1/config.toml" --channeldata "$DEMO/$1"
}

say "identities (derived Nostr keys)"
"$BIN/parallax-wallet" nostr whoami $(wflags alice) "$DEMO/alice/key.json"
"$BIN/parallax-wallet" nostr whoami $(wflags bob)   "$DEMO/bob/key.json"
BOB_NPUB=$("$BIN/parallax-wallet" nostr whoami $(wflags bob) --json "$DEMO/bob/key.json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["npubHex"])')

say "starting bob's channel daemon (receiver)"
"$BIN/parallax-wallet" channel daemon $(wflags bob) "$DEMO/bob/key.json" >"$LOG/bob-daemon.log" 2>&1 &
BOB_PID=$!
sleep 3

say "alice opens a channel to bob: 10 LAX deposit, 144-block challenge period"
"$BIN/parallax-wallet" channel open $(wflags alice) \
  --npub "$BOB_NPUB" --deposit 10000000000000000000 --period 144 \
  "$DEMO/alice/key.json" "$BOB_ADDR"

say "waiting for the deposit to confirm (3 blocks) and bob to accept the handshake"
for i in $(seq 1 30); do
  if grep -q "handshake accepted" "$LOG/bob-daemon.log" 2>/dev/null; then break; fi
  sleep 2
done
grep -q "handshake accepted" "$LOG/bob-daemon.log" \
  && info "bob accepted the channel (consent checks passed)" \
  || { echo "!! handshake never arrived — check $LOG/bob-daemon.log and relay reachability"; exit 1; }
sleep 12  # bob's watcher credits alice's deposit at 3 confirmations

say "channel state before payment"
"$BIN/parallax-wallet" channel list $(wflags alice) "$DEMO/alice/key.json"

say "alice pays bob 1.5 LAX over the relay"
"$BIN/parallax-wallet" channel pay $(wflags alice) --wait 90s \
  "$DEMO/alice/key.json" 1 1500000000000000000

say "channel state after payment (alice's view; bob's store is held by his daemon)"
"$BIN/parallax-wallet" channel list $(wflags alice) "$DEMO/alice/key.json"

say "cooperative close"
"$BIN/parallax-wallet" channel close $(wflags alice) --wait 120s "$DEMO/alice/key.json" 1

say "final state (both views; bob's daemon stopped to release his store)"
"$BIN/parallax-wallet" channel list $(wflags alice) "$DEMO/alice/key.json"
kill "$BOB_PID" 2>/dev/null || true; wait "$BOB_PID" 2>/dev/null || true
"$BIN/parallax-wallet" channel list $(wflags bob) "$DEMO/bob/key.json"

say "done — a payment went devnet -> $RELAYS -> devnet settlement"
info "artifacts in $DEMO (KEEP=1 to keep devnet+daemon alive next time)"
