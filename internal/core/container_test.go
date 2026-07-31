// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestPullPolicy(t *testing.T) {
	tests := []struct {
		name        string
		pull        any
		expected    core.PullPolicy
		expectError bool
	}{
		{
			name:        "Nil",
			pull:        nil,
			expected:    core.PullPolicyMissing,
			expectError: false,
		},
		{
			name:        "EmptyString",
			pull:        "",
			expected:    core.PullPolicyMissing,
			expectError: false,
		},
		{
			name:        "TrueBool",
			pull:        true,
			expected:    core.PullPolicyAlways,
			expectError: false,
		},
		{
			name:        "TrueString",
			pull:        "true",
			expected:    core.PullPolicyAlways,
			expectError: false,
		},
		{
			name:        "FalseBool",
			pull:        false,
			expected:    core.PullPolicyNever,
			expectError: false,
		},
		{
			name:        "FalseString",
			pull:        "false",
			expected:    core.PullPolicyNever,
			expectError: false,
		},
		{
			name:        "MissingString",
			pull:        "missing",
			expected:    core.PullPolicyMissing,
			expectError: false,
		},
		{
			name:        "AlwaysString",
			pull:        "always",
			expected:    core.PullPolicyAlways,
			expectError: false,
		},
		{
			name:        "NeverString",
			pull:        "never",
			expected:    core.PullPolicyNever,
			expectError: false,
		},
		{
			name:        "FallbackString",
			pull:        "fallback",
			expected:    core.PullPolicyFallback,
			expectError: false,
		},
		{
			name:        "Error",
			pull:        "random pull policy should not exist",
			expected:    core.PullPolicyMissing,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := core.ParsePullPolicy(tt.pull)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return // No need to check the result if we expected an error
			}

			assert.Equal(t, tt.expected, actual, "PullPolicy should match expected value")
		})
	}
}
