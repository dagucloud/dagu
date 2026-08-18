// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
)

// PushBackEntry records one push-back event for a step approval cycle.
type PushBackEntry struct {
	Iteration int               `json:"iteration"`
	By        string            `json:"by,omitempty"`
	ByID      string            `json:"byId,omitempty"`
	At        string            `json:"at,omitempty"`
	Inputs    map[string]string `json:"inputs,omitempty"`
}

// NodeStatusDetail identifies an independently tracked execution within a node.
type NodeStatusDetail struct {
	Label  string     `json:"label"`
	Status NodeStatus `json:"status"`
}

// BuildDecision records how an build node was satisfied.
type BuildDecision string

const (
	BuildDecisionNone     BuildDecision = "none"
	BuildDecisionAlways   BuildDecision = "always"
	BuildDecisionExecute  BuildDecision = "execute"
	BuildDecisionReuse    BuildDecision = "reuse"
	BuildDecisionDeferred BuildDecision = "deferred"
)

// BuildPhase records the latest build lifecycle phase.
type BuildPhase string

const (
	BuildPhasePrecondition BuildPhase = "precondition"
	BuildPhaseEvaluate     BuildPhase = "evaluate"
	BuildPhaseExecute      BuildPhase = "execute"
	BuildPhaseVerify       BuildPhase = "verify"
	BuildPhaseCommit       BuildPhase = "commit"
	BuildPhaseComplete     BuildPhase = "complete"
)

// BuildReason is a stable machine-readable explanation of a decision.
type BuildReason string

const (
	BuildReasonIneligible                  BuildReason = "ineligible"
	BuildReasonStoreUnavailable            BuildReason = "store_unavailable"
	BuildReasonEvaluationFailed            BuildReason = "evaluation_failed"
	BuildReasonCancelledBeforeDecision     BuildReason = "cancelled_before_decision"
	BuildReasonRecoveryFailed              BuildReason = "recovery_failed"
	BuildReasonUpstreamWouldExecute        BuildReason = "upstream_would_execute"
	BuildReasonInputMissing                BuildReason = "input_missing"
	BuildReasonControlDependencyRan        BuildReason = "control_dependency_ran"
	BuildReasonReuseDisabled               BuildReason = "reuse_disabled"
	BuildReasonManifestMissing             BuildReason = "manifest_missing"
	BuildReasonRecipeChanged               BuildReason = "recipe_changed"
	BuildReasonInputChanged                BuildReason = "input_changed"
	BuildReasonOutputMissing               BuildReason = "output_missing"
	BuildReasonOutputChanged               BuildReason = "output_changed"
	BuildReasonMatched                     BuildReason = "matched"
	BuildReasonInputChangedDuringExecution BuildReason = "input_changed_during_execution"
	BuildReasonPreconditionError           BuildReason = "precondition_error"
	BuildReasonPreconditionNotMet          BuildReason = "precondition_not_met"
)

// BuildExecution explains how an build node was satisfied.
type BuildExecution struct {
	Decision           BuildDecision `json:"decision"`
	Phase              BuildPhase    `json:"phase"`
	Reason             BuildReason   `json:"reason"`
	Detail             string        `json:"detail,omitempty"`
	Fingerprint        string        `json:"fingerprint,omitempty"`
	MaterializationKey string        `json:"materializationKey,omitempty"`
	ProducerRun        DAGRunRef     `json:"producerRun,omitzero"`
	ProducerAttemptID  string        `json:"producerAttemptId,omitempty"`
}

// Node represents a DAG step with its execution state for persistence
type Node struct {
	Step                Step                 `json:"step,omitzero"`
	PreconditionResults []ConditionResult    `json:"-"`
	Stdout              string               `json:"stdout"` // standard output log file path
	Stderr              string               `json:"stderr"` // standard error log file path
	WorkingDir          string               `json:"workingDir,omitempty"`
	StartedAt           string               `json:"startedAt"`
	FinishedAt          string               `json:"finishedAt"`
	Status              NodeStatus           `json:"status"`
	RetriedAt           string               `json:"retriedAt,omitempty"`
	RetryCount          int                  `json:"retryCount,omitempty"`
	DoneCount           int                  `json:"doneCount,omitempty"`
	Repeated            bool                 `json:"repeated,omitempty"` // indicates if the node has been repeated
	SkippedByRetry      bool                 `json:"skippedByRetry,omitempty"`
	Error               string               `json:"error,omitempty"`
	StatusDetails       []NodeStatusDetail   `json:"statusDetails,omitempty"`
	Build               *BuildExecution      `json:"build,omitempty"`
	SubRuns             []SubDAGRun          `json:"children,omitempty"`
	SubRunsRepeated     []SubDAGRun          `json:"childrenRepeated,omitempty"` // repeated sub DAG runs
	OutputVariables     *collections.SyncMap `json:"outputVariables,omitempty"`
	OutputValue         *string              `json:"outputValue,omitempty"`
	OutputsValue        *string              `json:"outputsValue,omitempty"`
	StepOutputsValue    *string              `json:"stepOutputsValue,omitempty"`
	HumanTaskInput      json.RawMessage      `json:"humanTaskInput,omitempty"`
	// AgentState stores the goal progress of an agent DAG's agent
	// step, so a suspended run resumes with its task list intact.
	AgentState json.RawMessage `json:"agentState,omitempty"`
	// HumanTaskCompletedBy records who completed this human task.
	HumanTaskCompletedBy string `json:"humanTaskCompletedBy,omitempty"`
	// HumanTaskCompletedByID records the subject ID that completed this human task.
	HumanTaskCompletedByID string `json:"humanTaskCompletedById,omitempty"`
	// ApprovedAt records when this wait step was approved
	ApprovedAt string `json:"approvedAt,omitempty"`
	// ApprovalInputs stores key-value parameters provided during approval
	ApprovalInputs map[string]string `json:"approvalInputs,omitempty"`
	// ApprovedBy records who approved this wait step (username)
	ApprovedBy string `json:"approvedBy,omitempty"`
	// ApprovedByID records the subject ID that approved this wait step.
	ApprovedByID string `json:"approvedById,omitempty"`
	// RejectedAt records when this wait step was rejected
	RejectedAt string `json:"rejectedAt,omitempty"`
	// RejectedBy records who rejected this wait step (username)
	RejectedBy string `json:"rejectedBy,omitempty"`
	// RejectedByID records the subject ID that rejected this wait step.
	RejectedByID string `json:"rejectedById,omitempty"`
	// RejectionReason stores the optional reason for rejection
	RejectionReason string `json:"rejectionReason,omitempty"`
	// ApprovalIteration tracks how many times this step has been pushed back.
	ApprovalIteration int `json:"approvalIteration,omitempty"`
	// PushBackInputs stores the inputs from the last push-back.
	// These are injected as environment variables when the step re-executes.
	PushBackInputs map[string]string `json:"pushBackInputs,omitempty"`
	// PushBackHistory stores the chronological push-back inputs for this step.
	PushBackHistory []PushBackEntry `json:"pushBackHistory,omitempty"`
	// PushBackPreviousStdout stores the stdout log path from the execution that
	// was reset by the latest push-back.
	PushBackPreviousStdout string `json:"pushBackPreviousStdout,omitempty"`
	// ChatMessages stores the session messages for chat/LLM steps.
	// This field is populated during execution and synced via status updates
	// from workers.
	ChatMessages []LLMMessage `json:"chatMessages,omitempty"`
	// ToolDefinitions stores the tool definitions that were available to the LLM.
	// This enables debugging visibility into what tools and schemas were sent.
	ToolDefinitions []ToolDefinition `json:"toolDefinitions,omitempty"`
	// AgentSession stores managed coding-agent state for display and durable resume.
	AgentSession *AgentSession `json:"agentSession,omitempty"`
}

type nodeJSON Node

// presentRawJSON returns the first candidate that carries a value, or nil when
// none does. A literal JSON null counts as absent, so a field written as null
// neither masks a later candidate nor reaches callers that test for emptiness.
func presentRawJSON(candidates ...json.RawMessage) json.RawMessage {
	for _, raw := range candidates {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			return raw
		}
	}
	return nil
}

// MarshalJSON keeps runtime condition results inside the persisted step object.
func (n Node) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Step stepSnapshot `json:"step,omitzero"`
		nodeJSON
	}{
		Step:     newStepSnapshot(n.Step, n.PreconditionResults),
		nodeJSON: nodeJSON(n),
	})
}

// UnmarshalJSON restores the step definition and its runtime condition results,
// and keeps agent state written under its legacy name readable.
func (n *Node) UnmarshalJSON(data []byte) error {
	var decoded nodeJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var runtimeState struct {
		Step struct {
			Preconditions []ConditionResult `json:"preconditions"`
		} `json:"step"`
		LegacyAgentState json.RawMessage `json:"controllerState"`
	}
	if err := json.Unmarshal(data, &runtimeState); err != nil {
		return err
	}
	*n = Node(decoded)
	n.AgentState = presentRawJSON(n.AgentState, runtimeState.LegacyAgentState)
	n.PreconditionResults = slices.Clone(runtimeState.Step.Preconditions)
	if n.PreconditionResults != nil {
		n.Step = newStepSnapshot(n.Step, n.PreconditionResults).definition()
	}
	return nil
}

// SubDAGRun represents a sub DAG run associated with a node
type SubDAGRun struct {
	DAGRunID     string `json:"dagRunId,omitempty"`
	Params       string `json:"params,omitempty"`
	ParallelItem string `json:"parallelItem,omitempty"`
	// DAGName is the name of the executed sub-DAG.
	// For chat tool calls, this is the tool DAG name.
	// This field enables UI drill-down when step.call is not set.
	DAGName string `json:"dagName,omitempty"`
}

// NewNodesFromSteps converts a list of DAG steps to persistence Node objects.
func NewNodesFromSteps(steps []Step) []*Node {
	var ret []*Node
	for _, s := range steps {
		ret = append(ret, NewNodeFromStep(s))
	}
	return ret
}

// NewNodeFromStep creates a new Node with default status values for the given step.
func NewNodeFromStep(step Step) *Node {
	return &Node{
		Step:                step,
		PreconditionResults: conditionResults(step.Preconditions),
		StartedAt:           "-",
		FinishedAt:          "-",
		Status:              NodeNotStarted,
	}
}

// newNodeOrNil creates a Node from a Step or returns nil if the step is nil.
func newNodeOrNil(s *Step) *Node {
	if s == nil {
		return nil
	}
	return NewNodeFromStep(*s)
}
