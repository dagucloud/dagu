// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddr(t *testing.T) {
	t.Parallel()

	t.Run("Deterministic", func(t *testing.T) {
		t.Parallel()
		addr := Addr("mydag", "run123")
		require.Equal(t, addr, Addr("mydag", "run123"))
		require.NotEqual(t, addr, Addr("mydag", "run456"))
		require.True(t, strings.HasPrefix(addr, filepath.Join(socketDir(), "@dagu_")))
		require.True(t, strings.HasSuffix(addr, ".sock"))
	})

	t.Run("SafeAndPortableLength", func(t *testing.T) {
		t.Parallel()
		addr := Addr("my/dag\\with:special*chars"+strings.Repeat("x", 100), "run|with<>chars")
		name := filepath.Base(addr)
		require.LessOrEqual(t, len(name), 50)
		for _, char := range []string{"/", "\\", ":", "*", "|", "<", ">"} {
			require.NotContains(t, name, char)
		}
	})
}
