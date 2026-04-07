// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// getFreeDiskSpace mirrors cmd/utils/diskusage_windows.go.
func getFreeDiskSpace(path string) (uint64, error) {
	wpath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("UTF16PtrFromString: %w", err)
	}
	var freeBytesAvailableToCaller, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(wpath, &freeBytesAvailableToCaller, &totalBytes, &totalFreeBytes); err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx: %w", err)
	}
	return freeBytesAvailableToCaller, nil
}
