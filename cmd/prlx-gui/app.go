// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package main

import (
	"context"
	"fmt"

	"github.com/ParallaxProtocol/parallax/cmd/prlx-gui/backend"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the root struct bound to the Wails frontend. Every exported method
// becomes a JS-callable function under window.go.main.App.*
type App struct {
	ctx context.Context

	cfg      *backend.ConfigStore
	logs     *backend.LogTail
	node     *backend.NodeController
	metamask *backend.MetaMaskHelper
	miner    *backend.MinerController
}

// NewApp constructs the App. The heavy lifting (loading config, attaching the
// log handler) happens in startup so that early errors can be surfaced to the
// frontend instead of crashing the binary.
func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Logs first so that any subsequent setup gets captured.
	a.logs = backend.NewLogTail(2048)
	a.logs.Install()
	a.logs.SetEmitter(func(line backend.LogLine) {
		wruntime.EventsEmit(ctx, "log", line)
	})

	backend.InstallCrashHandler(func(reportPath string) {
		wruntime.EventsEmit(ctx, "crash", reportPath)
	})

	cfg, err := backend.LoadConfigStore("")
	if err != nil {
		wruntime.LogErrorf(ctx, "load config: %v", err)
	}
	a.cfg = cfg

	a.node = backend.NewNodeController(cfg)
	a.node.SetEmitter(func(evt backend.NodeEvent) {
		wruntime.EventsEmit(ctx, "node", evt)
	})

	// Tiny localhost helper that serves a one-click "Add to MetaMask"
	// page. Lives for the lifetime of the GUI process so the URL is
	// stable across navigations.
	a.metamask = backend.NewMetaMaskHelper(cfg)
	if err := a.metamask.Start(); err != nil {
		wruntime.LogErrorf(ctx, "metamask helper: %v", err)
	}

	a.miner = backend.NewMinerController(cfg, a.node)
	a.miner.SetEmitter(func(evt backend.MinerEvent) {
		wruntime.EventsEmit(ctx, "miner", evt)
	})
}

func (a *App) shutdown(ctx context.Context) {
	if a.miner != nil {
		_ = a.miner.Stop()
	}
	if a.metamask != nil {
		_ = a.metamask.Stop()
	}
	if a.node != nil {
		_ = a.node.Stop()
	}
	if a.logs != nil {
		a.logs.Close()
	}
}

// ---------------------------------------------------------------------------
// Bootstrap / config
// ---------------------------------------------------------------------------

// BootstrapNeeded reports whether the onboarding wizard should run.
func (a *App) BootstrapNeeded() bool {
	return a.cfg == nil || !a.cfg.Get().Bootstrapped
}

// SaveBootstrap finalises the onboarding wizard and persists initial config.
func (a *App) SaveBootstrap(cfg backend.GUIConfig) error {
	cfg.Bootstrapped = true
	return a.cfg.Save(cfg)
}

func (a *App) GetConfig() backend.GUIConfig           { return a.cfg.Get() }
func (a *App) UpdateConfig(c backend.GUIConfig) error { return a.cfg.Save(c) }

// ---------------------------------------------------------------------------
// Node lifecycle
// ---------------------------------------------------------------------------

func (a *App) StartNode() error               { return a.node.Start(a.ctx) }
func (a *App) StopNode() error                { return a.node.Stop() }
func (a *App) NodeStatus() backend.NodeStatus { return a.node.Status() }
func (a *App) Peers() []backend.PeerView      { return a.node.Peers() }
func (a *App) RecentBlocks(n int) []backend.BlockView {
	return a.node.RecentBlocks(n)
}
func (a *App) RecentTransactions(n int) []backend.TxView {
	return a.node.RecentTransactions(n)
}

// ---------------------------------------------------------------------------
// MetaMask helper
// ---------------------------------------------------------------------------

// MetaMaskHelperURL returns the loopback URL of the embedded "Add to
// MetaMask" landing page. The frontend opens this in the user's default
// browser via window.runtime.BrowserOpenURL.
func (a *App) MetaMaskHelperURL() string {
	if a.metamask == nil {
		return ""
	}
	return a.metamask.URL()
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

func (a *App) GetLogTail(n int) []backend.LogLine { return a.logs.Tail(n) }

// SetLogVerbosity changes the active log level (0 = silent, 3 = info,
// 5 = trace).
func (a *App) SetLogVerbosity(level int) { a.logs.SetVerbosity(level) }

// ---------------------------------------------------------------------------
// Mining
// ---------------------------------------------------------------------------

func (a *App) StartMining(mode string) error              { return a.miner.Start(a.ctx, mode) }
func (a *App) StopMining() error                          { return a.miner.Stop() }
func (a *App) MinerStatus() backend.MinerStatus           { return a.miner.Status() }
func (a *App) DetectGPUs() ([]backend.DeviceInfo, error)  { return a.miner.DetectGPUs() }
func (a *App) DefaultPools() []backend.PoolInfo           { return a.miner.DefaultPools() }
func (a *App) HashwarpInstalled() bool { return a.miner.HashwarpInstalled() }
func (a *App) InstallHashwarp(gpuType string) error {
	return a.miner.InstallHashwarp(gpuType, func(step, detail string) {
		wruntime.EventsEmit(a.ctx, "hashwarp-install", map[string]string{
			"step":   step,
			"detail": detail,
		})
	})
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

func (a *App) Version() string {
	return fmt.Sprintf("Parallax Desktop %s", backend.GUIVersion)
}

// ClientVersion returns the embedded prlx client version string.
func (a *App) ClientVersion() string {
	return a.node.ClientVersion()
}
