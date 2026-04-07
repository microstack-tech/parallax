# Parallax Desktop (`prlx-gui`)

A desktop node manager for the Parallax network. Built on
[Wails v2](https://wails.io) — a Go backend embeds a full `prl.Parallax`
node in-process and exposes it to a React frontend rendered in the
platform's native webview. One binary, no sidecars.

## Scope

This release is intentionally focused. It does **one thing**: run a
Parallax full node from a friendly desktop UI. Wallet, mining, key
management, and transaction history are deliberately out of scope.

What ships:

- One binary that runs a Parallax full node.
- First-run wizard for picking a data directory, sync mode, and inbound
  NAT policy.
- **Client** dashboard with sync progress, peer count, uptime, disk
  usage, latest blocks (with miner / reward / size / gas-fill), and
  latest transactions.
- **Connect** page with a one-click *Add to MetaMask* button (powered by
  an embedded localhost helper that calls
  `wallet_addEthereumChain` in the user's default browser) plus manual
  fields for non-MetaMask wallets. Detects when the user is already on
  the Parallax network and reflects it in the UI.
- **Peers** page that lists every connected peer, sortable, click-to-
  expand for full enode URL, addresses, capabilities, and per-protocol
  metadata.
- **Settings** with curated and advanced sections (cache sizes, peer
  limit, HTTP-RPC toggle + port, inbound-connection toggle), a
  spring-animated save bar that warns when changes require a node
  restart, and a one-click *Restart node* action.
- **Logs** screen (linked from Settings → Diagnostics) with live tail,
  level filter, pause/resume, and copy-to-clipboard.
- TopBar live client-status indicator that polls the node every 3 s and
  flips between *Synced*, *Syncing*, and *Stopped* with a pulsing dot.
- Local-only crash reports written to the user's config directory.

## Building

The first time you build, install the Wails CLI:

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

On Linux you also need GTK3 + WebKit2GTK 4.1 + pkg-config:

```sh
# Arch
sudo pacman -S --needed gtk3 webkit2gtk-4.1 pkgconf

# Debian / Ubuntu
sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev pkg-config
```

Then from the repository root:

```sh
make prlx-gui              # build for the host platform
make prlx-gui-cross        # cross-build (best-effort, see notes below)
make prlx-gui-package      # build + zip per GUI_TARGETS into build/package/
```

Output for `make prlx-gui` is in `cmd/prlx-gui/build/bin/`. Run it with:

```sh
./cmd/prlx-gui/build/bin/prlx-gui
```

### Development

```sh
cd cmd/prlx-gui
wails dev
```

`wails dev` runs Vite in watch mode and live-reloads both the Go
bindings and the React frontend.

### Build tags

Two tags are applied to every Parallax Desktop build via the Makefile's
`WAILS_TAGS` variable (default `embedfrontend webkit2_41`):

- **`embedfrontend`** — switches `cmd/prlx-gui/embed.go` (the real
  `//go:embed all:frontend/dist`) on. Without it, the build falls back
  to the empty `embed_stub.go`. The tag is required for any production
  binary that needs to actually serve the UI; plain `go test ./...`
  from CI deliberately runs without it so the package compiles cleanly
  on a fresh checkout where `frontend/dist/` hasn't been populated yet.
- **`webkit2_41`** — selects the modern WebKit2GTK ABI on Linux. Wails
  2.x defaults to 4.0 but Arch and recent Debian/Ubuntu only ship 4.1.
  Harmless on macOS / Windows.

Override with `make prlx-gui WAILS_TAGS="…"` if you need different tags.

### Cross-compilation notes

Wails GUI cross-compilation is fragile by design — building `darwin/*`
from a non-mac host requires `osxcross`, `windows/*` from a non-windows
host requires `mingw-w64`, etc. `make prlx-gui-package` runs `wails build`
once per `GUI_TARGETS` entry and continues past failures, so you'll get
zips for whatever your local toolchain can produce.

For full multi-platform releases, see the **Releases** section below — the
release workflow uses one native runner per OS, sidestepping the
cross-compile problem entirely.

## Releases

Tagged releases are produced by `.github/workflows/release.yml`. On
`git push` of a `v*` tag, the workflow:

1. Cross-compiles all CLI binaries (`prlx`, `clef`, `parallaxkey`) for
   every entry in the Makefile's `TARGETS`, using the same `make package`
   path local releases use.
2. Builds the GUI on a native runner per OS:
   - `linux/amd64` and `linux/arm64` on Ubuntu (apt installs the GTK +
     WebKit deps).
   - `darwin/amd64` on `macos-13` (last x86 macOS runner).
   - `darwin/arm64` on `macos-latest` (Apple Silicon).
   - `windows/amd64` on `windows-latest`.
3. Combines every artifact into a single `release/` directory, generates
   a unified `SHA256SUMS.txt`, and creates a **draft** GitHub Release.
   A maintainer reviews the artifacts, edits the auto-generated notes,
   and clicks Publish when ready.

You can also dry-run the workflow against `main` via the Actions tab —
the build jobs run but the release-publishing job is gated on a tag push.

## Architecture

```
cmd/prlx-gui/
  main.go            Wails bootstrap, window options, asset embedding.
  app.go             App struct bound to the frontend (every exported
                     method becomes a JS-callable function).
  embed.go           Real //go:embed of frontend/dist, gated on the
                     `embedfrontend` build tag.
  embed_stub.go      Empty embed.FS fallback for non-tagged builds
                     (used by `go test ./...` from CI).
  backend/
    config.go         GUIConfig persistence (~/.config/Parallax/gui.json).
    node.go           NodeController: in-process node.Node + prl.Parallax
                      lifecycle, status snapshot, bootnode wiring, DNS
                      discovery defaults, RPC endpoint formatting,
                      Peers() and RecentBlocks/Transactions() walkers.
    logs.go           LogTail: GlogHandler tee'd to stderr + ring buffer
                      + live frontend emitter. Verbosity defaults to Info.
    metamask.go       Tiny localhost helper HTTP server that serves a
                      Parallax-themed page calling wallet_addEthereumChain
                      in the user's default browser.
    crash.go          Crash report writer; installs a recover() hook.
    types.go          Wails-serialisable structs shared with the frontend.
  frontend/          React + Vite + TypeScript + Tailwind + motion.
    src/
      App.tsx           Top bar nav, route shell, AnimatePresence
                        between pages, splash screen.
      lib/api.ts        Typed wrappers around the bound App methods.
      lib/format.ts     wei↔LAX, byte / duration / hash / ago helpers.
      components/       SectionHeading, AnimatedNumber, StatusPill,
                        ClientStatus, PageStagger, Toggle.
      pages/
        Dashboard.tsx   Client overview, status, latest blocks/txs,
                        connect-wallet hint row.
        Connect.tsx     One-click MetaMask flow + manual fallback.
        Peers.tsx       Sortable peers list, expand-for-details rows.
        Settings/       Curated + advanced settings, floating save bar,
                        About card with prlx + desktop versions.
        Logs.tsx        Live log tail, filter, pause/resume.
        Onboarding/     5-step first-run wizard.
      assets/           Logo (white version, from parallax-website).
```

## Security defaults

- **HTTP-RPC** is on by default and bound to `127.0.0.1` only. There is
  no UI option to expose it to `0.0.0.0`. Users can disable the
  endpoint entirely from Settings → Local apps.
- **Inbound NAT** is on by default — Wails opens a UPnP/PMP mapping so
  other peers can dial in, helping the network. Users behind strict
  firewalls or who prefer outbound-only can flip the toggle in
  Settings → Networking (or during first-run onboarding).
- **Crash reports** are written to `<userConfigDir>/Parallax/crash-*.log`
  and never sent off the machine.

## Connecting MetaMask

The Connect page leads with a single **Add Parallax to MetaMask** button.
Clicking it opens a localhost helper page in the user's default browser
which fires `wallet_addEthereumChain` (or `wallet_switchEthereumChain` if
the network is already added). The helper auto-detects whether MetaMask
is already on Parallax and shows a connected confirmation instead of the
add button.

For non-MetaMask wallets, the same Connect page also shows the manual
network values:

| Field           | Value                          |
| --------------- | ------------------------------ |
| Network name    | Parallax                       |
| RPC URL         | `http://127.0.0.1:8545`        |
| Chain ID        | `2110`                         |
| Currency symbol | `LAX`                          |

Any EVM-compatible wallet that supports custom networks can use these
values directly.
