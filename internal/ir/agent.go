// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

const (
	// AgentStepName is the reserved name of the synthesized step that drives
	// an agent DAG. It cannot be used as a step name or ID.
	AgentStepName = "__agent__"

	// AskUserStepName is the reserved name and ID of the synthesized human task
	// an agent opens when it needs to ask a question no declared step
	// covers. It cannot be used as a step name or ID.
	//
	// The name is plain rather than underscore-wrapped because it is what an
	// operator types to answer:
	//
	//	dagu human-task complete <dag> --step ask_user --inputs-json '{"answer":"..."}'
	AskUserStepName = "ask_user"

	// AskUserAnswerField is the single form field an ask_user question collects.
	AskUserAnswerField = "answer"

	// DefaultAgentMaxIterations bounds the number of agent turns when
	// llm.max_tool_iterations is not set.
	DefaultAgentMaxIterations = 50

	// DefaultAgentMaxStepRuns caps how many times the agent may run a
	// single declared step within one DAG run.
	DefaultAgentMaxStepRuns = 5

	// DefaultAgentMaxQuestions caps how many questions an agent may put
	// to a person in one run. Each one suspends the run, so an unbounded
	// agent could pester someone indefinitely.
	DefaultAgentMaxQuestions = 5

	// DefaultAgentMaxContextTokens is the prompt size at which observation
	// aging starts.
	DefaultAgentMaxContextTokens = 200_000

	// DefaultAgentObservationMaxBytes bounds each tool result added to a
	// agent conversation.
	DefaultAgentObservationMaxBytes = 512 * 1024

	// DefaultAgentObservationKeepRecent is the number of tool results that
	// remain complete after observation aging starts.
	DefaultAgentObservationKeepRecent = 20
)

// AgentTask is a goal an Agent DAG must settle before the run concludes.
type AgentTask struct {
	// Name identifies the task. It is unique within the DAG.
	Name string `json:"name"`
	// Description states the completion criteria in natural language.
	Description string `json:"description,omitempty"`
}

// IsAgent reports whether the DAG is driven by an LLM agent instead of
// a static dependency graph.
func (d *DAG) IsAgent() bool {
	return d != nil && d.Type == TypeAgent
}

// IsSynthesizedAgentStep reports whether a step name belongs to the
// scaffolding an agent DAG is built with rather than to a declared action.
func IsSynthesizedAgentStep(name string) bool {
	return name == AgentStepName || name == AskUserStepName
}

// AgentStep returns the synthesized agent step, or nil when the DAG is
// not an agent DAG.
func (d *DAG) AgentStep() *Step {
	if d == nil {
		return nil
	}
	for i, step := range d.Steps {
		if step.Name == AgentStepName {
			return &d.Steps[i]
		}
	}
	return nil
}

// AgentMaxIterations returns the upper bound on agent turns for a
// single run.
func (d *DAG) AgentMaxIterations() int {
	if d == nil || d.LLM == nil || d.LLM.MaxToolIterations == nil {
		return DefaultAgentMaxIterations
	}
	if n := *d.LLM.MaxToolIterations; n > 0 {
		return n
	}
	return DefaultAgentMaxIterations
}

// AgentMaxContextTokens returns the prompt size at which observation aging
// starts. Zero disables proactive aging.
func (d *DAG) AgentMaxContextTokens() int {
	if d == nil || d.LLM == nil || d.LLM.MaxContextTokens == nil {
		return DefaultAgentMaxContextTokens
	}
	return *d.LLM.MaxContextTokens
}

// AgentObservationMaxBytes returns the maximum agent-facing size of
// one observation. Zero disables the size limit.
func (d *DAG) AgentObservationMaxBytes() int {
	if d == nil || d.LLM == nil || d.LLM.ObservationMaxBytes == nil {
		return DefaultAgentObservationMaxBytes
	}
	return *d.LLM.ObservationMaxBytes
}

// AgentObservationKeepRecent returns how many recent observations remain
// complete after aging starts. Zero disables observation aging.
func (d *DAG) AgentObservationKeepRecent() int {
	if d == nil || d.LLM == nil || d.LLM.ObservationKeepRecent == nil {
		return DefaultAgentObservationKeepRecent
	}
	return *d.LLM.ObservationKeepRecent
}

// NewAgentStep builds the step that carries the agent's LLM config and
// task list. It is appended to an agent DAG at build time and is the node the
// runner drives the decision loop from.
func NewAgentStep(dag *DAG) Step {
	return Step{
		Name:        AgentStepName,
		Description: "LLM agent",
		LLM:         dag.LLM,
		ExecutorConfig: ExecutorConfig{
			Type: ExecutorTypeAgent,
		},
	}
}

// AgentSessionState describes the lifecycle of a managed coding-agent session.
type AgentSessionState string

const (
	AgentSessionStarting    AgentSessionState = "starting"
	AgentSessionRunning     AgentSessionState = "running"
	AgentSessionWaiting     AgentSessionState = "waiting"
	AgentSessionSucceeded   AgentSessionState = "succeeded"
	AgentSessionFailed      AgentSessionState = "failed"
	AgentSessionAborted     AgentSessionState = "aborted"
	AgentSessionUnavailable AgentSessionState = "unavailable"
)

// AgentInteractionKind identifies input requested by a managed agent.
type AgentInteractionKind string

const (
	AgentInteractionPermission AgentInteractionKind = "permission"
	AgentInteractionQuestion   AgentInteractionKind = "question"
)

// AgentInteractionStatus describes whether requested input can still be answered.
type AgentInteractionStatus string

const (
	AgentInteractionPending  AgentInteractionStatus = "pending"
	AgentInteractionAnswered AgentInteractionStatus = "answered"
	AgentInteractionRejected AgentInteractionStatus = "rejected"
)

// AgentQuestion is one question in a managed agent interaction.
type AgentQuestion struct {
	Header   string                `json:"header"`
	Question string                `json:"question"`
	Options  []AgentQuestionOption `json:"options,omitempty"`
	Multiple bool                  `json:"multiple,omitempty"`
	Custom   bool                  `json:"custom,omitempty"`
}

// AgentQuestionOption is a selectable answer offered by an agent.
type AgentQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AgentInteraction records an agent request and its durable response.
type AgentInteraction struct {
	ID                      string                 `json:"id"`
	Kind                    AgentInteractionKind   `json:"kind"`
	Status                  AgentInteractionStatus `json:"status"`
	Permission              string                 `json:"permission,omitempty"`
	Patterns                []string               `json:"patterns,omitempty"`
	AllowForSessionPatterns []string               `json:"allowForSessionPatterns,omitempty"`
	Questions               []AgentQuestion        `json:"questions,omitempty"`
	Decision                string                 `json:"decision,omitempty"`
	Answers                 [][]string             `json:"answers,omitempty"`
	Applied                 bool                   `json:"applied,omitempty"`
	CreatedAt               string                 `json:"createdAt,omitempty"`
	RespondedAt             string                 `json:"respondedAt,omitempty"`
	RespondedBy             string                 `json:"respondedBy,omitempty"`
	RespondedByID           string                 `json:"respondedById,omitempty"`
}

// AgentPermissionGrant is a permission scope approved for one Dagu session generation.
type AgentPermissionGrant struct {
	Permission  string   `json:"permission"`
	Patterns    []string `json:"patterns"`
	GrantedAt   string   `json:"grantedAt,omitempty"`
	GrantedBy   string   `json:"grantedBy,omitempty"`
	GrantedByID string   `json:"grantedById,omitempty"`
}

// AgentSessionEvent is one normalized item in a managed agent session timeline.
type AgentSessionEvent struct {
	Sequence  int64    `json:"sequence"`
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Timestamp string   `json:"timestamp,omitempty"`
	Role      string   `json:"role,omitempty"`
	Content   string   `json:"content,omitempty"`
	Name      string   `json:"name,omitempty"`
	Status    string   `json:"status,omitempty"`
	Files     []string `json:"files,omitempty"`
}

// AgentUsage contains aggregate model usage for a managed session.
type AgentUsage struct {
	InputTokens     int64   `json:"inputTokens,omitempty"`
	OutputTokens    int64   `json:"outputTokens,omitempty"`
	ReasoningTokens int64   `json:"reasoningTokens,omitempty"`
	TotalTokens     int64   `json:"totalTokens,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
}

// AgentSessionResource identifies a Dagu-owned provider session retained by a DAG run.
type AgentSessionResource struct {
	Provider      string `json:"provider"`
	SessionID     string `json:"sessionId"`
	Directory     string `json:"directory,omitempty"`
	OwnerWorkerID string `json:"ownerWorkerId,omitempty"`
	StepName      string `json:"stepName,omitempty"`
	Generation    int    `json:"generation,omitempty"`
}

// AgentSession contains durable state required to display and resume a managed session.
type AgentSession struct {
	Provider           string                 `json:"provider"`
	ProviderVersion    string                 `json:"providerVersion,omitempty"`
	SessionID          string                 `json:"sessionId,omitempty"`
	Generation         int                    `json:"generation,omitempty"`
	Agent              string                 `json:"agent,omitempty"`
	Model              string                 `json:"model,omitempty"`
	Variant            string                 `json:"variant,omitempty"`
	OwnerWorkerID      string                 `json:"ownerWorkerId,omitempty"`
	HostInstanceID     string                 `json:"hostInstanceId,omitempty"`
	Directory          string                 `json:"directory,omitempty"`
	State              AgentSessionState      `json:"state"`
	LastError          string                 `json:"lastError,omitempty"`
	PromptSent         bool                   `json:"promptSent,omitempty"`
	RestartPending     bool                   `json:"restartPending,omitempty"`
	SessionOwned       bool                   `json:"sessionOwned,omitempty"`
	DiscardedSessionID string                 `json:"discardedSessionId,omitempty"`
	DiscardedOwned     bool                   `json:"discardedOwned,omitempty"`
	PermissionGrants   []AgentPermissionGrant `json:"permissionGrants,omitempty"`
	Usage              AgentUsage             `json:"usage,omitzero"`
	Interactions       []AgentInteraction     `json:"interactions,omitempty"`
	Events             []AgentSessionEvent    `json:"events,omitempty"`
}

// CloneAgentSession returns a detached copy safe for runtime handoff.
func CloneAgentSession(session *AgentSession) *AgentSession {
	if session == nil {
		return nil
	}
	clone := *session
	clone.Interactions = make([]AgentInteraction, len(session.Interactions))
	for i := range session.Interactions {
		clone.Interactions[i] = session.Interactions[i]
		clone.Interactions[i].Patterns = append([]string(nil), session.Interactions[i].Patterns...)
		clone.Interactions[i].AllowForSessionPatterns = append([]string(nil), session.Interactions[i].AllowForSessionPatterns...)
		clone.Interactions[i].Questions = append([]AgentQuestion(nil), session.Interactions[i].Questions...)
		for j := range clone.Interactions[i].Questions {
			clone.Interactions[i].Questions[j].Options = append([]AgentQuestionOption(nil), session.Interactions[i].Questions[j].Options...)
		}
		clone.Interactions[i].Answers = make([][]string, len(session.Interactions[i].Answers))
		for j := range session.Interactions[i].Answers {
			clone.Interactions[i].Answers[j] = append([]string(nil), session.Interactions[i].Answers[j]...)
		}
	}
	clone.PermissionGrants = make([]AgentPermissionGrant, len(session.PermissionGrants))
	for i := range session.PermissionGrants {
		clone.PermissionGrants[i] = session.PermissionGrants[i]
		clone.PermissionGrants[i].Patterns = append([]string(nil), session.PermissionGrants[i].Patterns...)
	}
	clone.Events = make([]AgentSessionEvent, len(session.Events))
	for i := range session.Events {
		clone.Events[i] = session.Events[i]
		clone.Events[i].Files = append([]string(nil), session.Events[i].Files...)
	}
	return &clone
}

// MergeAgentSessionResources retains every Dagu-owned provider session observed by a DAG run.
func MergeAgentSessionResources(resources []AgentSessionResource, nodes []*Node) []AgentSessionResource {
	result := make([]AgentSessionResource, 0, len(resources))
	indexes := make(map[string]int, len(resources))
	for _, resource := range resources {
		result, indexes = mergeAgentSessionResource(result, indexes, resource)
	}
	for _, node := range nodes {
		if node == nil || node.AgentSession == nil {
			continue
		}
		session := node.AgentSession
		if session.SessionOwned {
			result, indexes = mergeAgentSessionResource(result, indexes, AgentSessionResource{
				Provider: session.Provider, SessionID: session.SessionID, Directory: session.Directory,
				OwnerWorkerID: session.OwnerWorkerID, StepName: node.Step.Name, Generation: session.Generation,
			})
		}
		if session.DiscardedOwned {
			result, indexes = mergeAgentSessionResource(result, indexes, AgentSessionResource{
				Provider: session.Provider, SessionID: session.DiscardedSessionID, Directory: session.Directory,
				OwnerWorkerID: session.OwnerWorkerID, StepName: node.Step.Name, Generation: session.Generation - 1,
			})
		}
	}
	return result
}

func mergeAgentSessionResource(
	resources []AgentSessionResource,
	indexes map[string]int,
	resource AgentSessionResource,
) ([]AgentSessionResource, map[string]int) {
	if resource.Provider == "" || resource.SessionID == "" {
		return resources, indexes
	}
	key := agentSessionResourceKey(resource)
	if index, ok := indexes[key]; ok {
		resources[index] = resource
		return resources, indexes
	}
	indexes[key] = len(resources)
	return append(resources, resource), indexes
}

func agentSessionResourceKey(resource AgentSessionResource) string {
	return resource.Provider + "\x00" + resource.OwnerWorkerID + "\x00" + resource.SessionID
}

// RetryAgentOwnerWorkerID returns the execution host required by an agent session that will resume.
func RetryAgentOwnerWorkerID(status *DAGRunStatus, stepRetry bool) string {
	if status == nil {
		return ""
	}
	var owner string
	for _, node := range status.Nodes {
		if node == nil || node.AgentSession == nil {
			continue
		}
		session := node.AgentSession
		if !stepRetry && node.Status != NodeFailed && node.Status != NodeRetrying && node.Status != NodeAborted &&
			node.Status != NodeRejected && node.Status != NodeNotStarted && node.Status != NodeWaiting {
			continue
		}
		if session.RestartPending || session.OwnerWorkerID == "" {
			continue
		}
		if owner != "" && owner != session.OwnerWorkerID {
			return ""
		}
		owner = session.OwnerWorkerID
	}
	return owner
}
