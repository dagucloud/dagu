// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
)

const (
	canonicalAbortHandlerName = "onAbort"
	legacyAbortHandlerName    = "onCancel"
	// DefaultStaleLeaseThreshold is the shared default for considering a
	// distributed run lease stale when the coordinator has not observed recent
	// liveness for that run.
	DefaultStaleLeaseThreshold = 30 * time.Second
)

// InitialStatus creates an initial Status object for the given DAG
func InitialStatus(dag *core.DAG) DAGRunStatus {
	var (
		autoRetryLimit       int
		autoRetryInterval    time.Duration
		autoRetryBackoff     float64
		autoRetryMaxInterval time.Duration
		procGroup            string
		suspendFlagName      string
	)
	if dag != nil {
		procGroup = dag.ProcGroup()
		suspendFlagName = dag.SuspendFlagName()
		if dag.RetryPolicy != nil {
			autoRetryLimit = dag.RetryPolicy.Limit
			autoRetryInterval = dag.RetryPolicy.Interval
			autoRetryBackoff = dag.RetryPolicy.Backoff
			autoRetryMaxInterval = dag.RetryPolicy.MaxInterval
		}
	}

	return DAGRunStatus{
		Name:                 dag.Name,
		Status:               core.NotStarted,
		PID:                  PID(0),
		Nodes:                NewNodesFromSteps(dag.Steps),
		OnInit:               NewNodeOrNil(dag.HandlerOn.Init),
		OnExit:               NewNodeOrNil(dag.HandlerOn.Exit),
		OnSuccess:            NewNodeOrNil(dag.HandlerOn.Success),
		OnFailure:            NewNodeOrNil(dag.HandlerOn.Failure),
		OnAbort:              NewNodeOrNil(dag.HandlerOn.Abort),
		OnWait:               NewNodeOrNil(dag.HandlerOn.Wait),
		Params:               strings.Join(dag.Params, " "),
		ParamsList:           dag.Params,
		AutoRetryCount:       0,
		AutoRetryLimit:       autoRetryLimit,
		AutoRetryInterval:    autoRetryInterval,
		AutoRetryBackoff:     autoRetryBackoff,
		AutoRetryMaxInterval: autoRetryMaxInterval,
		ProcGroup:            procGroup,
		SuspendFlagName:      suspendFlagName,
		CreatedAt:            time.Now().UnixMilli(),
		StartedAt:            stringutil.FormatTime(time.Time{}),
		FinishedAt:           stringutil.FormatTime(time.Time{}),
		Preconditions:        dag.Preconditions,
		Labels:               dag.Labels.Strings(),
	}
}

// DAGRunStatus represents the complete execution state of a dag-run.
type DAGRunStatus struct {
	Root           DAGRunRef        `json:"root,omitzero"`
	Parent         DAGRunRef        `json:"parent,omitzero"`
	Name           string           `json:"name"`
	DAGRunID       string           `json:"dagRunId"`
	AttemptID      string           `json:"attemptId"`
	AttemptKey     string           `json:"attemptKey,omitempty"` // Globally unique attempt identifier
	Status         core.Status      `json:"status"`
	TriggerType    core.TriggerType `json:"triggerType,omitempty"`
	WorkerID       string           `json:"workerId,omitempty"`
	PID            PID              `json:"pid,omitempty"`
	Nodes          []*Node          `json:"nodes,omitempty"`
	OnInit         *Node            `json:"onInit,omitempty"`
	OnExit         *Node            `json:"onExit,omitempty"`
	OnSuccess      *Node            `json:"onSuccess,omitempty"`
	OnFailure      *Node            `json:"onFailure,omitempty"`
	OnAbort        *Node            `json:"onAbort,omitempty"`
	OnWait         *Node            `json:"onWait,omitempty"`
	CreatedAt      int64            `json:"createdAt,omitempty"`
	QueuedAt       string           `json:"queuedAt,omitempty"`
	ScheduleTime   string           `json:"scheduleTime,omitempty"`
	StartedAt      string           `json:"startedAt,omitempty"`
	FinishedAt     string           `json:"finishedAt,omitempty"`
	AutoRetryCount int              `json:"autoRetryCount,omitempty"`
	AutoRetryLimit int              `json:"autoRetryLimit,omitempty"`
	// AutoRetryInterval is stored as a duration snapshot for retry scanner decisions.
	AutoRetryInterval time.Duration `json:"autoRetryInterval,omitempty"`
	AutoRetryBackoff  float64       `json:"autoRetryBackoff,omitempty"`
	// AutoRetryMaxInterval is stored as a duration snapshot for retry scanner decisions.
	AutoRetryMaxInterval time.Duration      `json:"autoRetryMaxInterval,omitempty"`
	ProcGroup            string             `json:"procGroup,omitempty"`
	SuspendFlagName      string             `json:"suspendFlagName,omitempty"`
	Log                  string             `json:"log,omitempty"`
	WorkingDir           string             `json:"workingDir,omitempty"`
	ArchiveDir           string             `json:"archiveDir,omitempty"`
	Error                string             `json:"error,omitempty"`
	Params               string             `json:"params,omitempty"`
	ParamsList           []string           `json:"paramsList,omitempty"`
	PendingStepRetries   []PendingStepRetry `json:"pendingStepRetries"`
	Preconditions        []*core.Condition  `json:"preconditions,omitempty"`
	Labels               []string           `json:"labels,omitempty"`
	LeaseAt              int64              `json:"leaseAt,omitempty"` // Unix millis; stamped by coordinator on observed run liveness
}

// Tags returns labels under their deprecated name.
// Deprecated: use Labels directly.
func (s DAGRunStatus) Tags() []string {
	return s.Labels
}

// IsLeaseActive reports whether the run's lease is fresh (i.e. a worker is
// still actively executing and the coordinator has observed recent liveness).
// A zero LeaseAt is treated as stale.
func IsLeaseActive(status *DAGRunStatus, staleThreshold time.Duration) bool {
	if status == nil || status.LeaseAt == 0 {
		return false
	}
	if staleThreshold <= 0 {
		staleThreshold = DefaultStaleLeaseThreshold
	}
	return time.Since(time.UnixMilli(status.LeaseAt)) < staleThreshold
}

// DAGRun returns a reference to the dag-run associated with this status
func (st *DAGRunStatus) DAGRun() DAGRunRef {
	return NewDAGRunRef(st.Name, st.DAGRunID)
}

// Errors returns a slice of errors for the current status
func (st *DAGRunStatus) Errors() []error {
	var errs []error
	if st.Error != "" {
		errs = append(errs, errors.New(st.Error))
	}
	for _, node := range st.Nodes {
		if node.Error != "" {
			errs = append(errs, fmt.Errorf("node %s: %s", node.Step.Name, node.Error))
		}
	}
	for _, handler := range st.handlerNodes() {
		if handler.node != nil && handler.node.Error != "" {
			errs = append(errs, fmt.Errorf("%s: %s", handler.name, handler.node.Error))
		}
	}
	return errs
}

// PendingStepRetriesFromNodes extracts pending parent-managed step retries from
// a DAG status snapshot.
func PendingStepRetriesFromNodes(nodes []*Node) []PendingStepRetry {
	var retries []PendingStepRetry
	for _, node := range nodes {
		if retry, ok := pendingStepRetryForNode(node.Step.Name, node); ok {
			retries = append(retries, retry)
		}
	}
	return retries
}

// PendingStepRetriesFromStatus returns the persisted pending step retries when
// present and falls back to deriving them from node state for older statuses
// that predate the field.
func PendingStepRetriesFromStatus(status *DAGRunStatus) []PendingStepRetry {
	if status == nil {
		return nil
	}
	if status.PendingStepRetries != nil {
		return status.PendingStepRetries
	}

	retries := PendingStepRetriesFromNodes(status.Nodes)
	for _, handler := range status.handlerNodes() {
		stepName := handler.name
		if handler.node != nil && handler.node.Step.Name != "" {
			stepName = handler.node.Step.Name
		}
		if retry, ok := pendingStepRetryForNode(stepName, handler.node); ok {
			retries = append(retries, retry)
		}
	}
	return retries
}

// NodeByName returns the node with the specified name.
// For handlers, it matches on both the handler label (e.g., "onSuccess")
// and the step name within the handler.
func (st *DAGRunStatus) NodeByName(name string) (*Node, error) {
	name = normalizeAbortHandlerLookup(name)
	for _, node := range st.Nodes {
		if node.Step.Name == name {
			return node, nil
		}
	}
	for _, handler := range st.handlerNodes() {
		if handler.node != nil {
			// Match on handler label (e.g., "onSuccess") or step name
			if handler.name == name || handler.node.Step.Name == name {
				return handler.node, nil
			}
		}
	}
	return nil, fmt.Errorf("node %s not found", name)
}

// StatusFromJSON deserializes a JSON string into a Status object
func StatusFromJSON(s string) (*DAGRunStatus, error) {
	var status DAGRunStatus
	if err := json.Unmarshal([]byte(s), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// UnmarshalJSON keeps legacy onCancel and tags status files readable while
// normalizing canonical handler/metadata names in memory.
func (st *DAGRunStatus) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	_, hasLabels := raw["labels"]

	type alias DAGRunStatus
	aux := struct {
		alias
		OnCancel       *Node    `json:"onCancel,omitempty"`
		DeprecatedTags []string `json:"tags,omitempty"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	*st = DAGRunStatus(aux.alias)
	if !hasLabels && len(aux.DeprecatedTags) > 0 {
		st.Labels = aux.DeprecatedTags
	}
	if st.OnAbort == nil {
		st.OnAbort = aux.OnCancel
	}
	normalizeAbortHandlerNode(st.OnAbort)
	return nil
}

// PID represents a process ID for a running dag-run
type PID int

// String returns the string representation of the PID, or an empty string if 0
func (p PID) String() string {
	if p <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", p)
}

// FormatTime formats a time.Time or returns empty string if it's the zero value.
// This is a convenience wrapper around stringutil.FormatTime.
func FormatTime(val time.Time) string {
	return stringutil.FormatTime(val)
}

// handlerNode pairs a handler node with its name for iteration
type handlerNode struct {
	name string
	node *Node
}

// handlerNodes returns all handler nodes for iteration
func (st *DAGRunStatus) handlerNodes() []handlerNode {
	return []handlerNode{
		{"onInit", st.OnInit},
		{"onExit", st.OnExit},
		{"onSuccess", st.OnSuccess},
		{"onFailure", st.OnFailure},
		{canonicalAbortHandlerName, st.OnAbort},
		{"onWait", st.OnWait},
	}
}

func normalizeAbortHandlerLookup(name string) string {
	if name == legacyAbortHandlerName {
		return canonicalAbortHandlerName
	}
	return name
}

func normalizeAbortHandlerNode(node *Node) {
	if node == nil {
		return
	}
	if node.Step.Name == "" || node.Step.Name == legacyAbortHandlerName {
		node.Step.Name = canonicalAbortHandlerName
	}
}

func pendingStepRetryForNode(stepName string, node *Node) (PendingStepRetry, bool) {
	if node == nil || node.Status != core.NodeRetrying || stepName == "" {
		return PendingStepRetry{}, false
	}

	interval := core.CalculateBackoffInterval(
		node.Step.RetryPolicy.Interval,
		node.Step.RetryPolicy.Backoff,
		node.Step.RetryPolicy.MaxInterval,
		node.RetryCount-1,
	)
	return PendingStepRetry{
		StepName: stepName,
		Interval: interval,
	}, true
}
