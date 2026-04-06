// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

import (
	"context"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"sync"
	"time"
)

// MetaMaskHelper runs a tiny single-page web server on a loopback address.
// The page it serves is a Parallax-themed "Add to MetaMask" landing page
// that fires `wallet_addEthereumChain` against the user's local node.
//
// We can't drive MetaMask from inside the Wails webview because the
// extension only exposes window.ethereum on pages it has injected. By
// asking the OS to open this URL in the user's default browser
// (window.runtime.BrowserOpenURL on the frontend) we land on a page where
// MetaMask *is* available, and a single click adds the network.
type MetaMaskHelper struct {
	cfg *ConfigStore

	mu       sync.RWMutex
	listener net.Listener
	server   *http.Server
	url      string
	tmpl     *template.Template
}

// NewMetaMaskHelper constructs a helper bound to the GUI config store. The
// server is not started until Start() is called.
func NewMetaMaskHelper(cfg *ConfigStore) *MetaMaskHelper {
	return &MetaMaskHelper{
		cfg:  cfg,
		tmpl: template.Must(template.New("metamask").Parse(metaMaskHTML)),
	}
}

// Start brings up the helper on a random loopback port. Idempotent.
func (m *MetaMaskHelper) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener != nil {
		return nil
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("metamask helper: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", m.handle)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = server.Serve(l) }()
	m.listener = l
	m.server = server
	m.url = fmt.Sprintf("http://%s/", l.Addr().String())
	return nil
}

// Stop tears down the helper. Idempotent.
func (m *MetaMaskHelper) Stop() error {
	m.mu.Lock()
	server := m.server
	m.server = nil
	m.listener = nil
	m.url = ""
	m.mu.Unlock()
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

// URL returns the address the helper is listening on, or "" if it's not
// running. Use BrowserOpenURL on the frontend to open it.
func (m *MetaMaskHelper) URL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.url
}

func (m *MetaMaskHelper) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	cfg := m.cfg.Get()
	rpcURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.HTTPRPCPort)
	data := struct {
		RPCURL     string
		ChainID    int
		ChainIDHex string
	}{
		RPCURL:     rpcURL,
		ChainID:    2110,
		ChainIDHex: fmt.Sprintf("0x%x", 2110),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = m.tmpl.Execute(w, data)
}

// metaMaskHTML is the single page the helper serves. Inlined so we don't
// need go:embed for one file. Mirrors the marketing-site aesthetic so the
// hand-off feels native: black bg, gold accent, Newsreader serif heading.
const metaMaskHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Add Parallax to MetaMask</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Geist:wght@400;500&family=Geist+Mono:wght@400&family=Newsreader:opsz,wght@6..72,400;6..72,500&display=swap">
<style>
  :root {
    --bg: oklch(0.06 0.015 265);
    --bg-elev: oklch(0.12 0.012 265);
    --bg-elev-2: oklch(0.14 0.015 265);
    --fg: oklch(0.93 0.005 80);
    --muted: oklch(0.6 0.01 265);
    --border: oklch(1 0 0 / 0.08);
    --gold: #f7931a;
    --success: oklch(0.696 0.17 162.48);
    --danger: oklch(0.704 0.191 22.216);
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; min-height: 100vh; }
  body {
    background: var(--bg);
    color: var(--fg);
    font-family: "Geist", ui-sans-serif, system-ui, sans-serif;
    display: grid;
    place-items: center;
    padding: 2rem;
    -webkit-font-smoothing: antialiased;
  }
  .card {
    max-width: 520px;
    width: 100%;
    border: 1px solid var(--border);
    border-left: 3px solid var(--gold);
    background: var(--bg-elev);
    border-radius: 0.625rem;
    padding: 3rem 2.5rem;
    text-align: center;
  }
  .accent {
    display: block;
    width: 40px;
    height: 2px;
    background: var(--gold);
    margin: 0 auto 1.5rem;
    border-radius: 2px;
  }
  .eyebrow {
    font-size: 11px;
    letter-spacing: 0.18em;
    text-transform: uppercase;
    color: var(--muted);
    font-weight: 500;
  }
  h1 {
    font-family: "Newsreader", ui-serif, Georgia, serif;
    font-size: 2.25rem;
    font-weight: 400;
    letter-spacing: -0.02em;
    line-height: 1.05;
    margin: 0.875rem 0 1rem;
  }
  p {
    color: var(--muted);
    line-height: 1.65;
    margin: 0 auto 2rem;
    max-width: 380px;
    font-size: 14px;
  }
  button {
    background: var(--gold);
    color: oklch(0.15 0.03 60);
    border: none;
    padding: 0.95rem 2rem;
    border-radius: 0.625rem;
    font-family: inherit;
    font-size: 14px;
    font-weight: 500;
    letter-spacing: 0.01em;
    cursor: pointer;
    transition: filter 0.2s, transform 0.15s;
    box-shadow: 0 0 60px -10px rgb(247 147 26 / 0.35);
  }
  button:hover { filter: brightness(1.1); }
  button:active { transform: scale(0.98); }
  button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
    box-shadow: none;
  }
  .status {
    margin-top: 1.5rem;
    font-size: 13px;
    min-height: 20px;
    line-height: 1.4;
  }
  .status.success { color: var(--success); }
  .status.error { color: var(--danger); }
  .details {
    margin: 2rem 0 0;
    padding-top: 2rem;
    border-top: 1px solid var(--border);
    text-align: left;
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 1.25rem 2rem;
  }
  .details > div { min-width: 0; }
  .details dt {
    font-size: 11px;
    letter-spacing: 0.18em;
    text-transform: uppercase;
    color: var(--muted);
    font-weight: 500;
  }
  .details dd {
    font-family: "Geist Mono", ui-monospace, monospace;
    color: var(--fg);
    margin: 0.4rem 0 0;
    font-size: 12px;
    word-break: break-all;
  }
  .footer {
    margin-top: 1.5rem;
    font-size: 11px;
    color: var(--muted);
  }
</style>
</head>
<body>
<main class="card">
  <span class="accent" aria-hidden="true"></span>
  <div class="eyebrow">Parallax Network</div>
  <h1 id="heading">Add to MetaMask.</h1>
  <p id="lede">One click adds the Parallax mainnet to your MetaMask wallet, pointed at your local node.</p>
  <button id="add-btn" type="button">Add Parallax to MetaMask</button>
  <div class="status" id="status" role="status" aria-live="polite"></div>
  <dl class="details">
    <div>
      <dt>RPC URL</dt>
      <dd>{{.RPCURL}}</dd>
    </div>
    <div>
      <dt>Chain ID</dt>
      <dd>{{.ChainID}}</dd>
    </div>
    <div>
      <dt>Currency</dt>
      <dd>LAX</dd>
    </div>
    <div>
      <dt>Network</dt>
      <dd>Parallax</dd>
    </div>
  </dl>
  <div class="footer">You can close this tab once the network has been added.</div>
</main>
<script>
  const params = {
    chainId: "{{.ChainIDHex}}",
    chainName: "Parallax",
    nativeCurrency: { name: "Lax", symbol: "LAX", decimals: 18 },
    rpcUrls: ["{{.RPCURL}}"],
    blockExplorerUrls: ["https://explorer.parallaxprotocol.org"],
  };
  const heading = document.getElementById("heading");
  const lede = document.getElementById("lede");
  const btn = document.getElementById("add-btn");
  const status = document.getElementById("status");

  function setStatus(text, kind) {
    status.textContent = text;
    status.className = "status" + (kind ? " " + kind : "");
  }

  // Three UI states: connected (already on Parallax in MetaMask), present
  // (not currently selected — we don't actually know if the chain is added
  // until the user clicks), and missing (no MetaMask at all). We can only
  // distinguish "added but not selected" from "not added" by attempting
  // wallet_switchEthereumChain and watching for error 4902.
  function paintConnected() {
    heading.textContent = "Already connected.";
    lede.textContent = "Your MetaMask is already on the Parallax network and pointed at your local node.";
    btn.style.display = "none";
    setStatus("✓ Connected to Parallax. You can close this tab.", "success");
  }

  function paintNotConnected() {
    heading.textContent = "Add to MetaMask.";
    lede.textContent = "One click adds the Parallax mainnet to your MetaMask wallet, pointed at your local node.";
    btn.style.display = "";
    btn.disabled = false;
    btn.textContent = "Add Parallax to MetaMask";
    setStatus("");
  }

  async function checkCurrentChain() {
    if (typeof window.ethereum === "undefined") {
      btn.disabled = true;
      setStatus(
        "MetaMask is not installed in this browser. Install MetaMask, then refresh this page.",
        "error",
      );
      return;
    }
    try {
      const chainId = await window.ethereum.request({ method: "eth_chainId" });
      if (chainId && chainId.toLowerCase() === params.chainId.toLowerCase()) {
        paintConnected();
      } else {
        paintNotConnected();
      }
    } catch {
      paintNotConnected();
    }
  }

  // React to the user switching networks in MetaMask while this tab is open.
  if (typeof window.ethereum !== "undefined" && window.ethereum.on) {
    window.ethereum.on("chainChanged", (chainId) => {
      if (chainId && chainId.toLowerCase() === params.chainId.toLowerCase()) {
        paintConnected();
      } else {
        paintNotConnected();
      }
    });
  }

  checkCurrentChain();

  btn.addEventListener("click", async () => {
    if (typeof window.ethereum === "undefined") {
      setStatus(
        "MetaMask is not installed in this browser. Install MetaMask, then refresh this page.",
        "error",
      );
      return;
    }
    btn.disabled = true;
    setStatus("Waiting for approval in MetaMask…");
    try {
      // Try to switch first — this is a no-op if the chain is already
      // selected, and surfaces error 4902 ("chain not added") which we
      // use as the trigger to actually add the network.
      await window.ethereum.request({
        method: "wallet_switchEthereumChain",
        params: [{ chainId: params.chainId }],
      });
      // chainChanged listener will paint the success state.
      paintConnected();
      return;
    } catch (err) {
      const code = err && err.code;
      if (code !== 4902) {
        if (code === 4001) {
          setStatus("You rejected the request.", "error");
        } else {
          setStatus(
            (err && err.message) || "Failed to switch network.",
            "error",
          );
        }
        btn.disabled = false;
        return;
      }
    }
    // Fall through: chain isn't added yet, so add it.
    try {
      await window.ethereum.request({
        method: "wallet_addEthereumChain",
        params: [params],
      });
      paintConnected();
    } catch (err) {
      const code = err && err.code;
      if (code === 4001) {
        setStatus("You rejected the request.", "error");
      } else {
        setStatus(
          (err && err.message) || "Failed to add network.",
          "error",
        );
      }
      btn.disabled = false;
    }
  });
</script>
</body>
</html>
`
