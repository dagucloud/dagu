// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
)

func TestDispatchOperationStringUsesDomainNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   exec.DispatchOperation
		want string
	}{
		{name: "unspecified", op: exec.DispatchOperationUnspecified, want: "unspecified"},
		{name: "start", op: exec.DispatchOperationStart, want: "start"},
		{name: "retry", op: exec.DispatchOperationRetry, want: "retry"},
		{name: "unknown", op: exec.DispatchOperation(99), want: "DispatchOperation(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.op.String())
		})
	}
}
