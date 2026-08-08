// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCondition_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		condition *ir.Condition
		expected  string
	}{
		{
			name: "Basic",
			condition: &ir.Condition{
				Condition: "test -f file.txt",
				Expected:  "true",
			},
			expected: `{"condition":"test -f file.txt","expected":"true"}`,
		},
		{
			name: "WithErrorMessage",
			condition: func() *ir.Condition {
				c := &ir.Condition{
					Condition: "test -f file.txt",
					Expected:  "true",
				}
				c.SetErrorMessage("file not found")
				return c
			}(),
			expected: `{"condition":"test -f file.txt","expected":"true","error":"file not found"}`,
		},
		{
			name: "WithEval",
			condition: &ir.Condition{
				Eval:     "$(printf ready)",
				Expected: "ready",
			},
			expected: `{"eval":"$(printf ready)","expected":"ready"}`,
		},
		{
			name:      "EmptyFields",
			condition: &ir.Condition{},
			expected:  `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tt.condition)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))
		})
	}
}

func TestCondition_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		json     string
		expected *ir.Condition
	}{
		{
			name: "Basic",
			json: `{"condition":"test -f file.txt","expected":"true"}`,
			expected: &ir.Condition{
				Condition: "test -f file.txt",
				Expected:  "true",
			},
		},
		{
			name: "WithErrorMessage",
			json: `{"condition":"test -f file.txt","expected":"true","error":"file not found"}`,
			expected: func() *ir.Condition {
				c := &ir.Condition{
					Condition: "test -f file.txt",
					Expected:  "true",
				}
				c.SetErrorMessage("file not found")
				return c
			}(),
		},
		{
			name: "WithEval",
			json: `{"eval":"$(printf ready)","expected":"ready"}`,
			expected: &ir.Condition{
				Eval:     "$(printf ready)",
				Expected: "ready",
			},
		},
		{
			name:     "EmptyFields",
			json:     `{}`,
			expected: &ir.Condition{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var condition ir.Condition
			err := json.Unmarshal([]byte(tt.json), &condition)
			require.NoError(t, err)

			assert.Equal(t, tt.expected.Condition, condition.Condition)
			assert.Equal(t, tt.expected.Eval, condition.Eval)
			assert.Equal(t, tt.expected.Expected, condition.Expected)
			assert.Equal(t, tt.expected.GetErrorMessage(), condition.GetErrorMessage())
		})
	}
}

func TestCondition_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		condition *ir.Condition
		wantErr   bool
	}{
		{
			name: "Valid",
			condition: &ir.Condition{
				Condition: "test -f file.txt",
				Expected:  "true",
			},
			wantErr: false,
		},
		{
			name: "MissingValueSource",
			condition: &ir.Condition{
				Expected: "true",
			},
			wantErr: true,
		},
		{
			name: "EvalValueMatch",
			condition: &ir.Condition{
				Eval:     "$(printf ready)",
				Expected: "ready",
			},
			wantErr: false,
		},
		{
			name: "EvalWithoutExpected",
			condition: &ir.Condition{
				Eval: "$(printf ready)",
			},
			wantErr: true,
		},
		{
			name: "ConditionAndEval",
			condition: &ir.Condition{
				Condition: "ready",
				Eval:      "$(printf ready)",
				Expected:  "ready",
			},
			wantErr: true,
		},
		{
			name: "EmptyExpected",
			condition: &ir.Condition{
				Condition: "test -f file.txt",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.condition.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCondition_ErrorMessage(t *testing.T) {
	t.Parallel()

	condition := &ir.Condition{
		Condition: "test -f file.txt",
		Expected:  "true",
	}

	// Initial error message should be empty
	assert.Empty(t, condition.GetErrorMessage())

	// Set error message
	errorMsg := "file not found"
	condition.SetErrorMessage(errorMsg)
	assert.Equal(t, errorMsg, condition.GetErrorMessage())

	// Update error message
	newErrorMsg := "permission denied"
	condition.SetErrorMessage(newErrorMsg)
	assert.Equal(t, newErrorMsg, condition.GetErrorMessage())
}

func TestCondition_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	condition := &ir.Condition{
		Condition: "test -f file.txt",
		Expected:  "true",
	}

	// Test concurrent access to error message
	done := make(chan bool)
	go func() {
		for range 100 {
			condition.SetErrorMessage("message 1")
			_ = condition.GetErrorMessage()
		}
		done <- true
	}()

	go func() {
		for range 100 {
			condition.SetErrorMessage("message 2")
			_ = condition.GetErrorMessage()
		}
		done <- true
	}()

	// Wait for goroutines to finish
	<-done
	<-done

	// No assertion needed, we're just testing that there's no race condition
}
