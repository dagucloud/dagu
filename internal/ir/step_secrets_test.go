// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

// A DAG has an untrusted step when a step, a handler or a step in the body of
// a foreach step is untrusted.
func TestHasUntrustedStep(t *testing.T) {
	t.Parallel()

	untrusted := ir.Step{Name: "agent", Untrusted: true}
	trusted := ir.Step{Name: "check"}
	for name, tc := range map[string]struct {
		dag  *ir.DAG
		want bool
	}{
		"nil":          {nil, false},
		"trusted":      {&ir.DAG{Steps: []ir.Step{trusted}}, false},
		"step":         {&ir.DAG{Steps: []ir.Step{trusted, untrusted}}, true},
		"handler":      {&ir.DAG{Steps: []ir.Step{trusted}, HandlerOn: ir.HandlerOn{Exit: &untrusted}}, true},
		"foreach body": {&ir.DAG{Steps: []ir.Step{{Name: "each", Foreach: &ir.ForeachConfig{Steps: []ir.Step{untrusted}}}}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.dag.HasUntrustedStep())
		})
	}
}
