// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Spec 092, "Secrets of a step", "Resolution". In a DAG with step scope, the
// coordinator gives the value of a registry secret only to a step of the DAG of
// the attempt that lists the secret, while neither the attempt nor the step has
// a final status. The check of a secret at the start of the run has no value
// and names no step.

// authorizeStepSecretReference applies the rules of a step to a request that
// passed the checks of the lease, the attempt, the DAG and the workspace.
func (h *Handler) authorizeStepSecretReference(
	ctx context.Context,
	attempt dagrun.Attempt,
	dag *ir.DAG,
	req *coordinatorv1.ResolveSecretReferenceRequest,
	ref secretref.Ref,
) error {
	stepName := req.GetStepName()
	if stepName == "" {
		if req.GetCheckOnly() || !dag.HasSecretStepScope() {
			return nil
		}
		return stepSecretDenied("", ref.Name, "a DAG with step scope gives a registry secret only to a step that lists it")
	}

	step, ok := dag.SecretStep(stepName)
	if !ok {
		return stepSecretDenied(stepName, ref.Name, fmt.Sprintf("the DAG %q has no such step", dag.Name))
	}
	if !dag.SecretScopeOf(step).Allows(ref.Name) {
		return stepSecretDenied(stepName, ref.Name, "the step does not list the secret")
	}
	if req.GetCheckOnly() {
		return nil
	}

	runStatus, err := attempt.ReadStatusUncached(ctx)
	if err != nil || runStatus == nil {
		return stepSecretDenied(stepName, ref.Name, "the status of the attempt is not available")
	}
	if !attemptMayFetchSecret(runStatus.Status) {
		return stepSecretDenied(stepName, ref.Name, fmt.Sprintf("the attempt has the final status %s", runStatus.Status))
	}
	// The status of the attempt holds the steps of its own DAG only. A step of a
	// child DAG that runs in the same process has its status elsewhere.
	if runStatus.Name != "" && runStatus.Name != dag.Name {
		return nil
	}
	if node := statusNode(runStatus, stepName); node != nil && !nodeMayFetchSecret(node.Status) {
		return stepSecretDenied(stepName, ref.Name, fmt.Sprintf("the step has the final status %s", node.Status))
	}
	return nil
}

// attemptMayFetchSecret reports whether an attempt with the status has no
// final status. An unknown status is final.
func attemptMayFetchSecret(s ir.Status) bool {
	switch s {
	case ir.NotStarted, ir.Queued, ir.Running, ir.Waiting:
		return true
	case ir.Failed, ir.Aborted, ir.Succeeded, ir.PartiallySucceeded, ir.Rejected:
		return false
	}
	return false
}

// nodeMayFetchSecret reports whether a step with the status can still start. A
// step that waits for an approval already ran its process. An unknown status is
// final.
func nodeMayFetchSecret(s ir.NodeStatus) bool {
	switch s {
	case ir.NodeNotStarted, ir.NodeRunning, ir.NodeRetrying:
		return true
	case ir.NodeFailed, ir.NodeAborted, ir.NodeSucceeded, ir.NodeSkipped,
		ir.NodePartiallySucceeded, ir.NodeWaiting, ir.NodeRejected:
		return false
	}
	return false
}

// statusNode returns the node of the step or of the handler with the name.
func statusNode(runStatus *ir.DAGRunStatus, stepName string) *ir.Node {
	for _, node := range runStatus.Nodes {
		if node != nil && node.Step.Name == stepName {
			return node
		}
	}
	for _, node := range []*ir.Node{
		runStatus.OnInit, runStatus.OnSuccess, runStatus.OnFailure,
		runStatus.OnAbort, runStatus.OnExit, runStatus.OnWait,
	} {
		if node != nil && node.Step.Name == stepName {
			return node
		}
	}
	return nil
}

// stepSecretDenied returns the error of a refused request. It names the step
// and the secret and never holds a value.
func stepSecretDenied(stepName, secretName, reason string) error {
	if stepName == "" {
		return status.Error(codes.PermissionDenied, fmt.Sprintf("secret %q: %s", secretName, reason))
	}
	return status.Error(codes.PermissionDenied, fmt.Sprintf("step %q: secret %q: %s", stepName, secretName, reason))
}
