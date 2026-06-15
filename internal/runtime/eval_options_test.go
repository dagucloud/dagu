// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
)

func TestCommandEvalOptionsDirectAndEmptyUseOSExpansion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		shell []string
	}{
		{name: "EmptyShell"},
		{name: "DirectShell", shell: []string{"direct"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := cmnvalue.NewOptions()
			for _, opt := range runtime.CommandEvalOptions(tt.shell) {
				opt(opts)
			}

			assert.True(t, opts.ExpandOS)
		})
	}
}
