// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

//go:build embedfrontend

package main

import "embed"

// The real embed: only compiled when the `embedfrontend` build tag is set.
// `wails build` (driven by the Makefile and the release workflow) always
// passes this tag, so production binaries embed the Vite output.
//
// Plain `go test ./...` from CI does NOT set the tag, so it uses
// embed_stub.go instead — that file is the empty FS fallback, and is the
// reason CI doesn't fail when frontend/dist is missing or empty.
//
//go:embed all:frontend/dist
var assets embed.FS
