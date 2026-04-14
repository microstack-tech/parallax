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

//go:build windows
// +build windows

package main

import "syscall"

// daemonSysProcAttr returns process attributes that detach the child from
// the parent's console window so it continues running when the parent exits.
// DETACHED_PROCESS (0x00000008) prevents the child from inheriting the
// console; CREATE_NEW_PROCESS_GROUP (0x00000200) gives it its own signal
// scope.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200,
	}
}
