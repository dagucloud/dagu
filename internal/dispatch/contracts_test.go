// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dispatch_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/stretchr/testify/assert"
)

func TestDispatchOperationStringUsesDomainNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   dispatch.DispatchOperation
		want string
	}{
		{name: "unspecified", op: dispatch.DispatchOperationUnspecified, want: "unspecified"},
		{name: "start", op: dispatch.DispatchOperationStart, want: "start"},
		{name: "retry", op: dispatch.DispatchOperationRetry, want: "retry"},
		{name: "unknown", op: dispatch.DispatchOperation(99), want: "DispatchOperation(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.op.String())
		})
	}
}
