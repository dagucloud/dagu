// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package fileutil

import (
	"fmt"
	"os"
	"syscall"
)

func sameFileSystem(a, b string) (bool, error) {
	statA, err := deviceStat(a)
	if err != nil {
		return false, err
	}
	statB, err := deviceStat(b)
	if err != nil {
		return false, err
	}
	return statA.Dev == statB.Dev, nil
}

func deviceStat(path string) (*syscall.Stat_t, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("%s: no device information", path)
	}
	return stat, nil
}
