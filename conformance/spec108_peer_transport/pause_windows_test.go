// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package spec108_peer_transport_test

import "testing"

// pauseProcess leaves the process running: Windows has no signal that
// pauses one.
func pauseProcess(*testing.T, int) {}
