# Parallax Core

Reference Golang implementation of the **Parallax protocol**.

> **Parallax** is a Proof-of-Work timechain protocol designed to merge the security model of Bitcoin with the programmability of the EVM. It combines Bitcoin’s fixed monetary rules with Ethereum’s virtual machine to deliver a scarce, decentralized, and programmable timechain.

---

## More on Parallax

- Website: [https://parallaxprotocol.org](https://parallaxprotocol.org)
- The Parallax Doctrine: [https://github.com/ParallaxProtocol/parallax-doctrine](https://github.com/ParallaxProtocol/parallax-doctrine)
- Beginner Guides: [https://docs.parallaxprotocol.org/guides](https://docs.parallaxprotocol.org/guides)
- Technical Documentation: [https://docs.parallaxprotocol.org](https://docs.parallaxprotocol.org)
- Whitepaper: [https://parallaxprotocol.org/introduction/whitepaper](https://parallaxprotocol.org/introduction/whitepaper)

---

## Building from Source

Parallax requires **Go 1.25+** and a C compiler.

```bash
make prlx
```

Build the full suite:

```bash
make all
```

---

## Executables

Binaries are located under `build/bin`:

| Command        | Description |
|----------------|-------------|
| **`prlx`** | Main CLI client. Runs full, archive, or light nodes; exposes JSON-RPC over HTTP, WS, and IPC. |
| **`prlx-gui`** | Desktop application that embeds a full Parallax node and exposes it through a friendly UI. See [Parallax Desktop](#parallax-desktop) below. |
| `clef`         | Stand-alone signer for secure account operations. |
| `devp2p`       | Networking utilities to inspect and interact at the P2P layer. |
| `abigen`       | Generates type-safe Go bindings from contract ABIs. |
| `bootnode`     | Lightweight discovery node to bootstrap networks. |
| `pvm`          | Execute and debug PVM bytecode snippets in isolation. |
| `rlpdump`      | Decode RLP structures into a human-readable form. |

---

## Running a Node

Interactive console:

```bash
./prlx console
```

### Hardware Recommendations

- **Minimum**: 2 cores, 4 GB RAM, 250 GB SSD, 8 Mbps
- **Recommended**: 4+ cores, 8 GB RAM, 500 TB SSD, 25+ Mbps

---

## Parallax Desktop

`prlx-gui` is a cross-platform desktop application that runs a Parallax full
node from a friendly UI. The same `prl.Parallax` service that powers the CLI
is embedded in-process — there's no separate sidecar — so a single binary
is everything an end user needs to join the network.

**Highlights**

- One-click node lifecycle, sync progress, peer count, disk usage, and
  uptime on a live dashboard.
- Live tail of recent blocks and transactions.
- Drill-down peers screen with inbound/outbound classification, capabilities,
  and per-protocol metadata.
- HTTP-RPC enabled by default on `127.0.0.1` so MetaMask and any other
  EVM-compatible wallet can connect to the user's own node — including a
  one-click "Add to MetaMask" flow served by an embedded helper page.
- Live log tail with verbosity control and a Pause/Resume button, accessible
  from the Settings page.
- First-run wizard for picking a data directory, sync mode, inbound NAT
  policy, and confirming the local-RPC opt-in.

**Building locally**

The GUI is built with [Wails v2](https://wails.io). One-time prerequisites:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Linux only
sudo pacman -S --needed gtk3 webkit2gtk-4.1 pkgconf       # Arch
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev pkg-config  # Debian/Ubuntu
```

Then from the repository root:

```bash
make prlx-gui                  # build for the host platform
./cmd/prlx-gui/build/bin/prlx-gui
```

**Pre-built binaries**

Tagged releases on GitHub include zipped GUI builds for `linux/amd64`,
`linux/arm64`, `darwin/amd64`, `darwin/arm64`, and `windows/amd64`,
produced natively per platform by the release workflow in
`.github/workflows/release.yml`.

For the full architecture, security model, and the list of fields the
desktop app exposes, see [`cmd/prlx-gui/README.md`](cmd/prlx-gui/README.md).

---

## Contribution

We welcome contributions aligned with **neutrality, openness, and decentralization**.

1. Fork the repo
2. Implement your changes
3. Open a PR against `main`

**Guidelines**

- Format with `gofmt`; document public symbols following Go conventions.  
- Keep commits focused; prefix messages with affected packages (e.g., `prlx, rpc:`).  

---

## License

- **Library code** (`/` excluding `cmd/`): [LGPL v3](https://www.gnu.org/licenses/lgpl-3.0.en.html)  
- **Executables** (`/cmd/*`): [GPL v3](https://www.gnu.org/licenses/gpl-3.0.en.html)

### Attribution Notice

Parallax Core is a derivative work of [go-ethereum](https://github.com/ethereum/go-ethereum) originally developed by the go-ethereum authors and licensed under the GNU LGPL-3.0.
