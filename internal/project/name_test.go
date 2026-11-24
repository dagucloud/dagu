// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package project_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/require"
)

func TestValidateName(t *testing.T) {
	t.Parallel()

	valid := []string{
		"a",
		"9",
		"weather",
		"Day-Ahead_2",
		strings.Repeat("a", 64),
		"con1",
		"com10",
		"console",
		"defaults",
	}
	for _, name := range valid {
		require.NoError(t, project.ValidateName(name), name)
	}

	invalid := []string{
		"",
		"-ops",
		"_ops",
		"a b",
		"a.b",
		"a/b",
		"ünicode",
		strings.Repeat("a", 65),
		// Windows device names, in any case.
		"con", "CON", "Prn", "aux", "NUL", "com0", "COM9", "lpt0", "Lpt9",
		// The default project's name, in any case.
		"default", "DEFAULT", "Default",
	}
	for _, name := range invalid {
		require.ErrorIs(t, project.ValidateName(name), project.ErrInvalidName, name)
	}
}

func TestValidateDisplayName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "Weather & Grid", strings.Repeat("天", 200)} {
		require.NoError(t, project.ValidateDisplayName(name), name)
	}
	for _, name := range []string{strings.Repeat("a", 201), "bad \xff byte"} {
		require.ErrorIs(t, project.ValidateDisplayName(name), project.ErrInvalidDisplayName, name)
	}
}
