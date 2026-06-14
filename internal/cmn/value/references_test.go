// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanReferencesClassifiesReservedAndCompatibilityRefs(t *testing.T) {
	t.Parallel()

	refs := value.ScanReferences("${consts.service} $env.FOO ${DATA.image} $DATA.tag", value.ModeWorkflowValue)

	require.Len(t, refs, 4)
	assert.Equal(t, value.ReferenceDagu, refs[0].Kind)
	assert.Equal(t, "consts", refs[0].Namespace)
	assert.True(t, refs[0].Strict)
	assert.Equal(t, value.ReferenceInvalid, refs[1].Kind)
	assert.Contains(t, refs[1].Err.Error(), "invalid binding shorthand")
	assert.Equal(t, value.ReferenceCompatibility, refs[2].Kind)
	assert.True(t, refs[2].Strict)
	assert.Equal(t, value.ReferenceCompatibility, refs[3].Kind)
	assert.False(t, refs[3].Strict)
}

func TestValidateReferencesModeMatrix(t *testing.T) {
	t.Parallel()

	scope := value.Scope{
		Consts: value.Values{"service": "api"},
		Params: value.Values{"environment": "prod"},
		Env:    value.Values{"HOME": "/workspace"},
		Steps:  value.StepOutputs{"build": value.Values{"image": true}},
	}

	tests := []struct {
		name    string
		raw     string
		mode    value.Mode
		wantErr string
	}{
		{
			name: "ConstLoadAllowsConsts",
			raw:  "${consts.service}",
			mode: value.ModeConstLoad,
		},
		{
			name:    "ConstLoadRejectsParams",
			raw:     "${params.environment}",
			mode:    value.ModeConstLoad,
			wantErr: "not available while loading consts",
		},
		{
			name:    "ReservedShorthandRejected",
			raw:     "$env.HOME",
			mode:    value.ModeWorkflowValue,
			wantErr: "invalid binding shorthand",
		},
		{
			name:    "UnknownConstRejected",
			raw:     "${consts.missing}",
			mode:    value.ModeStaticValidation,
			wantErr: "unknown consts binding",
		},
		{
			name:    "UnknownDeclaredOutputRejected",
			raw:     "${steps.build.outputs.digest}",
			mode:    value.ModeStaticValidation,
			wantErr: "unknown output",
		},
		{
			name: "CompatibilityRefsAllowed",
			raw:  "${DATA.image} $DATA.tag",
			mode: value.ModeStaticValidation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := value.ValidateReferences(tt.raw, scope, tt.mode, "run")
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExpandStringResolvesStrictRefsAndKeepsCompatibilityRefs(t *testing.T) {
	t.Parallel()

	got, err := value.ExpandString(
		"${consts.service}:${params.environment}:${env.HOME}:${steps.build.outputs.image}:${DATA.image}:$DATA.tag",
		value.Scope{
			Consts: value.Values{"service": "api"},
			Params: value.Values{"environment": "prod"},
			Env:    value.Values{"HOME": "/workspace"},
			Steps:  value.StepOutputs{"build": value.Values{"image": "repo/api:v1"}},
		},
		value.ModeWorkflowValue,
		"run",
	)
	require.NoError(t, err)
	assert.Equal(t, "api:prod:/workspace:repo/api:v1:${DATA.image}:$DATA.tag", got)
}
