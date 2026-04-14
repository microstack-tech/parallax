# Parallax CLI

[Website](https://parallaxprotocol.org) | [Documentation](https://docs.parallaxprotocol.org) | [Whitepaper](https://parallaxprotocol.org/introduction/whitepaper)

Parallax CLI connects to the Parallax peer-to-peer network to download and fully validate blocks and transactions. It includes a full node, mining engine, and JSON-RPC server.

A peer-to-peer programmable cash system. See the [Whitepaper](https://parallaxprotocol.org/introduction/whitepaper) and [The Parallax Doctrine](https://github.com/ParallaxProtocol/parallax-doctrine).

## Building from Source

Parallax CLI requires **Go 1.26+** and a C compiler.

```bash
make parallax
```

This builds the `parallax` binary into `build/bin/`. To build the full suite of tools:

```bash
make all
```

For detailed build instructions including cross-compilation, Docker builds, and platform-specific notes, see `docs/`.

## Executables

| Command | Description |
|---------|-------------|
| **`parallax`** | Main node client. Runs full or archive nodes; serves JSON-RPC over HTTP, WebSocket, and IPC. |
| `clef` | Standalone transaction signer for secure account operations. |
| `devp2p` | Networking utilities to inspect and interact at the P2P layer. |
| `abigen` | Generates type-safe Go bindings from contract ABIs. |
| `pvm` | Execute and debug PVM bytecode in isolation. |
| `rlpdump` | Decode RLP-encoded data into human-readable form. |

## Running a Node

```bash
./build/bin/parallax
```

Start with an interactive JavaScript console:

```bash
./build/bin/parallax console
```

See the [getting started guide](https://docs.parallaxprotocol.org/parallax-client/getting-started/introduction) for connecting to the network, creating accounts, and sending transactions.

### Hardware Requirements

| | Minimum | Recommended |
|---|---------|-------------|
| CPU | 1 core | 2+ cores |
| RAM | 2 GB | 4+ GB |
| Storage | 50 GB SSD | 100 GB SSD |
| Network | 1 Mbps | 10+ Mbps |

## Architecture

Parallax CLI uses a layered architecture modeled after Bitcoin Core's `libbitcoin_kernel` separation:

```
kernel/         Consensus rules — no RPC, no networking, no I/O
validation/     Blockchain state, transaction pool, block validation
script/         PVM execution engine, ABI codec
primitives/     Fundamental types (blocks, transactions) and serialization (RLP)
p2p/            Peer-to-peer networking and wire protocol
node/           Node lifecycle, full node, mining
rpc/            JSON-RPC server, GraphQL, typed client
wallet/         Account management, keystore, hardware wallets
```

Each layer only imports from layers below it, enforced by Go's import rules. The `kernel/` package can be embedded independently without pulling in the full node stack.

## Development Process

The `main` branch is the development branch. Changes are submitted as pull requests and require review before merging.

### Contributing

1. Fork the repository
2. Create a feature branch
3. Submit a pull request against `main`

Format code with `gofmt`. Document public symbols. Keep commits focused and prefix messages with the affected package (e.g., `kernel/xhash: fix difficulty calculation`).

### Testing

Run the full test suite:

```bash
make test
```

Run the linter:

```bash
make lint
```

Parallax CLI is a security-critical project. Any mistake can cost users money. All changes must include appropriate test coverage and pass CI before merging.

## Resources

- [Technical Documentation](https://docs.parallaxprotocol.org)
- [Beginner Guides](https://docs.parallaxprotocol.org/guides)
- [JSON-RPC API Reference](https://docs.parallaxprotocol.org/parallax-client/interacting-with-parallax/json-rpc-server/overview)
- [Whitepaper](https://parallaxprotocol.org/introduction/whitepaper)
- [The Parallax Doctrine](https://github.com/ParallaxProtocol/parallax-doctrine)

## License

LGPL-3.0 (library) / GPL-3.0 (executables). See [COPYING](COPYING).
