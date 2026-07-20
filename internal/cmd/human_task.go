// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
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
	humanTaskSubRunIDFlag = commandLineFlag{
		name:      "sub-run-id",
		shorthand: "s",
		usage:     "Sub DAG-run ID containing the human task",
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
		humanTaskSubRunIDFlag,
		humanTaskStepFlag,
		humanTaskInputsJSONFlag,
	}, runHumanTaskComplete)
	command.Flags().StringArray(humanTaskFlagInput, nil, "Human task input in key=value form; repeatable")
	command.MarkFlagsMutuallyExclusive(humanTaskFlagInput, humanTaskFlagInputsJSON)
	return command
}

type humanTaskCompletionInput struct {
	values        map[string]any
	coerceStrings bool
}

type humanTaskCompleteDeps struct {
	now    func() time.Time
	actor  func() string
	launch func(*Context, *core.DAG, *exec.DAGRunStatus, exec.DAGRunRef, exec.DAGRunRef) error
}

func defaultHumanTaskCompleteDeps() humanTaskCompleteDeps {
	return humanTaskCompleteDeps{
		now:    time.Now,
		actor:  currentHumanTaskActor,
		launch: launchHumanTaskRetry,
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
	if result.alreadyCompleted {
		return reportHumanTaskAlreadyCompleted(
			ctx,
			target.dag.Name,
			result.status,
			request.stepID,
			target.rootRef,
			target.owningRef,
		)
	}
	if hasWaitingNodes(result.status.Nodes) {
		_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Completed human task %s; DAG-run remains waiting.\n", request.stepID)
		return err
	}
	if err := deps.launch(ctx, target.dag, result.status, target.rootRef, target.owningRef); err != nil {
		return fmt.Errorf(
			"human task %q was completed, but the DAG-run could not be resumed: %w; resume it with `%s`",
			request.stepID,
			err,
			humanTaskRetryCommand(ctx, target.dag.Name, target.rootRef, target.owningRef),
		)
	}
	_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "Completed human task %s; DAG-run resume started.\n", request.stepID)
	return err
}

type humanTaskCompletionRequest struct {
	dagRunID string
	subRunID string
	stepID   string
}

type humanTaskCompletionTarget struct {
	dag       *core.DAG
	status    *exec.DAGRunStatus
	rootRef   exec.DAGRunRef
	owningRef exec.DAGRunRef
	request   humanTaskCompletionRequest
}

type humanTaskCompletionResult struct {
	status           *exec.DAGRunStatus
	alreadyCompleted bool
}

func parseHumanTaskCompletionRequest(ctx *Context) (humanTaskCompletionRequest, error) {
	dagRunID, err := ctx.StringParam(humanTaskRunIDFlag.name)
	if err != nil {
		return humanTaskCompletionRequest{}, err
	}
	subRunID, err := ctx.StringParam(humanTaskSubRunIDFlag.name)
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
		subRunID: subRunID,
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
	rootRef := exec.NewDAGRunRef(dagName, request.dagRunID)
	attempt, err := extractAttemptForStatus(ctx, dagName, request.dagRunID, request.subRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to find DAG-run: %w", err)
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
	status, err = waitForHumanTaskCompletionReady(ctx, attempt, dag, status)
	if err != nil {
		return nil, err
	}

	owningRef := exec.NewDAGRunRef(status.Name, status.DAGRunID)
	if owningRef.Name == "" || owningRef.ID == "" {
		return nil, fmt.Errorf("stored DAG-run identity is incomplete")
	}
	return &humanTaskCompletionTarget{
		dag:       dag,
		status:    status,
		rootRef:   rootRef,
		owningRef: owningRef,
		request:   request,
	}, nil
}

func completeHumanTaskStatus(
	ctx *Context,
	target *humanTaskCompletionTarget,
	input humanTaskCompletionInput,
	deps humanTaskCompleteDeps,
) (*humanTaskCompletionResult, error) {
	stepID := target.request.stepID
	node, err := findHumanTaskNodeByID(target.status.Nodes, stepID)
	if err != nil {
		return nil, err
	}
	validated, err := validateHumanTaskCompletion(node, input)
	if err != nil {
		return nil, err
	}
	if _, err := marshalHumanTaskCompletionOutputs(target.dag, validated); err != nil {
		return nil, err
	}
	if humanTaskNodeCompleted(node) {
		if !bytes.Equal(node.HumanTaskInput, validated.Canonical) {
			return nil, fmt.Errorf("human task step %q was already completed with different input", stepID)
		}
		return &humanTaskCompletionResult{status: target.status, alreadyCompleted: true}, nil
	}
	if target.status.Status != core.Waiting {
		return nil, fmt.Errorf("DAG-run %s is not waiting (status: %s)", target.owningRef, target.status.Status)
	}

	completedAt := deps.now().UTC().Format(time.RFC3339)
	completedBy := deps.actor()
	var concurrentlyCompletedStatus *exec.DAGRunStatus
	casOptions := []exec.CompareAndSwapStatusOption{
		exec.WithCompareAndSwapRootDAGRun(target.rootRef),
		exec.WithCompareAndSwapExpectedAttemptKey(target.status.AttemptKey),
	}
	if target.request.subRunID != "" {
		casOptions = append(casOptions, exec.WithCompareAndSwapExpectedRootStatus(core.Waiting))
	}
	updated, swapped, err := ctx.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		target.owningRef,
		target.status.AttemptID,
		core.Waiting,
		func(latest *exec.DAGRunStatus) error {
			latestNode, err := findHumanTaskNodeByID(latest.Nodes, stepID)
			if err != nil {
				return err
			}
			latestValidated, err := validateHumanTaskCompletion(latestNode, input)
			if err != nil {
				return err
			}
			if humanTaskNodeCompleted(latestNode) {
				if !bytes.Equal(latestNode.HumanTaskInput, latestValidated.Canonical) {
					return fmt.Errorf("human task step %q was already completed with different input", stepID)
				}
				concurrentlyCompletedStatus = latest
				return errHumanTaskCompletionAlreadyApplied
			}
			if latestNode.Status != core.NodeWaiting {
				return fmt.Errorf("human task step %q is not waiting (status: %s)", stepID, latestNode.Status)
			}

			outputsValue, err := marshalHumanTaskCompletionOutputs(target.dag, latestValidated)
			if err != nil {
				return err
			}
			latestNode.HumanTaskInput = append(json.RawMessage(nil), latestValidated.Canonical...)
			latestNode.HumanTaskCompletedAt = completedAt
			latestNode.HumanTaskCompletedBy = completedBy
			if outputsValue == "" {
				latestNode.StepOutputsValue = nil
			} else {
				latestNode.StepOutputsValue = &outputsValue
			}
			latestNode.FinishedAt = completedAt
			latestNode.Status = core.NodeSucceeded
			return nil
		},
		casOptions...,
	)
	if errors.Is(err, errHumanTaskCompletionAlreadyApplied) {
		return &humanTaskCompletionResult{status: concurrentlyCompletedStatus, alreadyCompleted: true}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to complete human task: %w", err)
	}
	if !swapped {
		return resolveHumanTaskCompletionConflict(updated, stepID, input)
	}
	return &humanTaskCompletionResult{status: updated}, nil
}

func resolveHumanTaskCompletionConflict(
	updated *exec.DAGRunStatus,
	stepID string,
	input humanTaskCompletionInput,
) (*humanTaskCompletionResult, error) {
	if updated != nil {
		latestNode, findErr := findHumanTaskNodeByID(updated.Nodes, stepID)
		if findErr == nil && humanTaskNodeCompleted(latestNode) {
			latestValidated, validateErr := validateHumanTaskCompletion(latestNode, input)
			if validateErr == nil && bytes.Equal(latestNode.HumanTaskInput, latestValidated.Canonical) {
				return &humanTaskCompletionResult{status: updated, alreadyCompleted: true}, nil
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
		decoder := json.NewDecoder(strings.NewReader(rawJSON))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return humanTaskCompletionInput{}, fmt.Errorf("invalid --%s value: %w", humanTaskFlagInputsJSON, err)
		}
		if err := requireJSONEOF(decoder); err != nil {
			return humanTaskCompletionInput{}, fmt.Errorf("invalid --%s value: %w", humanTaskFlagInputsJSON, err)
		}
		values, ok := decoded.(map[string]any)
		if !ok || values == nil {
			return humanTaskCompletionInput{}, fmt.Errorf("--%s must be a JSON object", humanTaskFlagInputsJSON)
		}
		return humanTaskCompletionInput{values: values}, nil
	}

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

func validateHumanTaskCompletion(node *exec.Node, input humanTaskCompletionInput) (*spec.HumanTaskInputResult, error) {
	result, err := spec.ValidateHumanTaskInputs(node.Step.HumanTask.Form, input.values, input.coerceStrings)
	if err != nil {
		return nil, fmt.Errorf("invalid input for human task step %q: %w", node.Step.ID, err)
	}
	return result, nil
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

func reportHumanTaskAlreadyCompleted(
	ctx *Context,
	dagName string,
	status *exec.DAGRunStatus,
	stepID string,
	rootRef exec.DAGRunRef,
	owningRef exec.DAGRunRef,
) error {
	if status != nil && status.Status == core.Waiting && !hasWaitingNodes(status.Nodes) {
		_, err := fmt.Fprintf(
			ctx.Command.OutOrStdout(),
			"Human task %s was already completed, but the DAG-run is still waiting; resume it with `%s`.\n",
			stepID,
			humanTaskRetryCommand(ctx, dagName, rootRef, owningRef),
		)
		return err
	}
	_, err := fmt.Fprintf(ctx.Command.OutOrStdout(), "Human task %s was already completed.\n", stepID)
	return err
}

func waitForHumanTaskCompletionReady(
	ctx *Context,
	attempt exec.DAGRunAttempt,
	dag *core.DAG,
	status *exec.DAGRunStatus,
) (*exec.DAGRunStatus, error) {
	if ctx.ProcStore == nil || status.Status != core.Waiting || !isLocalHumanTaskWorker(status.WorkerID) || status.AttemptID == "" {
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
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(humanTaskSettlePollInterval):
		}
	}

	latest, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to reload DAG-run status after waiting for the attempt to settle: %w", err)
	}
	if latest == nil {
		return nil, fmt.Errorf("failed to reload DAG-run status after waiting for the attempt to settle: status data is nil")
	}
	return latest, nil
}

func isLocalHumanTaskWorker(workerID string) bool {
	return workerID == "" || workerID == "local"
}

func launchHumanTaskRetry(
	ctx *Context,
	dag *core.DAG,
	status *exec.DAGRunStatus,
	rootRef exec.DAGRunRef,
	owningRef exec.DAGRunRef,
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

	retrySpec := humanTaskRetrySpec(ctx, prepared, rootRef, owningRef)
	if err := launcher.Start(ctx, retrySpec); err != nil {
		return fmt.Errorf("start retry subprocess: %w", err)
	}
	return nil
}

func humanTaskRetrySpec(ctx *Context, dag *core.DAG, rootRef, owningRef exec.DAGRunRef) launcher.CmdSpec {
	selectors := newHumanTaskRetrySelectors(ctx, rootRef, owningRef)
	builder := launcher.NewSubCmdBuilder(ctx.Config)
	retrySpec := builder.Retry(dag, selectors.runID, "")
	if !selectors.rootRef.Zero() {
		retrySpec = builder.RetryWithRootDAGRun(dag, selectors.runID, "", selectors.rootRef)
	}
	if selectors.daguHome != "" {
		target := retrySpec.Args[len(retrySpec.Args)-1]
		retrySpec.Args = append(retrySpec.Args[:len(retrySpec.Args)-1], "--dagu-home="+selectors.daguHome, target)
	}
	return retrySpec
}

func humanTaskRetryCommand(ctx *Context, dagName string, rootRef, owningRef exec.DAGRunRef) string {
	selectors := newHumanTaskRetrySelectors(ctx, rootRef, owningRef)
	parts := []string{"dagu", "retry", "--run-id=" + selectors.runID}
	if !selectors.rootRef.Zero() {
		parts = append(parts, "--root="+selectors.rootRef.String())
	}
	if ctx != nil && ctx.Config != nil && ctx.Config.Paths.ConfigFileUsed != "" {
		parts = append(parts, "--config", cmdutil.ShellQuote(ctx.Config.Paths.ConfigFileUsed))
	}
	if selectors.daguHome != "" {
		parts = append(parts, "--dagu-home", cmdutil.ShellQuote(selectors.daguHome))
	}
	parts = append(parts, cmdutil.ShellQuote(dagName))
	return strings.Join(parts, " ")
}

type humanTaskRetrySelectors struct {
	runID    string
	rootRef  exec.DAGRunRef
	daguHome string
}

func newHumanTaskRetrySelectors(ctx *Context, rootRef, owningRef exec.DAGRunRef) humanTaskRetrySelectors {
	selectors := humanTaskRetrySelectors{
		runID:    owningRef.ID,
		daguHome: explicitHumanTaskDAGUHome(ctx),
	}
	if owningRef != rootRef {
		selectors.rootRef = rootRef
	}
	return selectors
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

func currentHumanTaskActor() string {
	current, err := user.Current()
	if err == nil && current != nil {
		if username := strings.TrimSpace(current.Username); username != "" {
			return username
		}
		if name := strings.TrimSpace(current.Name); name != "" {
			return name
		}
		if uid := strings.TrimSpace(current.Uid); uid != "" {
			return "uid:" + uid
		}
	}
	if username := strings.TrimSpace(os.Getenv("USER")); username != "" {
		return username
	}
	if username := strings.TrimSpace(os.Getenv("USERNAME")); username != "" {
		return username
	}
	return "local"
}
