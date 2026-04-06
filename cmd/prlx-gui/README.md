# Parallax Desktop (`prlx-gui`)

A desktop node manager for the Parallax network. Built on
[Wails v2](https://wails.io) — a Go backend embeds a full `prl.Parallax`
node in-process and exposes it to a React frontend rendered in the
platform's native webview.

## Scope (MVP)

This release is intentionally focused. It does **one thing**: run a
Parallax full node from a friendly desktop UI.

- One binary that runs a Parallax full node.
- First-run wizard for picking a data directory and sync mode.
- Live dashboard with sync progress, peer count, uptime, disk usage.
- HTTP-RPC enabled by default (bound to `127.0.0.1`) so users can
  connect MetaMask or any other EVM-compatible wallet to their own
  node — the dashboard surfaces the RPC URL, chain ID and currency
  symbol with copy-to-clipboard.
- Live log tail with verbosity control (default level 3 / Info).
- Settings for cache sizes, peer limit, and toggling HTTP-RPC.
- Local-only crash reports.

Wallet, mining, transaction history, and key management are deliberately
out of scope for this version.

## Building

The first time you build, install the Wails CLI and add the runtime
dependency to the Go module:

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@latest
go get github.com/wailsapp/wails/v2
```

On Linux you also need WebKit2GTK and GTK3:

```sh
# Arch
sudo pacman -S --needed gtk3 webkit2gtk-4.1 pkgconf
```

Then from the repository root:

```sh
make prlx-gui              # build for the host platform
make prlx-gui-cross        # cross-build linux/darwin/windows on amd64+arm64
```

Output binaries are written to `cmd/prlx-gui/build/bin/`.

### Development

```sh
cd cmd/prlx-gui
wails dev
```

## Architecture

```
cmd/prlx-gui/
  main.go           Wails bootstrap, window options, asset embedding.
  app.go            App struct bound to the frontend (every exported
                    method becomes a JS-callable function).
  embed.go          //go:embed all:frontend/dist
  backend/
    config.go         GUIConfig persistence (~/.config/Parallax/gui.json)
    node.go           NodeController: in-process node.Node + prl.Parallax
                      lifecycle, status snapshot, bootnode wiring,
                      DNS-discovery defaults, RPC endpoint formatting.
    logs.go           LogTail: GlogHandler tee'd to stderr + ring buffer +
                      live frontend emitter. Verbosity defaults to Info.
    crash.go          Crash report writer; installs a recover() hook.
    types.go          Wails-serialisable structs shared with the frontend.
  frontend/         React + Vite + TypeScript + Tailwind frontend.
    src/
      lib/api.ts        Typed wrappers around the bound App methods.
      lib/format.ts     Byte / duration formatters.
      pages/            Dashboard, Settings, Logs, Onboarding.
      assets/           Logo (copied from ../parallax-website/public/).
```

## Security defaults

- HTTP-RPC is **on** by default but bound to `127.0.0.1` only — there is
  no UI option to expose it to `0.0.0.0`. Users can disable it from
  Settings.
- Crash reports are written locally to the user's config directory and
  never sent off the machine.

## Connecting MetaMask

Once the node is running, the dashboard shows everything you need to add
the Parallax network to MetaMask:

| Field           | Value                          |
| --------------- | ------------------------------ |
| Network name    | Parallax               |
| RPC URL         | `http://127.0.0.1:8545`        |
| Chain ID        | `2110`                         |
| Currency symbol | `PRLX`                         |

The same values work for any other EVM-compatible wallet that supports
custom networks.
