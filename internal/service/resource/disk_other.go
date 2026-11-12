// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !unix

package resource

import (
	"path/filepath"
	"strings"
)

// sameDisk reports whether the two paths are on one volume.
func sameDisk(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return true
	}
	return strings.EqualFold(filepath.VolumeName(absA), filepath.VolumeName(absB))
}
