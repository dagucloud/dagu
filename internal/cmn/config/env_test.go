// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/stretchr/testify/require"
)

func TestLoadBaseEnv(t *testing.T) {
	windowsExpected := runtime.GOOS == "windows"

	testCases := []struct {
		name     string
		expected bool
	}{
		{"TEST_VAR_BASE_ENV", false},
		{"DAGU_TEST_BASE_ENV", true},

		// Windows-specific user profile and install roots should only pass
		// through on Windows builds.
		{"APPDATA", windowsExpected},
		{"LOCALAPPDATA", windowsExpected},
		{"USERNAME", windowsExpected},
		{"USERDOMAIN", windowsExpected},
		{"PROGRAMFILES", windowsExpected},
		{"PROGRAMFILES(X86)", windowsExpected},
		{"PROGRAMDATA", windowsExpected},

		// Docker daemon connection vars must pass through.
		{"DOCKER_HOST", true},
		{"DOCKER_TLS_VERIFY", true},
		{"DOCKER_CERT_PATH", true},
		{"DOCKER_API_VERSION", true},

		// Docker credentials must NOT leak through the global env.
		{"DOCKER_AUTH_CONFIG", false},

		// Kubernetes in-cluster discovery vars must pass through so kubectl and
		// client-go based tools do not fall back to localhost:8080.
		{"KUBERNETES_SERVICE_HOST", true},
		{"KUBERNETES_SERVICE_PORT", true},
		{"KUBERNETES_SERVICE_PORT_HTTPS", true},
	}

	for _, tc := range testCases {
		t.Setenv(tc.name, "value")
	}

	baseEnv := config.LoadBaseEnv()
	envSlice := baseEnv.AsSlice()
	envMap := parseEnvSlice(envSlice)

	for _, tc := range testCases {
		_, found := envMap[tc.name]
		require.Equal(t, tc.expected, found, "expected %s: %v", tc.name, tc.expected)
	}
}

func TestBaseEnv_AsSlice(t *testing.T) {
	t.Parallel()

	baseEnv := config.NewBaseEnv([]string{"A=1", "B=2"})
	require.Equal(t, []string{"A=1", "B=2"}, baseEnv.AsSlice())
}

func TestLoadBaseEnvWithExtras_ExactNamesAndPrefixes(t *testing.T) {
	t.Setenv("EXTRA_ALLOWED_NAME", "name-value")
	t.Setenv("EXTRA_ALLOWED_PREFIX_ONE", "prefix-value")
	t.Setenv("EXTRA_BLOCKED", "blocked-value")

	baseEnv := config.LoadBaseEnvWithExtras(
		[]string{" EXTRA_ALLOWED_NAME ", "EXTRA_ALLOWED_NAME", ""},
		[]string{" EXTRA_ALLOWED_PREFIX_", "EXTRA_ALLOWED_PREFIX_", ""},
	)
	envMap := parseEnvSlice(baseEnv.AsSlice())

	require.Equal(t, "name-value", envMap["EXTRA_ALLOWED_NAME"])
	require.Equal(t, "prefix-value", envMap["EXTRA_ALLOWED_PREFIX_ONE"])
	_, found := envMap["EXTRA_BLOCKED"]
	require.False(t, found)
}

func TestLoadBaseEnvWithExtras_PrefixMatchingHonorsPlatformCaseRules(t *testing.T) {
	t.Setenv("CASE_MATCH_ENV", "matched")

	baseEnv := config.LoadBaseEnvWithExtras(nil, []string{"case_"})
	envMap := parseEnvSlice(baseEnv.AsSlice())

	_, found := envMap["CASE_MATCH_ENV"]
	if runtime.GOOS == "windows" {
		require.True(t, found)
	} else {
		require.False(t, found)
	}
}

func parseEnvSlice(envSlice []string) map[string]string {
	envMap := make(map[string]string)
	for _, kv := range envSlice {
		key, value, found := strings.Cut(kv, "=")
		if found {
			envMap[key] = value
		}
	}
	return envMap
}
