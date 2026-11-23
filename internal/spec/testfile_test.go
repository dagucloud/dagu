// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
)

// Only the full suffix marks a test file, since DAG names may contain dots.
func TestCutTestFileSuffix(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"release.test.yaml":     "release",
		"etl/daily.test.yml":    "etl/daily",
		"load.test.v2.test.yml": "load.test.v2",
	} {
		base, ok := spec.CutTestFileSuffix(path)
		assert.True(t, ok, path)
		assert.Equal(t, want, base, path)
		assert.True(t, spec.IsTestFile(path), path)
	}
	for _, path := range []string{"release.yaml", "latest.yaml", "smoke_test.yaml", "load.test.v2.yaml", "release.test"} {
		base, ok := spec.CutTestFileSuffix(path)
		assert.False(t, ok, path)
		assert.Equal(t, path, base, path)
		assert.False(t, spec.IsTestFile(path), path)
	}
}
