// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	openapi "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/diagnostic"
	"github.com/dagucloud/dagu/internal/launcher"
)

func (a *API) attachRuntimeDiagnosticSink(spec *launcher.CmdSpec, dagName, dagRunID string) {
	if spec == nil {
		return
	}
	spec.DiagnosticSink = a.runtimeDiagnosticSink(dagName, dagRunID)
}

func (a *API) runtimeDiagnosticSink(dagName, dagRunID string) diagnostic.Sink {
	if a == nil || a.runDiagnostics == nil || dagName == "" || dagRunID == "" {
		return nil
	}
	return a.runDiagnostics.Sink(diagnostic.RunRef{Name: dagName, ID: dagRunID})
}

func (a *API) runtimeDiagnostics(dagName, dagRunID string) []openapi.Diagnostic {
	if a == nil || a.runDiagnostics == nil || dagName == "" || dagRunID == "" {
		return nil
	}
	return toAPIDiagnostics(a.runDiagnostics.Diagnostics(diagnostic.RunRef{
		Name: dagName,
		ID:   dagRunID,
	}))
}

func (a *API) toDAGRunDetailsWithDiagnostics(status exec.DAGRunStatus) openapi.DAGRunDetails {
	details := ToDAGRunDetails(status)
	if diagnostics := a.runtimeDiagnostics(status.Name, status.DAGRunID); len(diagnostics) > 0 {
		details.Diagnostics = &diagnostics
	}
	return details
}
