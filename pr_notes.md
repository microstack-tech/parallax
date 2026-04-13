## Summary

Restructure the Parallax client from go-ethereum's flat package layout into a layered architecture with strict dependency ordering. Rename the main binary from `prlx` to `parallax`.

### What changed

**Layered architecture introduced:**

- `kernel/` — RPC-free consensus engines (xhash, clique), chain parameters, and consensus interface. Cannot import rpc, p2p, node, or wallet.
- `validation/` — blockchain, state, tx pool (formerly `core/`)
- `script/` — PVM execution engine and ABI codec (formerly `core/vm/`, `accounts/abi/`)
- `primitives/` — fundamental types and serialization (`rlp/`, `types/`)
- `policy/` — fee estimation oracle
- `node/` — node lifecycle, mining (`miner/`), protocol handler (`protocol/`), stats, console
- `net/` — networking: `p2p/`, network parameters
- `wallet/` — account management, keystore, hardware wallets, signer (formerly `accounts/`, `signer/`)
- `rpc/` — JSON-RPC server, GraphQL transport, typed RPC client
- `support/` — event system, metrics

**Consensus wrapper eliminated:**

- Deleted the `consensus/` package entirely — it was a thin wrapper that added `APIs()` to kernel engines
- RPC API registration now happens directly in the server layer via type-switch (`node/fullnode/backend.go`, `node/light/client.go`)
- All 88 files switched from `consensus/*` imports to `kernel/*`

**Dead packages removed:**

- `swarm/` (empty, just a README)
- `prlx/` (unused mobile bindings)
- `contracts/` (checkpointoracle moved to `node/light/contracts/`)
- `cmd/checkpoint-admin/` (unused)

**Binary renamed:**

- `cmd/prlx/` → `cmd/parallax/` — main binary is now `parallax` instead of `prlx`
- Updated Makefile targets, build scripts, IPC socket names, metrics prefixes, and all user-facing references

**Network parameters separated from kernel:**

- `bootnodes.go` and `network_params.go` moved from `kernel/chainparams/` to `net/netparams/` — networking config doesn't belong in the consensus layer

**Version info moved to repo root:**

- `version.go` moved from `kernel/chainparams/` to the root `parallax` package — application version is not a chain parameter

**Complete directory rename map:**

| Before | After |
|--------|-------|
| `common/` | `util/` |
| `log/` | `logging/` |
| `prldb/` | `dbstore/` |
| `rlp/` | `primitives/rlp/` |
| `core/types/` | `primitives/types/` |
| `core/` | `validation/` |
| `core/vm/` | `script/` |
| `accounts/abi/` | `script/abi/` |
| `params/` | `kernel/chainparams/` |
| `event/` | `support/event/` |
| `metrics/` | `support/metrics/` |
| `accounts/` | `wallet/` |
| `signer/` | `wallet/signer/` |
| `p2p/` | `net/p2p/` |
| `les/` | `node/light/` |
| `light/` | `node/light/light/` |
| `miner/` | `node/miner/` |
| `prl/` | `node/fullnode/` |
| `prlstats/` | `node/stats/` |
| `console/` | `node/console/` |
| `graphql/` | `rpc/graphql/` |
| `prlclient/` | `rpc/client/` |
| `internal/prlapi/` | `internal/api/` |
| `consensus/` | *(deleted)* |
| `swarm/` | *(deleted)* |
| `contracts/` | *(deleted, moved to `node/light/contracts/`)* |
| `cmd/prlx/` | `cmd/parallax/` |

**Package names updated to match directories** using `gofmt -r` for AST-aware rewriting (e.g., `package common` → `package util`, all `common.Hash` → `util.Hash`).

### Final directory structure

```
parallax/
├── cmd/              — binaries (parallax, clef, pvm, abigen, etc.)
├── crypto/           — cryptographic primitives
├── dbstore/          — database abstraction (leveldb, memorydb)
├── internal/         — private utilities (api, debug, jsre, etc.)
├── kernel/           — RPC-free consensus layer
│   ├── chainparams/  — chain configuration, denomination, genesis
│   ├── clique/       — PoA consensus engine
│   ├── consensus/    — consensus interface (no rpc dependency)
│   ├── misc/         — fork verification, gas limit, EIP-1559
│   └── xhash/        — PoW consensus engine
├── logging/          — structured logging framework
├── net/              — networking layer
│   ├── les/          — light client protocol
│   ├── netparams/    — bootnodes, network constants
│   └── p2p/          — peer-to-peer networking
├── node/             — node lifecycle and services
│   ├── console/      — interactive JavaScript console
│   ├── miner/        — block mining
│   ├── protocol/     — full node protocol handler
│   └── stats/        — network statistics reporting
├── policy/           — transaction policy
│   └── fees/         — fee estimation oracle
├── primitives/       — fundamental data types
│   ├── rlp/          — RLP encoding/decoding
│   └── types/        — block, transaction, receipt types
├── rpc/              — RPC layer
│   ├── client/       — typed RPC client
│   └── graphql/      — GraphQL transport
├── script/           — smart contract execution
│   ├── abi/          — ABI codec and contract bindings
│   └── runtime/      — PVM runtime
├── support/          — support utilities
│   ├── event/        — event pub/sub system
│   └── metrics/      — metrics collection
├── tests/            — test vectors and fuzzers
├── util/             — common types and utilities (Address, Hash, etc.)
├── validation/       — blockchain validation
│   ├── asm/          — PVM assembly
│   ├── bloombits/    — bloom indexing
│   ├── forkid/       — fork identification
│   ├── rawdb/        — raw database operations
│   ├── state/        — state management (snapshots, pruning)
│   └── trie/         — Merkle Patricia trie
└── wallet/           — account management
    ├── external/     — external signer backend
    ├── keystore/     — encrypted key storage
    ├── scwallet/     — smart card wallets
    ├── signer/       — transaction signing rules
    └── usbwallet/    — USB hardware wallets (Ledger, Trezor)
```

## Test plan

- [x] `go build ./...` passes
- [x] `go vet ./...` passes
- [x] `golangci-lint run ./...` passes (0 issues)
- [x] `make all` passes
- [x] `make lint` passes
- [x] `go test ./kernel/...` — all kernel tests pass
- [x] `go test ./validation/...` — all validation tests pass
- [x] `go test ./primitives/...` — all primitives tests pass
- [x] `go test ./policy/...` — fee oracle tests pass
- [x] `go test ./script/...` — PVM tests pass
- [x] Kernel layer isolation verified: `go list -f '{{join .Imports "\n"}}' ./kernel/...` has no imports of rpc, p2p, node, or wallet
- [x] Full node startup and block sync
- [ ] `eth_gasPrice` and `eth_estimateSmartFee` RPCs work
- [ ] Mining produces blocks
