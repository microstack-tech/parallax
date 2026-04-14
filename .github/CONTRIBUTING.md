# Contributing

Thank you for considering contributing to Parallax. We welcome contributions from anyone and are grateful for even the smallest of fixes.

## Getting started

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes
4. Ensure `make all`, `make test`, and `make lint` pass
5. Submit a pull request against `main`

For larger changes, please open an issue first to discuss the approach. This helps ensure alignment with the project's direction and avoids wasted effort.

## Coding guidelines

- Code must be formatted with `gofmt`.
- Public symbols must be documented following Go [commentary](https://golang.org/doc/effective_go.html#commentary) guidelines.
- Pull requests must be based on and opened against `main`.
- Commit messages should be prefixed with the package(s) they modify:
  - `kernel/xhash: fix ASERT difficulty calculation`
  - `validation, node/miner: handle empty block edge case`
  - `rpc: add batch request timeout`

## Architecture

Parallax uses a layered architecture. Each layer only imports from layers below it:

```
kernel/         Consensus rules (no RPC, no networking)
validation/     Blockchain state, tx pool, block validation
script/         PVM execution, ABI codec
primitives/     Types (blocks, txs) and serialization (RLP)
p2p/            Peer-to-peer networking
node/           Node lifecycle, full node, mining
rpc/            JSON-RPC server, GraphQL
wallet/         Account management, keystore
```

When contributing, respect these boundaries. If your change requires a lower layer to import from a higher one, the design likely needs rethinking.

## Testing

All changes must include appropriate test coverage. This is a security-critical project — any mistake can cost users money.

```bash
make test       # full test suite
make lint       # linter
go test ./...   # all tests
```

## License

By contributing, you agree that your contributions will be licensed under the project's existing licenses (LGPL-3.0 for library code, GPL-3.0 for executables).
