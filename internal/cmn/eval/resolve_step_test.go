// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStepProperty(t *testing.T) {
	tests := []struct {
		name    string
		step    string
		path    string
		stepMap map[string]StepInfo
		wantOK  bool
		wantVal string
	}{
		{
			name:    "ValidStdout",
			step:    "step1",
			path:    ".stdout",
			stepMap: map[string]StepInfo{"step1": {Stdout: "/tmp/out", ExitCode: "0"}},
			wantOK:  true,
			wantVal: "/tmp/out",
		},
		{
			name:    "ValidExitCode",
			step:    "step1",
			path:    ".exit_code",
			stepMap: map[string]StepInfo{"step1": {Stdout: "out", ExitCode: "42"}},
			wantOK:  true,
			wantVal: "42",
		},
		{
			name:    "ValidStderr",
			step:    "step1",
			path:    ".stderr",
			stepMap: map[string]StepInfo{"step1": {Stderr: "/tmp/err"}},
			wantOK:  true,
			wantVal: "/tmp/err",
		},
		{
			name:    "EmptyStderr",
			step:    "step1",
			path:    ".stderr",
			stepMap: map[string]StepInfo{"step1": {Stdout: "out", Stderr: "", ExitCode: "0"}},
			wantOK:  false,
		},
		{
			name:    "ValidOutput",
			step:    "step1",
			path:    ".output",
			stepMap: map[string]StepInfo{"step1": {Output: new("some value")}},
			wantOK:  true,
			wantVal: "some value",
		},
		{
			name:    "EmptyOutput",
			step:    "step1",
			path:    ".output",
			stepMap: map[string]StepInfo{"step1": {Output: new("")}},
			wantOK:  true,
			wantVal: "",
		},
		{
			name:    "NilOutput",
			step:    "step1",
			path:    ".output",
			stepMap: map[string]StepInfo{"step1": {Stdout: "out", ExitCode: "0"}},
			wantOK:  false,
		},
		{
			name:    "OutputWithSlicing",
			step:    "step1",
			path:    ".output:0:5",
			stepMap: map[string]StepInfo{"step1": {Output: new("hello world")}},
			wantOK:  true,
			wantVal: "hello",
		},
		{
			name:    "NestedOutputField",
			step:    "step1",
			path:    ".output.version",
			stepMap: map[string]StepInfo{"step1": {Output: new(`{"version":"v1.2.3"}`)}},
			wantOK:  true,
			wantVal: "v1.2.3",
		},
		{
			name:    "NestedOutputObject",
			step:    "step1",
			path:    ".output.meta",
			stepMap: map[string]StepInfo{"step1": {Output: new(`{"meta":{"env":"stg"}}`)}},
			wantOK:  true,
			wantVal: `{"env":"stg"}`,
		},
		{
			name:    "ValidOutputs",
			step:    "step1",
			path:    ".outputs",
			stepMap: map[string]StepInfo{"step1": {Outputs: new(`{"messageId":"msg-123"}`)}},
			wantOK:  true,
			wantVal: `{"messageId":"msg-123"}`,
		},
		{
			name:    "NestedOutputsField",
			step:    "step1",
			path:    ".outputs.messageId",
			stepMap: map[string]StepInfo{"step1": {Outputs: new(`{"messageId":"msg-123"}`)}},
			wantOK:  true,
			wantVal: "msg-123",
		},
		{
			name:    "NilOutputs",
			step:    "step1",
			path:    ".outputs.messageId",
			stepMap: map[string]StepInfo{"step1": {Stdout: "out", ExitCode: "0"}},
			wantOK:  false,
		},
		{
			name:    "UnknownProperty",
			step:    "step1",
			path:    ".unknown_prop",
			stepMap: map[string]StepInfo{"step1": {Stdout: "out", ExitCode: "0"}},
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := resolveStepProperty(context.Background(), tt.step, tt.path, tt.stepMap)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantVal, val)
			}
		})
	}
}

func TestParseStepReference(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantProp string
		wantSpec stepSliceSpec
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "NoSlice",
			path:     ".stdout",
			wantProp: ".stdout",
		},
		{
			name:     "WithStartOnly",
			path:     ".stdout:3",
			wantProp: ".stdout",
			wantSpec: stepSliceSpec{hasStart: true, start: 3},
		},
		{
			name:     "WithStartAndLength",
			path:     ".stdout:3:5",
			wantProp: ".stdout",
			wantSpec: stepSliceSpec{hasStart: true, start: 3, hasLength: true, length: 5},
		},
		{
			name:    "EmptySliceSpec",
			path:    ".stdout:",
			wantErr: true,
			errMsg:  "slice specification missing values",
		},
		{
			name:    "TooManyParts",
			path:    ".stdout:1:2:3",
			wantErr: true,
			errMsg:  "too many slice sections",
		},
		{
			name:    "EmptyOffset",
			path:    ".stdout::5",
			wantErr: true,
			errMsg:  "slice offset is required",
		},
		{
			name:    "InvalidOffset",
			path:    ".stdout:abc",
			wantErr: true,
			errMsg:  "invalid slice offset",
		},
		{
			name:    "NegativeOffset",
			path:    ".stdout:-1",
			wantErr: true,
			errMsg:  "slice offset must be non-negative",
		},
		{
			name:    "InvalidLength",
			path:    ".stdout:0:xyz",
			wantErr: true,
			errMsg:  "invalid slice length",
		},
		{
			name:    "NegativeLength",
			path:    ".stdout:0:-5",
			wantErr: true,
			errMsg:  "slice length must be non-negative",
		},
		{
			name:     "ZeroStart",
			path:     ".exit_code:0:10",
			wantProp: ".exit_code",
			wantSpec: stepSliceSpec{hasStart: true, start: 0, hasLength: true, length: 10},
		},
		{
			name:     "EmptyLengthPart",
			path:     ".stdout:5:",
			wantProp: ".stdout",
			wantSpec: stepSliceSpec{hasStart: true, start: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prop, spec, err := parseStepReference(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantProp, prop)
			assert.Equal(t, tt.wantSpec, spec)
		})
	}
}

func TestApplyStepSlice(t *testing.T) {
	tests := []struct {
		name  string
		value string
		spec  stepSliceSpec
		want  string
	}{
		{
			name:  "NoSlice",
			value: "hello",
			spec:  stepSliceSpec{},
			want:  "hello",
		},
		{
			name:  "StartOnly",
			value: "hello world",
			spec:  stepSliceSpec{hasStart: true, start: 6},
			want:  "world",
		},
		{
			name:  "StartAndLength",
			value: "hello world",
			spec:  stepSliceSpec{hasStart: true, start: 0, hasLength: true, length: 5},
			want:  "hello",
		},
		{
			name:  "StartBeyondLength",
			value: "short",
			spec:  stepSliceSpec{hasStart: true, start: 100},
			want:  "",
		},
		{
			name:  "LengthExceedsRemainder",
			value: "hello",
			spec:  stepSliceSpec{hasStart: true, start: 3, hasLength: true, length: 100},
			want:  "lo",
		},
		{
			name:  "UnicodeChars",
			value: "日本語テスト",
			spec:  stepSliceSpec{hasStart: true, start: 0, hasLength: true, length: 3},
			want:  "日本語",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyStepSlice(tt.value, tt.spec)
			assert.Equal(t, tt.want, got)
		})
	}
}
