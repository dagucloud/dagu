// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
)

// RetryNodesForTest returns runtime retry nodes for the supplied DAG and status.
func RetryNodesForTest(dag *core.DAG, status *exec.DAGRunStatus) ([]*runtime.Node, error) {
	a := &Agent{
		dag:         dag,
		retryTarget: status,
	}
	return a.retryNodes()
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
