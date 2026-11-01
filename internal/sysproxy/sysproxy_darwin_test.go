// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubScutil makes scutil print out, or fail with err.
func stubScutil(t *testing.T, out string, err error) {
	t.Helper()
	original := runScutil
	runScutil = func(context.Context) ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { runScutil = original })
}

func TestDetectMacOS(t *testing.T) {
	ctx := context.Background()

	t.Run("ConfiguredScript", func(t *testing.T) {
		scriptURL := servePAC(t, testPAC)
		stubScutil(t, "<dictionary> {\n  ProxyAutoConfigEnable : 1\n  ProxyAutoConfigURLString : "+scriptURL+"\n}\n", nil)
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assert.Equal(t, testPAC, script)
	})

	t.Run("Static", func(t *testing.T) {
		stubScutil(t, scutilStatic, nil)
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "fe80::/10")
		assertRoutes(t, script, []routeCase{
			{"https://www.example.com/", "PROXY secure.corp.example:8443"},
			{"http://printer.local/", "DIRECT"},
		})
	})

	t.Run("ExplicitOverridesSettings", func(t *testing.T) {
		stubScutil(t, scutilStatic, nil)
		script, warnings, err := Detect(ctx, "socks5://socks.example")
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assertRoutes(t, script, []routeCase{{"https://www.example.com/", "SOCKS5 socks.example:1080"}})
	})

	t.Run("ScutilFails", func(t *testing.T) {
		stubScutil(t, "", errors.New("exit status 1"))
		script, warnings, err := Detect(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, directPAC, script)
		assert.Equal(t, []string{"could not read the macOS proxy settings: exit status 1; requests go DIRECT"}, warnings)
	})
}
