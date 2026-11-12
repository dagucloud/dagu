// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// RetryNodesForTest returns runtime retry nodes for the supplied DAG and status.
func RetryNodesForTest(dag *ir.DAG, status *ir.DAGRunStatus) ([]*runtime.Node, error) {
	a := &Agent{
		dag:         dag,
		retryTarget: status,
	}
	return a.retryNodes()
}

// RetryPlanForTest builds the agent's default retry plan from a persisted
// status so tests cover forwarding of checkpoint-resume intent.
func RetryPlanForTest(dag *ir.DAG, status *ir.DAGRunStatus) (*runtime.Plan, error) {
	a := &Agent{dag: dag, retryTarget: status}
	nodes, err := a.retryNodes()
	if err != nil {
		return nil, err
	}
	return a.setupDefaultRetryPlan(context.Background(), nodes)
}

func RuntimeConfigVarsForTest(
	defaultEnvs []string,
	defaultSecrets []string,
	dagEnv []string,
	selectedEnvs []string,
	selectedSecrets []string,
	secretEnvs []string,
) map[string]string {
	return runtimeConfigVars(dagEnv, resolvedProfileValues{
		defaultEnvs:     defaultEnvs,
		defaultSecrets:  defaultSecrets,
		selectedEnvs:    selectedEnvs,
		selectedSecrets: selectedSecrets,
	}, secretEnvs)
}
