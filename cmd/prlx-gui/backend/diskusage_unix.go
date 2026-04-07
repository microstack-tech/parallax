// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

//go:build !windows

package backend

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// getFreeDiskSpace mirrors cmd/utils/diskusage.go (the CLI's helper, which
// is unexported and lives behind a `utils` import that would pull the whole
// CLI surface into the GUI binary). Same Statfs path, same FreeBSD-grace
// guard against negative blocks-available counts.
func getFreeDiskSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	bavail := stat.Bavail
	if bavail < 0 {
		bavail = 0
	}
	//nolint:unconvert
	return uint64(bavail) * uint64(stat.Bsize), nil
}
