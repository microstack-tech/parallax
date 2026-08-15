// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

// Package fuzzutil provides shared helpers for the native fuzz wrappers
// around the legacy go-fuzz targets in tests/fuzzers.
package fuzzutil

import (
	"os"
	"path/filepath"
	"testing"
)

// SeedFromDir adds every regular file in dir as a raw byte seed for f. The
// directory layout is the legacy go-fuzz corpus format: one input per file.
// A missing directory is not an error, so targets without a checked-in
// corpus can call this unconditionally.
func SeedFromDir(f *testing.F, dir string) {
	f.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		f.Fatalf("reading corpus dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			f.Fatalf("reading corpus file %s: %v", entry.Name(), err)
		}
		f.Add(data)
	}
}
