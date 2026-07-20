// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/dagwarning"
	"github.com/dagucloud/dagu/internal/launcher"
	"github.com/spf13/cobra"
)

const (
	humanTaskFlagInput      = "input"
	humanTaskFlagInputsJSON = "inputs-json"

	humanTaskSettleTimeout      = 5 * time.Second
	humanTaskSettlePollInterval = 50 * time.Millisecond
)

var errHumanTaskCompletionAlreadyApplied = errors.New("human task completion already applied")

var (
	humanTaskRunIDFlag = commandLineFlag{
		name:      "run-id",
		shorthand: "r",
		usage:     "DAG-run ID containing the human task",
		required:  true,
	}
	humanTaskStepFlag = commandLineFlag{
		name:     "step",
		usage:    "ID of the human task step to complete",
		required: true,
	}
	humanTaskInputsJSONFlag = commandLineFlag{
		name:  humanTaskFlagInputsJSON,
		usage: "Human task inputs as a JSON object",
	}
)

// HumanTask returns the command for managing human tasks.
func HumanTask() *cobra.Command {
	command := NewCommand(&cobra.Command{
		Use:   "human-task",
		Short: "Manage human tasks",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	command.AddCommand(humanTaskCompleteCommand())
	return command
}

func humanTaskCompleteCommand() *cobra.Command {
	command := NewCommand(&cobra.Command{
		Use:   "complete [flags] <DAG name>",
		Short: "Complete a waiting human task",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{
		humanTaskRunIDFlag,
		humanTaskStepFlag,
		humanTaskInputsJSONFlag,
	}, runHumanTaskComplete)
	command.Flags().StringArray(humanTaskFlagInput, nil, "Human task input in key=value form; repeatable")
	return command
}

type humanTaskCompletionInput struct {
	values        map[string]any
	coerceStrings bool
}

type humanTaskCompleteDeps struct {
	now    func() time.Time
	resume func(*Context, *core.DAG, *exec.DAGRunStatus) error
}

func defaultHumanTaskCompleteDeps() humanTaskCompleteDeps {
	return humanTaskCompleteDeps{
		now:    time.Now,
		resume: resumeHumanTaskRun,
	}
}

func runHumanTaskComplete(ctx *Context, args []string) error {
	return runHumanTaskCompleteWith(ctx, args, defaultHumanTaskCompleteDeps())
}

func runHumanTaskCompleteWith(ctx *Context, args []string, deps humanTaskCompleteDeps) error {
	if ctx.IsRemote() {
		return fmt.Errorf("human-task complete only supports the local context")
	}
	if ctx.DAGRunStore == nil {
		return fmt.Errorf("DAG-run store is not configured")
	}

	request, err := parseHumanTaskCompletionRequest(ctx)
	if err != nil {
		return err
	}
	completionInput, err := parseHumanTaskCompletionInput(ctx.Command)
	if err != nil {
		return err
	}
	target, err := loadHumanTaskCompletionTarget(ctx, args[0], request)
	if err != nil {
		return err
	}
	result, err := completeHumanTaskStatus(ctx, target, completionInput, deps)
	if err != nil {
		return err
	}
	if hasWaitingNodes(result.status.Nodes) {
		if result.alreadyCompleted {
			_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Human task %s was already completed.\n", request.stepID)
			return err
		}
		_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Completed human task %s; DAG-run remains waiting.\n", request.stepID)
		return err
	}
	if !result.resumeRequested {
		_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Human task %s was already completed.\n", request.stepID)
		return err
	}
	if err := deps.resume(ctx, target.dag, result.status); err != nil {
		rollbackHumanTaskResumeClaim(ctx, target, result)
		return fmt.Errorf(
			"human task %q was completed, but the DAG-run could not be resumed: %w; run the same completion command again to retry",
			request.stepID,
			err,
		)
	}
	message := fmt.Sprintf("Completed human task %s", request.stepID)
	if result.alreadyCompleted {
		message = fmt.Sprintf("Human task %s was already completed", request.stepID)
	}
	_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "%s; DAG-run resume requested.\n", message)
	return err
}

type humanTaskCompletionRequest struct {
	dagRunID string
	stepID   string
}

type humanTaskCompletionTarget struct {
	dag    *core.DAG
	status *exec.DAGRunStatus
	ref    exec.DAGRunRef
	stepID string
}

type humanTaskCompletionResult struct {
	status            *exec.DAGRunStatus
	alreadyCompleted  bool
	resumeRequested   bool
	waitingFinishedAt string
}

func parseHumanTaskCompletionRequest(ctx *Context) (humanTaskCompletionRequest, error) {
	dagRunID, err := ctx.StringParam(humanTaskRunIDFlag.name)
	if err != nil {
		return humanTaskCompletionRequest{}, err
	}
	stepID, err := ctx.StringParam(humanTaskStepFlag.name)
	if err != nil {
		return humanTaskCompletionRequest{}, err
	}
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return humanTaskCompletionRequest{}, fmt.Errorf("--step must not be empty")
	}
	return humanTaskCompletionRequest{
		dagRunID: dagRunID,
		stepID:   stepID,
	}, nil
}

func loadHumanTaskCompletionTarget(
	ctx *Context,
	dagArg string,
	request humanTaskCompletionRequest,
) (*humanTaskCompletionTarget, error) {
	dagName, err := extractDAGName(ctx, dagArg)
	if err != nil {
		return nil, fmt.Errorf("failed to extract DAG name: %w", err)
	}
	ref := exec.NewDAGRunRef(dagName, request.dagRunID)
	attempt, err := ctx.DAGRunStore.FindAttempt(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to find DAG-run %q with run ID %q: %w", dagName, request.dagRunID, err)
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read DAG from run data: %w", err)
	}
	if dag == nil {
		return nil, fmt.Errorf("failed to read DAG from run data: DAG data is nil")
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read DAG-run status: %w", err)
	}
	if status == nil {
		return nil, fmt.Errorf("failed to read DAG-run status: status data is nil")
	}
	status, err = waitForHumanTaskCompletionReady(ctx, attempt, dag, status, request.stepID)
	if err != nil {
		return nil, err
	}

	storedRef := status.DAGRun()
	if storedRef.Zero() {
		return nil, fmt.Errorf("stored DAG-run identity is incomplete")
	}
	if storedRef != ref {
		return nil, fmt.Errorf("stored DAG-run identity %s does not match requested DAG-run %s", storedRef, ref)
	}
	return &humanTaskCompletionTarget{
		dag:    dag,
		status: status,
		ref:    ref,
		stepID: request.stepID,
	}, nil
}

func completeHumanTaskStatus(
	ctx *Context,
	target *humanTaskCompletionTarget,
	input humanTaskCompletionInput,
	deps humanTaskCompleteDeps,
) (*humanTaskCompletionResult, error) {
	stepID := target.stepID
	node, err := findHumanTaskNodeByID(target.status.Nodes, stepID)
	if err != nil {
		return nil, err
	}
	completion, outputsValue, err := prepareHumanTaskCompletion(target.dag, node, input)
	if err != nil {
		return nil, err
	}
	if humanTaskNodeCompleted(node) {
		if !bytes.Equal(node.HumanTaskInput, completion.Canonical) {
			return nil, fmt.Errorf("human task step %q was already completed with different input", stepID)
		}
		return claimHumanTaskResume(ctx, target, true)
	}
	if target.status.Status != core.Waiting {
		return nil, fmt.Errorf("DAG-run %s is not waiting (status: %s)", target.ref, target.status.Status)
	}

	completedAt := deps.now().UTC().Format(time.RFC3339)
	waitingFinishedAt := target.status.FinishedAt
	resumeRequested := false
	var concurrentlyCompletedStatus *exec.DAGRunStatus
	updated, swapped, err := ctx.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.ref,
		target.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			latestNode, err := findHumanTaskNodeByID(latest.Nodes, stepID)
			if err != nil {
				return err
			}
			if humanTaskNodeCompleted(latestNode) {
				if !bytes.Equal(latestNode.HumanTaskInput, completion.Canonical) {
					return fmt.Errorf("human task step %q was already completed with different input", stepID)
				}
				concurrentlyCompletedStatus = latest
				return errHumanTaskCompletionAlreadyApplied
			}
			if latestNode.Status != core.NodeWaiting {
				return fmt.Errorf("human task step %q is not waiting (status: %s)", stepID, latestNode.Status)
			}

			latestNode.HumanTaskInput = append(json.RawMessage(nil), completion.Canonical...)
			if outputsValue == "" {
				latestNode.StepOutputsValue = nil
			} else {
				latestNode.StepOutputsValue = &outputsValue
			}
			latestNode.FinishedAt = completedAt
			latestNode.Status = core.NodeSucceeded
			if !hasWaitingNodes(latest.Nodes) && latest.FinishedAt != "" {
				latest.FinishedAt = ""
				resumeRequested = true
			}
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(target.status.AttemptKey),
	)
	if errors.Is(err, errHumanTaskCompletionAlreadyApplied) {
		return claimHumanTaskResume(ctx, &humanTaskCompletionTarget{
			dag:    target.dag,
			status: concurrentlyCompletedStatus,
			ref:    target.ref,
			stepID: target.stepID,
		}, true)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to complete human task: %w", err)
	}
	if !swapped {
		return resolveHumanTaskCompletionConflict(ctx, target, updated, completion.Canonical)
	}
	return &humanTaskCompletionResult{
		status:            updated,
		resumeRequested:   resumeRequested,
		waitingFinishedAt: waitingFinishedAt,
	}, nil
}

func claimHumanTaskResume(
	ctx *Context,
	target *humanTaskCompletionTarget,
	alreadyCompleted bool,
) (*humanTaskCompletionResult, error) {
	if target.status == nil || target.status.Status != core.Waiting ||
		target.status.FinishedAt == "" || hasWaitingNodes(target.status.Nodes) {
		return &humanTaskCompletionResult{status: target.status, alreadyCompleted: alreadyCompleted}, nil
	}
	waitingFinishedAt := target.status.FinishedAt
	claimed := false
	updated, swapped, err := ctx.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.ref,
		target.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			if hasWaitingNodes(latest.Nodes) {
				return fmt.Errorf("DAG-run changed while resuming human task %q", target.stepID)
			}
			if latest.FinishedAt != "" {
				latest.FinishedAt = ""
				claimed = true
			}
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(target.status.AttemptKey),
	)
	if err != nil {
		return nil, err
	}
	return &humanTaskCompletionResult{
		status:            updated,
		alreadyCompleted:  alreadyCompleted,
		resumeRequested:   swapped && claimed,
		waitingFinishedAt: waitingFinishedAt,
	}, nil
}

func rollbackHumanTaskResumeClaim(
	ctx *Context,
	target *humanTaskCompletionTarget,
	result *humanTaskCompletionResult,
) {
	_, _, err := ctx.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.ref,
		result.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			if latest.FinishedAt == "" {
				latest.FinishedAt = result.waitingFinishedAt
			}
			return nil
		},
		exec.WithCompareAndSwapExpectedAttemptKey(result.status.AttemptKey),
	)
	if err != nil {
		_, _ = fmt.Fprintf(
			ctx.Command.ErrOrStderr(),
			"warning: failed to roll back resume claim for human task %q in DAG-run %q: %v\n",
			target.stepID,
			target.ref.ID,
			err,
		)
	}
}

func resolveHumanTaskCompletionConflict(
	ctx *Context,
	target *humanTaskCompletionTarget,
	updated *exec.DAGRunStatus,
	canonicalInput json.RawMessage,
) (*humanTaskCompletionResult, error) {
	stepID := target.stepID
	if updated != nil {
		latestNode, findErr := findHumanTaskNodeByID(updated.Nodes, stepID)
		if findErr == nil && humanTaskNodeCompleted(latestNode) {
			if bytes.Equal(latestNode.HumanTaskInput, canonicalInput) {
				latestTarget := *target
				latestTarget.status = updated
				return claimHumanTaskResume(ctx, &latestTarget, true)
			}
			return nil, fmt.Errorf("human task step %q was already completed with different input", stepID)
		}
	}
	return nil, fmt.Errorf("DAG-run changed while completing human task %q; inspect its current status and retry", stepID)
}

func parseHumanTaskCompletionInput(command *cobra.Command) (humanTaskCompletionInput, error) {
	pairs, err := command.Flags().GetStringArray(humanTaskFlagInput)
	if err != nil {
		return humanTaskCompletionInput{}, fmt.Errorf("failed to read --%s: %w", humanTaskFlagInput, err)
	}
	rawJSON, err := command.Flags().GetString(humanTaskFlagInputsJSON)
	if err != nil {
		return humanTaskCompletionInput{}, fmt.Errorf("failed to read --%s: %w", humanTaskFlagInputsJSON, err)
	}
	if len(pairs) > 0 && command.Flags().Changed(humanTaskFlagInputsJSON) {
		return humanTaskCompletionInput{}, fmt.Errorf("--%s and --%s cannot be used together", humanTaskFlagInput, humanTaskFlagInputsJSON)
	}

	if command.Flags().Changed(humanTaskFlagInputsJSON) {
		return parseHumanTaskJSONInput(rawJSON)
	}
	return parseHumanTaskInputPairs(pairs)
}

func parseHumanTaskJSONInput(raw string) (humanTaskCompletionInput, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	decoded, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return humanTaskCompletionInput{}, fmt.Errorf("invalid --%s JSON value: %w", humanTaskFlagInputsJSON, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return humanTaskCompletionInput{}, fmt.Errorf("invalid --%s JSON value: %w", humanTaskFlagInputsJSON, err)
	}
	values, ok := decoded.(map[string]any)
	if !ok || values == nil {
		return humanTaskCompletionInput{}, fmt.Errorf("--%s must be a JSON object", humanTaskFlagInputsJSON)
	}
	return humanTaskCompletionInput{values: values}, nil
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object member name must be a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON member %q", key)
			}
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		var array []any
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func parseHumanTaskInputPairs(pairs []string) (humanTaskCompletionInput, error) {
	values := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return humanTaskCompletionInput{}, fmt.Errorf("--%s must use key=value form", humanTaskFlagInput)
		}
		if _, exists := values[name]; exists {
			return humanTaskCompletionInput{}, fmt.Errorf("--%s contains duplicate key %q", humanTaskFlagInput, name)
		}
		values[name] = value
	}
	return humanTaskCompletionInput{values: values, coerceStrings: len(pairs) > 0}, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("must contain exactly one JSON object")
		}
		return err
	}
	return nil
}

func findHumanTaskNodeByID(nodes []*exec.Node, stepID string) (*exec.Node, error) {
	var found *exec.Node
	for _, node := range nodes {
		if node == nil || node.Step.ID != stepID {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("human task step ID %q is ambiguous", stepID)
		}
		found = node
	}
	if found == nil {
		return nil, fmt.Errorf("human task step ID %q was not found", stepID)
	}
	if found.Step.HumanTask == nil {
		return nil, fmt.Errorf("step %q is not a human task", stepID)
	}
	return found, nil
}

func prepareHumanTaskCompletion(
	dag *core.DAG,
	node *exec.Node,
	input humanTaskCompletionInput,
) (*spec.HumanTaskInputResult, string, error) {
	result, err := spec.ValidateHumanTaskInputs(node.Step.HumanTask.Form, input.values, input.coerceStrings)
	if err != nil {
		return nil, "", fmt.Errorf("invalid input for human task step %q: %w", node.Step.ID, err)
	}
	outputs, err := marshalHumanTaskCompletionOutputs(dag, result)
	if err != nil {
		return nil, "", fmt.Errorf("human task step %q: %w", node.Step.ID, err)
	}
	return result, outputs, nil
}

func marshalHumanTaskCompletionOutputs(dag *core.DAG, result *spec.HumanTaskInputResult) (string, error) {
	maxSize := dag.MaxOutputSize
	if maxSize <= 0 {
		normalized := dag.Clone()
		core.InitializeDefaults(normalized)
		maxSize = normalized.MaxOutputSize
	}
	if len(result.Canonical) > maxSize {
		return "", fmt.Errorf("human task input exceeded maximum size limit of %d bytes", maxSize)
	}
	if len(result.Outputs) == 0 {
		return "", nil
	}
	outputsData, err := json.Marshal(result.Outputs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal human task outputs: %w", err)
	}
	if len(outputsData) > maxSize {
		return "", fmt.Errorf("human task step outputs exceeded maximum size limit of %d bytes", maxSize)
	}
	return string(outputsData), nil
}

func humanTaskNodeCompleted(node *exec.Node) bool {
	return node != nil && len(node.HumanTaskInput) > 0
}

func hasWaitingNodes(nodes []*exec.Node) bool {
	for _, node := range nodes {
		if node != nil && node.Status == core.NodeWaiting {
			return true
		}
	}
	return false
}

func waitForNextHumanTaskPoll(ctx *Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(humanTaskSettlePollInterval):
		return nil
	}
}

func waitForHumanTaskCompletionReady(
	ctx *Context,
	attempt exec.DAGRunAttempt,
	dag *core.DAG,
	status *exec.DAGRunStatus,
	stepID string,
) (*exec.DAGRunStatus, error) {
	if status.Status != core.Waiting || status.AttemptID == "" {
		return status, nil
	}
	if exec.IsRemoteWorkerID(status.WorkerID) {
		return waitForRemoteHumanTaskAttempt(ctx, attempt, status, stepID)
	}
	if ctx.ProcStore == nil {
		return status, nil
	}

	deadline := time.Now().Add(humanTaskSettleTimeout)
	for {
		alive, err := ctx.ProcStore.IsAttemptAlive(ctx, dag.ProcGroup(), status.DAGRun(), status.AttemptID)
		if err != nil {
			return nil, fmt.Errorf("failed to check whether DAG-run attempt is still finalizing: %w", err)
		}
		if !alive {
			break
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("DAG-run attempt %s is still finalizing; retry human-task completion", status.AttemptID)
		}
		if err := waitForNextHumanTaskPoll(ctx); err != nil {
			return nil, err
		}
	}

	latest, err := reloadHumanTaskStatus(ctx, attempt)
	if err != nil {
		return nil, err
	}
	return waitForHumanTaskAttemptFinalization(ctx, attempt, status.AttemptID, latest, stepID)
}

func waitForRemoteHumanTaskAttempt(
	ctx *Context,
	attempt exec.DAGRunAttempt,
	status *exec.DAGRunStatus,
	stepID string,
) (*exec.DAGRunStatus, error) {
	return waitForHumanTaskAttemptFinalization(ctx, attempt, status.AttemptID, status, stepID)
}

func waitForHumanTaskAttemptFinalization(
	ctx *Context,
	attempt exec.DAGRunAttempt,
	attemptID string,
	latest *exec.DAGRunStatus,
	stepID string,
) (*exec.DAGRunStatus, error) {
	deadline := time.Now().Add(humanTaskSettleTimeout)
	for {
		finalizing, err := humanTaskAttemptIsFinalizing(latest, attemptID, stepID)
		if err != nil {
			return nil, err
		}
		if !finalizing {
			return latest, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("DAG-run attempt %s is still finalizing; retry human-task completion", attemptID)
		}
		if err := waitForNextHumanTaskPoll(ctx); err != nil {
			return nil, err
		}
		latest, err = reloadHumanTaskStatus(ctx, attempt)
		if err != nil {
			return nil, err
		}
	}
}

func humanTaskAttemptIsFinalizing(status *exec.DAGRunStatus, attemptID, stepID string) (bool, error) {
	if status.Status != core.Waiting || status.AttemptID != attemptID || status.FinishedAt != "" {
		return false, nil
	}
	node, err := findHumanTaskNodeByID(status.Nodes, stepID)
	if err != nil {
		return false, err
	}
	return !humanTaskNodeCompleted(node), nil
}

func reloadHumanTaskStatus(ctx *Context, attempt exec.DAGRunAttempt) (*exec.DAGRunStatus, error) {
	latest, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to reload DAG-run status after waiting for the attempt to settle: %w", err)
	}
	if latest == nil {
		return nil, fmt.Errorf("failed to reload DAG-run status after waiting for the attempt to settle: status data is nil")
	}
	return latest, nil
}

func resumeHumanTaskRun(ctx *Context, dag *core.DAG, status *exec.DAGRunStatus) error {
	if exec.IsRemoteWorkerID(status.WorkerID) {
		if ctx.QueueStore == nil {
			return fmt.Errorf("queue store is not configured")
		}
		if err := waitForRemoteHumanTaskDispatch(ctx, dag, status); err != nil {
			return err
		}
		if err := exec.EnqueueRetry(ctx.Context, ctx.DAGRunStore, ctx.QueueStore, dag, status, exec.EnqueueRetryOptions{}); err != nil {
			return fmt.Errorf("enqueue distributed retry: %w", err)
		}
		return nil
	}
	return launchHumanTaskRetry(ctx, dag, status)
}

func waitForRemoteHumanTaskDispatch(ctx *Context, dag *core.DAG, status *exec.DAGRunStatus) error {
	deadline := time.Now().Add(humanTaskSettleTimeout)
	queueName := status.ProcGroup
	if queueName == "" {
		queueName = dag.ProcGroup()
	}
	for {
		items, err := ctx.QueueStore.ListByDAGName(ctx, queueName, status.Name)
		if err != nil {
			return fmt.Errorf("check previous distributed dispatch: %w", err)
		}
		pending := false
		for _, item := range items {
			ref, err := item.Data()
			if err != nil {
				return fmt.Errorf("read previous distributed dispatch: %w", err)
			}
			if ref != nil && *ref == status.DAGRun() {
				pending = true
				break
			}
		}
		if !pending {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("previous distributed dispatch is still finalizing")
		}
		if err := waitForNextHumanTaskPoll(ctx); err != nil {
			return err
		}
	}
}

func launchHumanTaskRetry(
	ctx *Context,
	dag *core.DAG,
	status *exec.DAGRunStatus,
) error {
	if ctx.Config == nil {
		return fmt.Errorf("configuration is not available")
	}
	result, err := spec.ResolveEnvWithWarnings(ctx, dag, status.ParamsList, spec.ResolveEnvOptions{
		BaseConfig: ctx.Config.Paths.BaseConfig,
	})
	if err != nil {
		return fmt.Errorf("prepare retry environment: %w", err)
	}
	dagwarning.Log(ctx, result.BuildWarnings)
	prepared := dag.Clone()
	prepared.Env = result.Env

	retrySpec := humanTaskRetrySpec(ctx, prepared, status.DAGRunID)
	if err := launcher.Start(ctx, retrySpec); err != nil {
		return fmt.Errorf("start retry subprocess: %w", err)
	}
	return nil
}

func humanTaskRetrySpec(ctx *Context, dag *core.DAG, dagRunID string) launcher.CmdSpec {
	builder := launcher.NewSubCmdBuilder(ctx.Config)
	retrySpec := builder.Retry(dag, dagRunID, "")
	if daguHome := explicitHumanTaskDAGUHome(ctx); daguHome != "" {
		target := retrySpec.Args[len(retrySpec.Args)-1]
		retrySpec.Args = append(retrySpec.Args[:len(retrySpec.Args)-1], "--dagu-home="+daguHome, target)
	}
	return retrySpec
}

func explicitHumanTaskDAGUHome(ctx *Context) string {
	if ctx == nil || ctx.Command == nil || !ctx.Command.Flags().Changed(daguHomeFlag.name) {
		return ""
	}
	if ctx.Config != nil {
		for _, entry := range ctx.Config.Core.BaseEnv.AsSlice() {
			key, value, found := strings.Cut(entry, "=")
			if found && key == "DAGU_HOME" {
				return value
			}
		}
	}
	value, err := ctx.Command.Flags().GetString(daguHomeFlag.name)
	if err != nil {
		return ""
	}
	return fileutil.ResolvePathOrBlank(value)
}
