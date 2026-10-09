// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !(windows && (amd64 || arm64))

package desktop

// OpenElements fails: elements are read through UI Automation, which
// needs 64-bit Windows.
func OpenElements() (Elements, error) {
	return nil, ErrElementsUnsupported
}
