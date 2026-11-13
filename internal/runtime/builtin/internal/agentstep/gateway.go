// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// GatewayStep returns the step as the AI gateway of Spec 094 names it: the run,
// the DAG, the workspace of the DAG, the step and the key allow of its named MCP
// servers. Only the local mode reads the key allow. The coordinator reads it from
// the DAG that it holds.
func GatewayStep(ctx context.Context, stepName string) aigateway.StepRef {
	rc := runctx.GetContext(ctx)
	ref := aigateway.StepRef{RunID: rc.DAGRunID, StepName: stepName}
	if rc.DAG != nil {
		ref.DAGName = rc.DAG.Name
		ref.Workspace, _ = workspace.WorkspaceNameFromLabels(rc.DAG.Labels)
		if step, ok := rc.DAG.SecretStep(stepName); ok {
			for _, name := range step.AIMCPServers() {
				if ref.MCPAllow == nil {
					ref.MCPAllow = map[string][]string{}
				}
				ref.MCPAllow[name] = step.MCPAllow(name)
			}
		}
	}
	return ref
}
