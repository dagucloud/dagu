// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/diagnostic"
	dagruntime "github.com/dagucloud/dagu/internal/runtime"
	runtimeagent "github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusIncludesBuildDiagnostics(t *testing.T) {
	t.Parallel()

	buildDiagnostic := diagnostic.Diagnostic{
		Level:   diagnostic.LevelNotice,
		Code:    diagnostic.CodeValueReferenceUnresolved,
		Field:   "consts.image",
		Token:   "${params.image}",
		Message: "${params.image} was left unchanged because params.image had no value when consts.image was evaluated.",
	}
	dagAgent := runtimeagent.New(
		"run-id",
		&core.DAG{
			Name:        "diagnostics",
			Diagnostics: []diagnostic.Diagnostic{buildDiagnostic},
		},
		"",
		"",
		dagruntime.Manager{},
		nil,
		runtimeagent.Options{},
	)

	status := dagAgent.Status(context.Background())

	require.Len(t, status.Diagnostics, 1)
	assert.Equal(t, buildDiagnostic, status.Diagnostics[0])
}
