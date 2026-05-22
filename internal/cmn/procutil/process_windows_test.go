// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package procutil

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanLookupStartTimeAcceptsWindowsUint32PIDs(t *testing.T) {
	t.Parallel()

	if strconv.IntSize < 64 {
		t.Skip("requires an int that can represent Windows uint32 PIDs above int32")
	}

	one := int64(1)
	require.True(t, canLookupStartTime(int(one<<31)))
	require.True(t, canLookupStartTime(int((one<<32)-1)))
	require.False(t, canLookupStartTime(int(one<<32)))
}
