// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuiltinHarnessProviderNamesSorted(t *testing.T) {
	assert.Equal(t, []string{"builtin", "claude", "codex", "copilot", "opencode", "pi"}, BuiltinHarnessProviderNames())
}

func TestBuiltinCLIHarnessProviderNamesSorted(t *testing.T) {
	assert.Equal(t, []string{"claude", "codex", "copilot", "opencode", "pi"}, BuiltinCLIHarnessProviderNames())
}

func TestIsBuiltinAgentHarnessProvider(t *testing.T) {
	assert.True(t, IsBuiltinAgentHarnessProvider("builtin"))
	assert.False(t, IsBuiltinAgentHarnessProvider("codex"))
}

func TestIsBuiltinCLIHarnessProvider(t *testing.T) {
	assert.True(t, IsBuiltinCLIHarnessProvider("codex"))
	assert.False(t, IsBuiltinCLIHarnessProvider("builtin"))
}
