// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package reffield_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/reffield"
)

func TestLiteralJQFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config ir.ExecutorConfig
		want   bool
	}{
		{"jq with args", ir.ExecutorConfig{Type: "jq", Config: map[string]any{"args": map[string]any{}}}, true},
		{"jq without args", ir.ExecutorConfig{Type: "jq", Config: map[string]any{"raw": true}}, false},
		{"other executor with args", ir.ExecutorConfig{Type: "http", Config: map[string]any{"args": map[string]any{}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, reffield.LiteralJQFilter(ir.Step{ExecutorConfig: tt.config}))
		})
	}
}

// A jq step with args takes its filter as literal jq source, so its command is
// not a field that value resolution reads.
func TestReferenceFieldsSkipLiteralJQFilter(t *testing.T) {
	t.Parallel()

	jq := ir.Step{
		Name:           "filter",
		Commands:       []ir.CommandEntry{{CmdWithArgs: "${steps.x.outputs.y}"}},
		ExecutorConfig: ir.ExecutorConfig{Type: "jq", Config: map[string]any{"args": map[string]any{}}},
	}
	for _, field := range reffield.ReferenceFields(&ir.DAG{Steps: []ir.Step{jq}}) {
		assert.NotEqual(t, "${steps.x.outputs.y}", field.Value, field.Path)
	}
}
