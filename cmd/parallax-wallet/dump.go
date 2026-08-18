// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of parallax.
//
// parallax is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// parallax is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with parallax. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/cmd/utils"
	"gopkg.in/urfave/cli.v1"
)

// dumpFormatVersion is bumped if dumpFile's on-disk shape ever changes.
const dumpFormatVersion = 1

// dumpFile is the serialized form of a keystore backup. Each entry carries
// the original filename plus the raw encrypted JSON blob as produced by
// keystore.EncryptKey — the private key material never leaves its
// encrypted form. This keeps the backup a single file while preserving
// every bit the keystore wrote.
type dumpFile struct {
	Version    int         `json:"version"`
	CreatedAt  time.Time   `json:"created_at"`
	SourcePath string      `json:"source_path"`
	Entries    []dumpEntry `json:"entries"`
}

type dumpEntry struct {
	Filename string          `json:"filename"`
	Keyjson  json.RawMessage `json:"keyjson"`
}

var outfileFlag = cli.StringFlag{
	Name:  "outfile",
	Usage: "path to write the dump file (default: stdout)",
}

var infileFlag = cli.StringFlag{
	Name:  "infile",
	Usage: "path to read the dump file from (default: stdin)",
}

var overwriteFlag = cli.BoolFlag{
	Name:  "overwrite",
	Usage: "overwrite existing files in the destination keystore",
}

var commandDump = cli.Command{
	Name:      "dump",
	Usage:     "dump the keystore to a structured backup file",
	ArgsUsage: " ",
	Flags: []cli.Flag{
		dataDirFlag,
		keyStoreDirFlag,
		outfileFlag,
	},
	Description: `
Serialise every keyfile in the keystore into a single JSON backup file.
Keys remain encrypted inside the dump — passphrases are never collected
and never present in the output.

The dump is written to --outfile, or stdout if that flag is omitted.
Restore a dump with "createfromdump".`,
	Action: runDump,
}

var commandCreateFromDump = cli.Command{
	Name:      "createfromdump",
	Usage:     "recreate a keystore from a dump file",
	ArgsUsage: " ",
	Flags: []cli.Flag{
		dataDirFlag,
		keyStoreDirFlag,
		infileFlag,
		overwriteFlag,
	},
	Description: `
Rebuild a keystore directory from a dump file produced by "dump". Each
entry is written as an individual keyfile in the destination keystore.

By default, existing files with the same name are left untouched. Pass
--overwrite to replace them.`,
	Action: runCreateFromDump,
}

func runDump(ctx *cli.Context) error {
	dir := resolveKeystoreDir(ctx)

	entries, err := os.ReadDir(dir)
	if err != nil {
		utils.Fatalf("Could not read keystore %s: %v", dir, err)
	}

	out := dumpFile{
		Version:    dumpFormatVersion,
		CreatedAt:  time.Now().UTC(),
		SourcePath: dir,
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip any non-keyfile housekeeping entries (lockfiles,
		// editor swap files). Geth keystore files start with "UTC--"
		// or are arbitrary names the user imported; we accept any
		// file that parses as JSON.
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			utils.Fatalf("Could not read keystore entry %s: %v", name, err)
		}
		if !json.Valid(data) {
			continue
		}
		out.Entries = append(out.Entries, dumpEntry{
			Filename: name,
			Keyjson:  json.RawMessage(data),
		})
	}

	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		utils.Fatalf("Failed to marshal dump: %v", err)
	}

	if target := ctx.String(outfileFlag.Name); target != "" {
		if err := os.WriteFile(target, buf, 0o600); err != nil {
			utils.Fatalf("Failed to write dump file: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %d account(s) to %s\n", len(out.Entries), target)
		return nil
	}
	if _, err := os.Stdout.Write(buf); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func runCreateFromDump(ctx *cli.Context) error {
	dest := resolveKeystoreDir(ctx)

	var raw []byte
	var err error
	if in := ctx.String(infileFlag.Name); in != "" {
		raw, err = os.ReadFile(in)
		if err != nil {
			utils.Fatalf("Could not read dump file %s: %v", in, err)
		}
	} else {
		raw, err = readAllStdin()
		if err != nil {
			utils.Fatalf("Could not read dump from stdin: %v", err)
		}
	}

	var dump dumpFile
	if err := json.Unmarshal(raw, &dump); err != nil {
		utils.Fatalf("Invalid dump file: %v", err)
	}
	if dump.Version != dumpFormatVersion {
		utils.Fatalf("Unsupported dump version %d (this binary understands %d)", dump.Version, dumpFormatVersion)
	}

	overwrite := ctx.Bool(overwriteFlag.Name)
	written, skipped := 0, 0
	for _, e := range dump.Entries {
		if e.Filename == "" {
			utils.Fatalf("Dump entry is missing its filename")
		}
		if filepath.Base(e.Filename) != e.Filename {
			utils.Fatalf("Refusing to write dump entry with path separator: %s", e.Filename)
		}
		target := filepath.Join(dest, e.Filename)
		if _, err := os.Stat(target); err == nil && !overwrite {
			skipped++
			continue
		} else if err != nil && !os.IsNotExist(err) {
			utils.Fatalf("Could not stat %s: %v", target, err)
		}
		if !json.Valid(e.Keyjson) {
			utils.Fatalf("Dump entry %s does not contain valid JSON", e.Filename)
		}
		if err := os.WriteFile(target, e.Keyjson, 0o600); err != nil {
			utils.Fatalf("Failed to write %s: %v", target, err)
		}
		written++
	}
	fmt.Fprintf(os.Stderr, "Restored %d account(s) into %s (%d skipped).\n", written, dest, skipped)
	return nil
}

func readAllStdin() ([]byte, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil && !errors.Is(err, io.EOF) {
		return data, err
	}
	return data, nil
}
