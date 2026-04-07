// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.
//
// prlx-gui is a desktop application that embeds a Parallax full node and
// exposes a friendly user interface for running the node, managing a wallet,
// sending transactions and (optionally) mining.
package main

import (
	_ "embed"
	"log"

	// Side-effect imports — register the JS and native tracer factories
	// so debug_traceTransaction / debug_traceCall and friends actually
	// have tracers available over RPC. cmd/prlx does the same thing in
	// cmd/prlx/main.go; without these the tracer namespace registers but
	// every call returns "tracer not found".
	_ "github.com/ParallaxProtocol/parallax/prl/tracers/js"
	_ "github.com/ParallaxProtocol/parallax/prl/tracers/native"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// appIcon is the master PNG used as the window/taskbar icon on Linux at
// runtime. On Windows the icon is supplied by build/windows/icon.ico (which
// wails embeds into the .exe via rsrc) and on macOS wails auto-generates
// build/darwin/icons.icns from this same file during `wails build`, so this
// embed only matters for the Linux side of the build.
//
//go:embed build/appicon.png
var appIcon []byte

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Parallax Desktop",
		Width:     1280,
		Height:    820,
		MinWidth:  1024,
		MinHeight: 700,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 16, G: 14, B: 24, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
		Linux: &linux.Options{
			Icon:                appIcon,
			WindowIsTranslucent: false,
			ProgramName:         "Parallax Desktop",
		},
	})
	if err != nil {
		log.Fatalf("prlx-gui: %v", err)
	}
}
