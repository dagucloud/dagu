// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestExampleListsAvailableExamples(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("example")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "parallel-steps")
}

func TestExampleShowsOneByID(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("example", "1")
	result.ExpectExitCode(0)
	require.Contains(t, result.Stdout(), "Parallel Steps")
	require.Contains(t, result.Stdout(), "steps:")
}

func TestExampleRejectsUnknownID(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("example", "99999")
	result.ExpectNonZeroExitCode()
}
