// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

//go:build !embedfrontend

package main

import "embed"

// Empty FS fallback used when the `embedfrontend` build tag is NOT set.
// This exists so that plain `go test ./...` and `go vet ./...` runs from
// CI compile cleanly even when frontend/dist is missing — at runtime the
// app would fail to serve any UI, but production builds always go through
// `wails build -tags embedfrontend ...` which uses the real embed in
// embed.go instead.
var assets embed.FS
