// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Data is a thread-safe wrapper around NodeData.
type Data struct {
	mu    sync.RWMutex
	inner NodeData
}

// NodeData represents the data of a node.
type NodeData struct {
	Step  core.Step
	State NodeState
}

type NodeState struct {
	// Status represents the state of the node.
	Status core.NodeStatus
	// Stdout is the log file path from the node.
	Stdout string
	// Stderr is the log file path for the error log (stderr).
	Stderr string
	// StartedAt is the time when the node started.
	StartedAt time.Time
	// FinishedAt is the time when the node finished.
	FinishedAt time.Time
	// RetryCount is the number of retries happened based on the retry policy.
	RetryCount int
	// RetriedAt is the time when the node was retried last time.
	RetriedAt time.Time
	// DoneCount is the number of times the node was executed.
	DoneCount int
	// Repeated is true if the node is a repeated step.
	// This is used to generate unique run IDs for repeated steps in case the node
	// runs nested DAGs.
	Repeated bool
	// SkippedByRetry marks a node that was intentionally skipped by an edited
	// retry while preserving its output variables for downstream steps.
	SkippedByRetry bool
	// Error is the error that the executor encountered.
	Error error
	// ExitCode is the exit code that the command exited with.
	// It only makes sense when the node is a command executor.
	ExitCode int
	// Parallel contains the evaluated parallel execution state for the node.
	// This is populated when a step has parallel configuration and tracks
	// all the items that need to be executed in parallel.
	*Parallel
	// SubRuns stores the sub dag-runs.
	SubRuns []SubDAGRun
	// SubRunsRepeated stores the repeated sub dag-runs.
	SubRunsRepeated []SubDAGRun
	// OutputVariables stores the output variables for the following steps.
	// It only contains the local output variables.
	OutputVariables *collections.SyncMap
	// OutputValue stores the step-scoped output payload for ${step.output} references.
	// String-form output stores captured stdout; object-form output stores compact JSON.
	OutputValue *string
	// ChatMessages stores the chat session messages for message passing between steps.
	ChatMessages []exec.LLMMessage
	// ToolDefinitions stores the tool definitions that were available to the LLM during execution.
	// This provides visibility into what tools/functions the LLM could call.
	ToolDefinitions []exec.ToolDefinition
	// ApprovalInputs stores key-value parameters provided during approval.
	// These are available as environment variables in subsequent steps.
	ApprovalInputs map[string]string
	// ApprovedAt is the time when the step was approved.
	ApprovedAt string
	// ApprovedBy is the username of the user who approved the step.
	ApprovedBy string
	// RejectedAt is the time when the step was rejected.
	RejectedAt string
	// RejectedBy is the username of the user who rejected the step.
	RejectedBy string
	// RejectionReason stores the optional reason for rejection.
	RejectionReason string
	// ApprovalIteration tracks how many times this step has been pushed back.
	ApprovalIteration int
	// PushBackInputs stores inputs from the last push-back for env var injection.
	PushBackInputs map[string]string
	// PushBackHistory stores the chronological push-back feedback for this step.
	PushBackHistory []exec.PushBackEntry
	// PushBackPreviousStdout stores the stdout log path from the execution that
	// was reset by the latest push-back.
	PushBackPreviousStdout string
}

// Parallel represents the evaluated parallel execution configuration for a node.
// It contains the expanded list of items to be processed in parallel.
type Parallel struct {
	// Items contains all the parallel items to be executed.
	// Each item will result in a separate sub DAG run.
	Items []ParallelItem
}

// ParallelItem represents a single item in a parallel execution.
// It combines the item data with a unique identifier for tracking.
type ParallelItem struct {
	// Item contains the actual data for this parallel execution.
	// It can be either a simple value or a map of parameters from core.ParallelItem.
	Item core.ParallelItem
}

// SubDAGRun represents a sub DAG execution within a parent DAG.
// Each sub DAG run has a deterministic ID based on its parameters to ensure idempotency.
type SubDAGRun struct {
	// DAGRunID is the unique identifier for the sub dag-run.
	// It is generated as a base58-encoded SHA-256 hash of the string:
	// "<parent-dag-run-id>:<step-name>:<deterministic-json-params>"
	//
	// This deterministic ID generation ensures:
	// - Same parameters always produce the same sub DAG run ID
	// - Retries reuse existing sub DAG runs instead of creating duplicates
	// - Each step's children are namespaced by step name to prevent collisions
	//
	// The params are encoded as deterministic JSON (sorted keys) before hashing.
	// Example input: "abc123:process-regions:{"REGION":"us-east-1","VERSION":"1.0.0"}"
	// Example output: "5Kd3NBUAdUnhyzenEwVLy9pBKxSwXvE9FMPyR4UKZvpe"
	DAGRunID string
	// Params contains the raw parameters passed to the sub DAG run.
	// This can be:
	// - A simple string: "param1 param2"
	// - Key-value pairs: "KEY1=value1 KEY2=value2"
	// - Raw JSON: '{"region": "us-east-1", "config": {"timeout": 30}}'
	// The exact format depends on how the DAG expects to receive parameters.
	Params string
	// DAGName is the name of the executed sub-DAG.
	// For chat tool calls, this is the tool DAG name.
	// This field enables UI drill-down when step.call is not set.
	DAGName string
}

func newSafeData(data NodeData) Data {
	return Data{inner: data}
}

func (d *Data) ResetError() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.Error = nil
	d.inner.State.ExitCode = 0
}

func (d *Data) SetExecutorConfig(cfg core.ExecutorConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.Step.ExecutorConfig = cfg
}

func (d *Data) SetSubDAG(subDAG core.SubDAG) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.Step.SubDAG = &subDAG
}

func (d *Data) Args() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.inner.Step.Commands) == 0 {
		return nil
	}
	args := make([]string, len(d.inner.Step.Commands[0].Args))
	copy(args, d.inner.Step.Commands[0].Args)
	return args
}

func (d *Data) SetArgs(args []string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.inner.Step.Commands) == 0 {
		d.inner.Step.Commands = []core.CommandEntry{{}}
	}
	d.inner.Step.Commands[0].Args = args
}

func (d *Data) Step() core.Step {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.Step
}

func (d *Data) SetScript(script string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.Step.Script = script
}

func (s *Data) SetStep(step core.Step) {
	// TODO: refactor to avoid modifying the step
	s.mu.Lock()
	defer s.mu.Unlock()

	s.inner.Step = step
}

func (s *Data) Data() NodeData {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inner
}

// SetSubRuns replaces the sub DAG runs associated with the node.
func (d *Data) SetSubRuns(subRuns []SubDAGRun) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Clear the existing sub runs and set the new ones.
	d.inner.State.SubRuns = make([]SubDAGRun, len(subRuns))
	copy(d.inner.State.SubRuns, subRuns)
}

// AddSubRunsRepeated appends repeated sub DAG runs to the node.
func (d *Data) AddSubRunsRepeated(subRun ...SubDAGRun) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.SubRunsRepeated = append(d.inner.State.SubRunsRepeated, subRun...)
}

func (d *Data) Setup(ctx context.Context, logFile string, startedAt time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Determine effective log output mode using the DAG and step settings
	rCtx := GetDAGContext(ctx)
	logOutputMode := core.EffectiveLogOutput(rCtx.DAG, &d.inner.Step)

	// Set log file paths based on the log output mode
	if logOutputMode == core.LogOutputMerged {
		// Merged mode: both stdout and stderr go to the same .log file
		d.inner.State.Stdout = logFile + ".log"
		d.inner.State.Stderr = logFile + ".log"
	} else {
		// Separate mode (default): stdout and stderr go to separate files
		d.inner.State.Stdout = logFile + ".out"
		d.inner.State.Stderr = logFile + ".err"
	}
	d.inner.State.StartedAt = startedAt

	// Evaluate the stdout field
	stdout, err := EvalStepString(ctx, d.inner.Step.Stdout, eval.WithoutDollarEscape())
	if err != nil {
		return fmt.Errorf("failed to evaluate stdout field: %w", err)
	}
	d.inner.Step.Stdout = stdout

	// Evaluate the stderr field
	stderr, err := EvalStepString(ctx, d.inner.Step.Stderr, eval.WithoutDollarEscape())
	if err != nil {
		return fmt.Errorf("failed to evaluate stderr field: %w", err)
	}
	d.inner.Step.Stderr = stderr

	return nil
}

func (d *Data) State() NodeState {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State
}

func (d *Data) Status() core.NodeStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.Status
}

func (d *Data) SetStatus(s core.NodeStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.Status = s
}

func (d *Data) StepInfo() eval.StepInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	info := eval.StepInfo{
		Stdout:   d.inner.State.Stdout,
		Stderr:   d.inner.State.Stderr,
		ExitCode: strconv.Itoa(d.inner.State.ExitCode),
	}

	// Step-scoped references use OutputValue for both string-form and object-form output.
	if d.inner.State.OutputValue != nil {
		value := *d.inner.State.OutputValue
		info.Output = &value
		return info
	}

	// Backward-compatible fallback for previously persisted string-form outputs.
	if value, ok := d.inner.StringFormOutputValue(); ok {
		info.Output = &value
	}

	return info
}

// StringFormOutputValue returns the canonical captured output for string-form output: NAME steps.
// OutputValue is the source of truth for newly executed steps; OutputVariables remains as a
// backward-compatible fallback for previously persisted state.
func (d NodeData) StringFormOutputValue() (string, bool) {
	if d.Step.Output == "" {
		return "", false
	}
	if d.State.OutputValue != nil {
		return *d.State.OutputValue, true
	}
	return legacyOutputVariableValue(d.Step.Output, d.State.OutputVariables)
}

func legacyOutputVariableValue(outputKey string, vars *collections.SyncMap) (string, bool) {
	if outputKey == "" || vars == nil {
		return "", false
	}

	raw, ok := vars.Load(outputKey)
	if !ok {
		return "", false
	}

	strVal, ok := raw.(string)
	if !ok {
		return "", false
	}

	if _, value, found := strings.Cut(strVal, "="); found {
		return value, true
	}

	return "", false
}

func (d *Data) ContinueOn() core.ContinueOn {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.Step.ContinueOn
}

func (d *Data) GetStdout() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.Stdout
}

func (d *Data) GetStderr() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.Stderr
}

func (d *Data) SignalOnStop() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.Step.SignalOnStop
}

func (d *Data) Name() string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.Step.Name
}

func (d *Data) Error() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.Error
}

func (d *Data) SetError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.Error = err
}

func (d *Data) ClearVariable(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.inner.State.OutputVariables == nil {
		return
	}

	d.inner.State.OutputVariables.Delete(key)
}

func (d *Data) MatchExitCode(exitCodes []int) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return slices.Contains(exitCodes, d.inner.State.ExitCode)
}

func (d *Data) getVariable(key string) (stringutil.KeyValue, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.inner.State.OutputVariables == nil {
		return "", false
	}

	v, ok := d.inner.State.OutputVariables.Load(key)
	if !ok {
		return "", false
	}

	return stringutil.KeyValue(v.(string)), true
}

func (d *Data) getBoolVariable(key string) (bool, bool) {
	v, ok := d.getVariable(key)
	if !ok {
		return false, false
	}

	return v.Bool(), true
}

func (d *Data) setBoolVariable(key string, value bool) {

	if d.inner.State.OutputVariables == nil {
		d.mu.Lock()
		d.inner.State.OutputVariables = &collections.SyncMap{}
		d.mu.Unlock()
	}
	d.inner.State.OutputVariables.Store(key, stringutil.NewKeyValue(key, strconv.FormatBool(value)).String())
}

func (d *Data) setVariable(key, value string) {
	if d.inner.State.OutputVariables == nil {
		d.mu.Lock()
		d.inner.State.OutputVariables = &collections.SyncMap{}
		d.mu.Unlock()
	}
	d.inner.State.OutputVariables.Store(key, stringutil.NewKeyValue(key, value).String())
}

func (d *Data) setOutputValue(value string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	v := value
	d.inner.State.OutputValue = &v
}

func (d *Data) Finish() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.FinishedAt = time.Now()
}

func (d *Data) IncRetryCount() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.RetryCount++
}

func (d *Data) GetRetryCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.RetryCount
}

func (d *Data) SetRetryCount(retryCount int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.RetryCount = retryCount
}

func (d *Data) SetRetriedAt(retriedAt time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.RetriedAt = retriedAt
}

func (d *Data) IncDoneCount() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.DoneCount++
}

func (d *Data) SetRepeated(repeated bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.Repeated = repeated
}

func (d *Data) IsRepeated() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.Repeated
}

func (d *Data) GetDoneCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.DoneCount
}

func (d *Data) GetExitCode() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.inner.State.ExitCode
}

func (d *Data) SetExitCode(exitCode int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.ExitCode = exitCode
}

func (d *Data) ClearState(s core.Step) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State = NodeState{}

	// Reset the state of the step
	d.inner.Step = s
}

func (d *Data) MarkError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inner.State.Error = err
	d.inner.State.Status = core.NodeFailed
}

// SetChatMessages sets the chat session messages for the node.
func (d *Data) SetChatMessages(messages []exec.LLMMessage) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inner.State.ChatMessages = messages
}

// GetChatMessages returns the chat session messages for the node.
func (d *Data) GetChatMessages() []exec.LLMMessage {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.inner.State.ChatMessages
}

// SetToolDefinitions sets the tool definitions that were available to the LLM.
func (d *Data) SetToolDefinitions(tools []exec.ToolDefinition) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inner.State.ToolDefinitions = tools
}

// GetToolDefinitions returns the tool definitions that were available to the LLM.
func (d *Data) GetToolDefinitions() []exec.ToolDefinition {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.inner.State.ToolDefinitions
}

// GetApprovalInputs returns a copy of the approval inputs map.
func (d *Data) GetApprovalInputs() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.inner.State.ApprovalInputs == nil {
		return nil
	}

	result := make(map[string]string, len(d.inner.State.ApprovalInputs))
	maps.Copy(result, d.inner.State.ApprovalInputs)
	return result
}

// SetApprovalInputs sets the approval inputs map.
func (d *Data) SetApprovalInputs(inputs map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if inputs == nil {
		d.inner.State.ApprovalInputs = nil
		return
	}

	d.inner.State.ApprovalInputs = make(map[string]string, len(inputs))
	maps.Copy(d.inner.State.ApprovalInputs, inputs)
}
