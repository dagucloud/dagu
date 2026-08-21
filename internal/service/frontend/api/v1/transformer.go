// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const maxIntValue = int(^uint(0) >> 1)

func toSchedule(s ir.Schedule) api.Schedule {
	schedule := api.Schedule{}
	if kind := s.GetKind(); kind != "" {
		schedule.Kind = ptrOf(api.ScheduleKind(kind))
	}
	if s.Expression != "" {
		schedule.Expression = s.Expression
	}
	if at, ok := s.OneOffTime(); ok {
		schedule.At = &at
	}
	schedule.Profile = toRuntimeProfileName(s.Profile)
	return schedule
}

func workspaceResponseNameFromLabels(labels ir.Labels) *string {
	workspaceName, ok := workspace.WorkspaceNameFromLabels(labels)
	if !ok {
		return nil
	}
	return ptrOf(workspaceName)
}

func workspaceResponseNameFromLabelStrings(labels []string) *string {
	return workspaceResponseNameFromLabels(ir.NewLabels(labels))
}

func toDAG(dag *ir.DAG) api.DAG {
	schedules := make([]api.Schedule, len(dag.Schedule))
	for i, s := range dag.Schedule {
		schedules[i] = toSchedule(s)
	}

	return api.DAG{
		Name:          dag.Name,
		Group:         ptrOf(dag.Group),
		Workspace:     workspaceResponseNameFromLabels(dag.Labels),
		Description:   ptrOf(dag.Description),
		Params:        ptrOf(dag.Params),
		DefaultParams: ptrOf(dag.DefaultParams),
		Resources:     toDAGResources(dag.Resources),
		Labels:        ptrOf(dag.Labels.Strings()),
		Tags:          ptrOf(dag.Labels.Strings()),
		Schedule:      ptrOf(schedules),
	}
}

func toDAGResources(resources *ir.Resources) *api.DAGResources {
	if resources == nil || resources.Limits == nil {
		return nil
	}
	return &api.DAGResources{
		Limits: &api.DAGResourceLimits{
			Cpu:    ptrOf(resources.Limits.CPU),
			Memory: ptrOf(resources.Limits.Memory),
		},
	}
}

func toStep(obj ir.Step) api.Step {
	conditions := make([]api.Condition, len(obj.Preconditions))
	for i := range obj.Preconditions {
		conditions[i] = toPrecondition(obj.Preconditions[i])
	}

	var repeatMode *api.RepeatMode
	if obj.RepeatPolicy.RepeatMode != "" {
		repeatMode = new(api.RepeatMode(obj.RepeatPolicy.RepeatMode))
	}

	repeatPolicy := api.RepeatPolicy{
		Repeat:   repeatMode,
		Interval: ptrOf(int(obj.RepeatPolicy.Interval.Seconds())),
		Limit:    ptrOf(obj.RepeatPolicy.Limit),
		ExitCode: ptrOf(obj.RepeatPolicy.ExitCode),
	}

	if obj.RepeatPolicy.Condition != nil {
		repeatPolicy.Condition = ptrOf(toPrecondition(obj.RepeatPolicy.Condition))
	}

	commands := make([]api.CommandEntry, len(obj.Commands))
	for i, cmd := range obj.Commands {
		commands[i] = api.CommandEntry{
			Command: cmd.Command,
			Args:    ptrOf(cmd.Args),
		}
	}

	step := api.Step{
		Name:          obj.Name,
		Id:            ptrOf(obj.ID),
		Description:   ptrOf(obj.Description),
		Commands:      ptrOf(commands),
		Depends:       ptrOf(obj.Depends),
		Dir:           ptrOf(obj.Dir),
		MailOnError:   ptrOf(obj.MailOnError),
		Output:        ptrOf(obj.Output),
		Preconditions: ptrOf(conditions),
		RepeatPolicy:  ptrOf(repeatPolicy),
		Script:        ptrOf(obj.Script),
	}
	if len(obj.Inputs) > 0 {
		inputs := make([]api.StepInputDeclaration, len(obj.Inputs))
		for i, input := range obj.Inputs {
			inputs[i] = api.StepInputDeclaration{Name: input.Name, Path: input.Path}
		}
		step.Inputs = &inputs
	}
	if len(obj.Dependencies) > 0 {
		step.Dependencies = ptrOf(obj.Dependencies)
	}
	if len(obj.Outputs) > 0 {
		outputs := make([]api.StepOutputDeclaration, len(obj.Outputs))
		for i, output := range obj.Outputs {
			outputs[i].Name = output.Name
			if output.Type != "" {
				outputType := api.StepOutputDeclarationType(output.Type)
				outputs[i].Type = &outputType
			}
			if output.Path != "" {
				outputs[i].Path = &output.Path
			}
		}
		step.Outputs = &outputs
	}

	if obj.Timeout > 0 {
		step.TimeoutSec = new(int(obj.Timeout.Seconds()))
	}

	if obj.SubDAG != nil {
		step.Call = ptrOf(obj.SubDAG.Name)
		step.Params = ptrOf(obj.SubDAG.Params)
	}
	if obj.Parallel != nil {
		parallel := struct {
			Items         *api.Step_Parallel_Items `json:"items,omitempty"`
			MaxConcurrent *int                     `json:"maxConcurrent,omitempty"`
		}{
			MaxConcurrent: ptrOf(obj.Parallel.MaxConcurrent),
		}

		switch {
		case obj.Parallel.Variable != "":
			items := &api.Step_Parallel_Items{}
			if err := items.FromStepParallelItems1(obj.Parallel.Variable); err == nil {
				parallel.Items = items
			}
		case len(obj.Parallel.Items) > 0:
			itemStrings := make([]string, len(obj.Parallel.Items))
			for i, item := range obj.Parallel.Items {
				itemStrings[i] = item.Value
			}
			items := &api.Step_Parallel_Items{}
			if err := items.FromStepParallelItems0(itemStrings); err == nil {
				parallel.Items = items
			}
		}
		step.Parallel = &parallel
	}

	if obj.ExecutorConfig.Type != "" || obj.ExecutorConfig.Config != nil {
		step.ExecutorConfig = &struct {
			Config *map[string]any `json:"config,omitempty"`
			Type   *string         `json:"type,omitempty"`
		}{
			Type:   ptrOf(obj.ExecutorConfig.Type),
			Config: ptrOf(obj.ExecutorConfig.Config),
		}
	}

	if obj.Approval != nil {
		step.Approval = &api.ApprovalConfig{
			Prompt:   ptrOf(obj.Approval.Prompt),
			Input:    ptrOf(obj.Approval.Input),
			Required: ptrOf(obj.Approval.Required),
			RewindTo: ptrOf(obj.Approval.RewindTo),
		}
	}

	if obj.HumanTask != nil {
		humanTask := &api.HumanTaskConfig{Prompt: obj.HumanTask.Prompt}
		if len(obj.HumanTask.Form) > 0 {
			var form map[string]any
			decoder := json.NewDecoder(bytes.NewReader(obj.HumanTask.Form))
			decoder.UseNumber()
			if err := decoder.Decode(&form); err == nil && form != nil {
				var extra any
				if err := decoder.Decode(&extra); err == io.EOF {
					humanTask.Form = &form
				}
			}
		}
		step.HumanTask = humanTask
	}

	if obj.Router != nil {
		routes := make([]struct {
			Pattern string   `json:"pattern"`
			Targets []string `json:"targets"`
		}, len(obj.Router.Routes))
		for i, r := range obj.Router.Routes {
			routes[i] = struct {
				Pattern string   `json:"pattern"`
				Targets []string `json:"targets"`
			}{
				Pattern: r.Pattern,
				Targets: r.Targets,
			}
		}
		step.Router = &struct {
			Routes []struct {
				Pattern string   `json:"pattern"`
				Targets []string `json:"targets"`
			} `json:"routes"`
			Value string `json:"value"`
		}{
			Value:  obj.Router.Value,
			Routes: routes,
		}
	}

	return step
}

func toPrecondition(obj *ir.Condition) api.Condition {
	condition := api.Condition{
		Expected: ptrOf(obj.Expected),
		Negate:   ptrOf(obj.Negate),
		Error:    ptrOf(""),
	}
	if obj.Condition != "" {
		condition.Condition = ptrOf(obj.Condition)
	}
	if obj.Eval != "" {
		condition.Eval = ptrOf(obj.Eval)
	}
	return condition
}

func toPreconditionResult(result ir.ConditionResult) api.Condition {
	condition := toPrecondition(&result.Condition)
	condition.Error = ptrOf(result.Error)
	return condition
}

func toTriggerType(t ir.TriggerType) *api.TriggerType {
	if t == ir.TriggerTypeUnknown {
		return nil
	}
	return new(api.TriggerType(t.String()))
}

func toDAGRunConditions(status ir.Status, conditions []ir.DAGRunCondition) *[]api.DAGRunCondition {
	if status != ir.Queued || len(conditions) == 0 {
		return nil
	}

	result := make([]api.DAGRunCondition, 0, len(conditions))
	for _, condition := range conditions {
		checkedAt, err := time.Parse(time.RFC3339, condition.CheckedAt)
		if err != nil {
			continue
		}
		conditionStatus, ok := toDAGRunConditionStatus(condition.Status)
		if !ok {
			continue
		}
		result = append(result, api.DAGRunCondition{
			Type:      condition.Type,
			Status:    conditionStatus,
			Reason:    condition.Reason,
			Message:   condition.Message,
			CheckedAt: checkedAt,
		})
	}
	if len(result) == 0 {
		return nil
	}
	return &result
}

func toDAGRunConditionStatus(status string) (api.DAGRunConditionStatus, bool) {
	switch status {
	case string(api.DAGRunConditionStatusFalse):
		return api.DAGRunConditionStatusFalse, true
	case string(api.DAGRunConditionStatusTrue):
		return api.DAGRunConditionStatusTrue, true
	case string(api.DAGRunConditionStatusUnknown):
		return api.DAGRunConditionStatusUnknown, true
	default:
		var zero api.DAGRunConditionStatus
		return zero, false
	}
}

func toRuntimeProfileName(name string) *api.RuntimeProfileName {
	if name == "" {
		return nil
	}
	profileName := api.RuntimeProfileName(name)
	return &profileName
}

func toDAGRunSummary(s ir.DAGRunStatus) api.DAGRunSummary {
	var autoRetryLimit *int
	if s.AutoRetryLimit > 0 {
		autoRetryLimit = ptrOf(s.AutoRetryLimit)
	}
	artifactsAvailable := hasArtifactEntries(s.ArchiveDir)

	return api.DAGRunSummary{
		Name:               s.Name,
		DagRunId:           s.DAGRunID,
		Workspace:          workspaceResponseNameFromLabelStrings(s.Labels),
		Params:             ptrOf(s.Params),
		ProfileName:        toRuntimeProfileName(s.ProfileName),
		QueuedAt:           ptrOf(s.QueuedAt),
		AutoRetryCount:     s.AutoRetryCount,
		AutoRetryLimit:     autoRetryLimit,
		Conditions:         toDAGRunConditions(s.Status, s.Conditions),
		ScheduleTime:       ptrOf(s.ScheduleTime),
		StartedAt:          s.StartedAt,
		FinishedAt:         s.FinishedAt,
		ArtifactsAvailable: artifactsAvailable,
		NoReuse:            ptrOf(s.NoReuse),
		Status:             api.Status(s.Status),
		StatusLabel:        api.StatusLabel(s.Status.String()),
		WorkerId:           ptrOf(s.WorkerID),
		TriggerType:        toTriggerType(s.TriggerType),
		TriggerActor:       ptrOf(s.TriggerActor),
		Labels:             &s.Labels,
		Tags:               &s.Labels,
	}
}

func toDAGRunsPageResponse(page persis.DAGRunStatusPage) api.DAGRunsPageResponse {
	dagRuns := make([]api.DAGRunSummary, 0, len(page.Items))
	for _, item := range page.Items {
		if item == nil {
			continue
		}
		dagRuns = append(dagRuns, toDAGRunSummary(*item))
	}

	resp := api.DAGRunsPageResponse{
		DagRuns: dagRuns,
	}
	if page.NextCursor != "" {
		resp.NextCursor = &page.NextCursor
	}
	return resp
}

// ToDAGRunDetails converts a DAGRunStatus to its API representation.
// This function is exported for use by the SSE package.
func ToDAGRunDetails(s ir.DAGRunStatus) api.DAGRunDetails {
	preconditions := make([]api.Condition, len(s.Preconditions))
	for i, p := range s.Preconditions {
		preconditions[i] = toPreconditionResult(p)
	}
	nodes := make([]api.Node, len(s.Nodes))
	for i, n := range s.Nodes {
		nodes[i] = toNode(n)
	}

	var autoRetryLimit *int
	if s.AutoRetryLimit > 0 {
		autoRetryLimit = ptrOf(s.AutoRetryLimit)
	}
	artifactsAvailable := hasArtifactEntries(s.ArchiveDir)
	var humanTaskResumePending *bool
	if humantask.ResumePending(&s) {
		humanTaskResumePending = ptrOf(true)
	}

	return api.DAGRunDetails{
		AgentTasks:             agentTaskProgress(s.Nodes),
		AgentEvents:            agentTimeline(s.Nodes),
		RootDAGRunName:         s.Root.Name,
		RootDAGRunId:           s.Root.ID,
		ParentDAGRunName:       ptrOf(s.Parent.Name),
		ParentDAGRunId:         ptrOf(s.Parent.ID),
		ArtifactsAvailable:     artifactsAvailable,
		NoReuse:                ptrOf(s.NoReuse),
		Log:                    s.Log,
		Name:                   s.Name,
		Params:                 ptrOf(s.Params),
		DagRunId:               s.DAGRunID,
		Workspace:              workspaceResponseNameFromLabelStrings(s.Labels),
		ProfileName:            toRuntimeProfileName(s.ProfileName),
		QueuedAt:               ptrOf(s.QueuedAt),
		AutoRetryCount:         s.AutoRetryCount,
		AutoRetryLimit:         autoRetryLimit,
		Conditions:             toDAGRunConditions(s.Status, s.Conditions),
		ScheduleTime:           ptrOf(s.ScheduleTime),
		StartedAt:              s.StartedAt,
		FinishedAt:             s.FinishedAt,
		Status:                 api.Status(s.Status),
		StatusLabel:            api.StatusLabel(s.Status.String()),
		WorkerId:               ptrOf(s.WorkerID),
		HumanTaskResumePending: humanTaskResumePending,
		TriggerType:            toTriggerType(s.TriggerType),
		TriggerActor:           ptrOf(s.TriggerActor),
		Preconditions:          ptrOf(preconditions),
		Nodes:                  nodes,
		OnInit:                 ptrOf(toNode(s.OnInit)),
		OnSuccess:              ptrOf(toNode(s.OnSuccess)),
		OnFailure:              ptrOf(toNode(s.OnFailure)),
		OnAbort:                ptrOf(toNode(s.OnAbort)),
		OnExit:                 ptrOf(toNode(s.OnExit)),
		OnWait:                 ptrOf(toNode(s.OnWait)),
		Labels:                 &s.Labels,
		Tags:                   &s.Labels,
	}
}

func hasArtifactEntries(archiveDir string) bool {
	if archiveDir == "" {
		return false
	}

	info, err := os.Stat(archiveDir)
	if err != nil || !info.IsDir() {
		return false
	}

	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if !fileutil.IsSymlinkDirEntry(entry) {
			return true
		}
	}
	return false
}

func toNode(node *ir.Node) api.Node {
	if node == nil {
		return api.Node{}
	}
	step := toStep(node.Step)
	if node.PreconditionResults != nil {
		preconditions := make([]api.Condition, len(node.PreconditionResults))
		for i, condition := range node.PreconditionResults {
			preconditions[i] = toPreconditionResult(condition)
		}
		step.Preconditions = ptrOf(preconditions)
	}
	result := api.Node{
		DoneCount:              node.DoneCount,
		FinishedAt:             node.FinishedAt,
		Stdout:                 node.Stdout,
		Stderr:                 node.Stderr,
		RetryCount:             node.RetryCount,
		StartedAt:              node.StartedAt,
		Status:                 api.NodeStatus(node.Status),
		StatusLabel:            api.NodeStatusLabel(node.Status.String()),
		Step:                   step,
		Error:                  ptrOf(node.Error),
		SubRuns:                ptrOf(toSubDAGRuns(node.SubRuns)),
		SubRunsRepeated:        ptrOf(toSubDAGRuns(node.SubRunsRepeated)),
		HumanTaskCompletedBy:   ptrOf(node.HumanTaskCompletedBy),
		HumanTaskCompletedById: ptrOf(node.HumanTaskCompletedByID),
		ApprovedAt:             ptrOf(node.ApprovedAt),
		ApprovedBy:             ptrOf(node.ApprovedBy),
		ApprovedById:           ptrOf(node.ApprovedByID),
		ApprovalInputs:         ptrOf(node.ApprovalInputs),
		PushBackInputs:         ptrOf(node.PushBackInputs),
		PushBackHistory:        ptrOf(toPushBackHistory(node)),
		RejectedAt:             ptrOf(node.RejectedAt),
		RejectedBy:             ptrOf(node.RejectedBy),
		RejectedById:           ptrOf(node.RejectedByID),
		RejectionReason:        ptrOf(node.RejectionReason),
		ApprovalIteration:      ptrOf(node.ApprovalIteration),
	}
	if node.AgentSession != nil {
		result.AgentSession = toAgentSession(node.AgentSession)
	}
	if node.Build != nil {
		result.Build = &api.BuildExecution{
			Decision:           api.BuildExecutionDecision(node.Build.Decision),
			Phase:              api.BuildExecutionPhase(node.Build.Phase),
			Reason:             string(node.Build.Reason),
			Detail:             ptrOf(node.Build.Detail),
			Fingerprint:        ptrOf(node.Build.Fingerprint),
			MaterializationKey: ptrOf(node.Build.MaterializationKey),
			ProducerAttemptId:  ptrOf(node.Build.ProducerAttemptID),
		}
		if !node.Build.ProducerRun.Zero() {
			result.Build.ProducerRun = &api.BuildProducer{
				Name: ptrOf(node.Build.ProducerRun.Name),
				Id:   ptrOf(node.Build.ProducerRun.ID),
			}
		}
	}
	return result
}

func toAgentSession(session *ir.AgentSession) *api.AgentSession {
	if session == nil {
		return nil
	}
	interactions := make([]api.AgentInteraction, 0, len(session.Interactions))
	for _, interaction := range session.Interactions {
		questions := make([]api.AgentQuestion, 0, len(interaction.Questions))
		for _, question := range interaction.Questions {
			options := make([]api.AgentQuestionOption, 0, len(question.Options))
			for _, option := range question.Options {
				options = append(options, api.AgentQuestionOption{
					Label: option.Label, Description: ptrOf(option.Description),
				})
			}
			questions = append(questions, api.AgentQuestion{
				Header: question.Header, Question: question.Question,
				Options: ptrOf(options), Multiple: ptrOf(question.Multiple), Custom: ptrOf(question.Custom),
			})
		}
		interactions = append(interactions, api.AgentInteraction{
			Id: interaction.ID, Kind: api.AgentInteractionKind(interaction.Kind), Status: api.AgentInteractionStatus(interaction.Status),
			Permission: ptrOf(interaction.Permission), Patterns: ptrOf(interaction.Patterns), AllowForSessionPatterns: ptrOf(interaction.AllowForSessionPatterns),
			Questions: ptrOf(questions), Decision: ptrOf(interaction.Decision), Answers: ptrOf(interaction.Answers),
			CreatedAt: ptrOf(interaction.CreatedAt), RespondedAt: ptrOf(interaction.RespondedAt),
			RespondedBy: ptrOf(interaction.RespondedBy), RespondedById: ptrOf(interaction.RespondedByID),
		})
	}
	events := make([]api.AgentSessionEvent, 0, len(session.Events))
	for _, event := range session.Events {
		events = append(events, toAgentSessionEvent(event))
	}
	return &api.AgentSession{
		Provider: session.Provider, ProviderVersion: ptrOf(session.ProviderVersion), SessionId: ptrOf(session.SessionID), Generation: ptrOf(session.Generation),
		Agent: ptrOf(session.Agent), Model: ptrOf(session.Model), Variant: ptrOf(session.Variant),
		State:     api.AgentSessionState(session.State),
		LastError: ptrOf(session.LastError), Interactions: ptrOf(interactions), Events: ptrOf(events),
		Usage: &api.AgentUsage{
			InputTokens: ptrOf(session.Usage.InputTokens), OutputTokens: ptrOf(session.Usage.OutputTokens),
			ReasoningTokens: ptrOf(session.Usage.ReasoningTokens), TotalTokens: ptrOf(session.Usage.TotalTokens), Cost: ptrOf(session.Usage.Cost),
		},
	}
}

func toAgentSessionEvent(event ir.AgentSessionEvent) api.AgentSessionEvent {
	return api.AgentSessionEvent{
		Sequence: event.Sequence, Id: event.ID, Type: event.Type,
		Timestamp: ptrOf(event.Timestamp), Role: ptrOf(event.Role), Content: ptrOf(event.Content),
		Name: ptrOf(event.Name), Status: ptrOf(event.Status), Files: ptrOf(event.Files),
	}
}

func toPushBackHistory(node *ir.Node) []api.PushBackHistoryEntry {
	if node == nil {
		return nil
	}

	var allowedInputs []string
	if node.Step.Approval != nil {
		allowedInputs = node.Step.Approval.Input
	}
	history := dagrun.NormalizePushBackHistory(
		allowedInputs,
		node.ApprovalIteration,
		node.PushBackInputs,
		node.PushBackHistory,
	)
	if len(history) == 0 {
		return nil
	}

	items := make([]api.PushBackHistoryEntry, len(history))
	for i, entry := range history {
		items[i] = api.PushBackHistoryEntry{
			Iteration: entry.Iteration,
			By:        ptrOf(entry.By),
			ById:      ptrOf(entry.ByID),
			Inputs:    ptrOf(entry.Inputs),
		}
		if entry.At != "" {
			if at, err := time.Parse(time.RFC3339, entry.At); err == nil {
				items[i].At = &at
			}
		}
	}
	return items
}

func toSubDAGRuns(subDAGRuns []ir.SubDAGRun) []api.SubDAGRun {
	result := make([]api.SubDAGRun, len(subDAGRuns))
	for i, w := range subDAGRuns {
		result[i] = api.SubDAGRun{
			DagRunId: w.DAGRunID,
			Params:   ptrOf(w.Params),
			DagName:  ptrOf(w.DAGName),
		}
	}
	return result
}

func toLocalDAG(dag *ir.DAG) api.LocalDag {
	return api.LocalDag{
		Name:   dag.Name,
		Dag:    toDAGDetails(dag),
		Errors: []string{},
	}
}

func toDAGDetails(dag *ir.DAG) *api.DAGDetails {
	if dag == nil {
		return nil
	}

	steps := make([]api.Step, len(dag.Steps))
	for i, step := range dag.Steps {
		steps[i] = toStep(step)
	}

	handlerOn := toHandlerOn(dag.HandlerOn)

	schedules := make([]api.Schedule, len(dag.Schedule))
	for i, s := range dag.Schedule {
		schedules[i] = toSchedule(s)
	}

	preconditions := make([]api.Condition, len(dag.Preconditions))
	for i, p := range dag.Preconditions {
		preconditions[i] = toPrecondition(p)
	}

	var runConfig *api.RunConfig
	if dag.RunConfig != nil {
		runConfig = &api.RunConfig{
			DisableParamEdit: dag.RunConfig.DisableParamEdit,
			DisableRunIdEdit: dag.RunConfig.DisableRunIdEdit,
		}
	}

	var paramDefs *[]api.ParamDef
	if len(dag.ParamDefs) > 0 {
		defs := toParamDefs(dag.ParamDefs)
		paramDefs = ptrOf(defs)
	}

	paramSchema := toJSONObject(dag.ParamSchema)

	var artifacts *api.DAGArtifactsConfig
	if dag.Artifacts != nil {
		artifacts = &api.DAGArtifactsConfig{
			Enabled: dag.Artifacts.Enabled,
			Dir:     ptrOf(dag.Artifacts.Dir),
		}
	}

	return &api.DAGDetails{
		Type:              agentDAGType(dag.Type),
		Tasks:             declaredAgentTasks(dag),
		Artifacts:         artifacts,
		Name:              dag.Name,
		Description:       ptrOf(dag.Description),
		DefaultParams:     ptrOf(dag.DefaultParams),
		Delay:             ptrOf(int(dag.Delay.Seconds())),
		Env:               ptrOf(dag.Env),
		Group:             ptrOf(dag.Group),
		HandlerOn:         ptrOf(handlerOn),
		HistRetentionDays: ptrOf(dag.HistRetentionDays),
		HistRetentionRuns: ptrOf(dag.HistRetentionRuns),
		LogDir:            ptrOf(dag.LogDir),
		MaxActiveRuns:     ptrOf(dag.MaxActiveRuns),
		MaxActiveSteps:    ptrOf(dag.MaxActiveSteps),
		Params:            ptrOf(dag.Params),
		ParamDefs:         paramDefs,
		ParamSchema:       paramSchema,
		Preconditions:     ptrOf(preconditions),
		Resources:         toDAGResources(dag.Resources),
		Schedule:          ptrOf(schedules),
		Steps:             ptrOf(steps),
		Labels:            ptrOf(dag.Labels.Strings()),
		Tags:              ptrOf(dag.Labels.Strings()),
		RunConfig:         runConfig,
	}
}

// agentDAGType exposes the DAG execution type, which the UI uses to decide
// whether agent-specific views apply.
func agentDAGType(dagType string) *api.DAGDetailsType {
	if dagType == "" {
		return nil
	}
	return ptrOf(api.DAGDetailsType(dagType))
}

// declaredAgentTasks lists the goals an agent DAG declares, before any
// run has made progress against them.
func declaredAgentTasks(dag *ir.DAG) *[]api.AgentTask {
	if len(dag.Tasks) == 0 {
		return nil
	}
	tasks := make([]api.AgentTask, 0, len(dag.Tasks))
	for _, task := range dag.Tasks {
		tasks = append(tasks, api.AgentTask{
			Name:        task.Name,
			Description: ptrOf(task.Description),
			Status:      api.AgentTaskStatusOpen,
		})
	}
	return &tasks
}

// agentTimeline reports the ordered decisions an agent DAG-run made.
func agentTimeline(nodes []*ir.Node) *[]api.AgentEvent {
	for _, node := range nodes {
		if node == nil || !ir.IsPersistedAgentStepName(node.Step.Name) {
			continue
		}
		recorded := agentloop.EventsFromState(node.AgentState)
		if len(recorded) == 0 {
			return nil
		}
		events := make([]api.AgentEvent, 0, len(recorded))
		for _, e := range recorded {
			events = append(events, api.AgentEvent{
				Turn:          e.Turn,
				Kind:          api.AgentEventKind(e.Kind),
				Name:          ptrOf(e.Name),
				Status:        ptrOf(e.Status),
				Attempt:       ptrOf(e.Attempt),
				Reason:        ptrOf(e.Reason),
				StartedAt:     ptrOf(e.StartedAt),
				FinishedAt:    ptrOf(e.FinishedAt),
				ChildDagRunId: ptrOf(e.ChildDAGRunID),
				ChildDagName:  ptrOf(e.ChildDAGName),
			})
		}
		return &events
	}
	return nil
}

// agentTaskProgress reports goal progress recorded by the agent step
// of an agent DAG-run.
func agentTaskProgress(nodes []*ir.Node) *[]api.AgentTask {
	for _, node := range nodes {
		if node == nil || !ir.IsPersistedAgentStepName(node.Step.Name) {
			continue
		}
		states := agentloop.TasksFromState(node.AgentState)
		if len(states) == 0 {
			return nil
		}
		tasks := make([]api.AgentTask, 0, len(states))
		for _, state := range states {
			status := state.Status
			if status == "" {
				status = agentloop.TaskOpen
			}
			tasks = append(tasks, api.AgentTask{
				Name:        state.Name,
				Description: ptrOf(state.Description),
				Status:      api.AgentTaskStatus(status),
				Reason:      ptrOf(state.Reason),
			})
		}
		return &tasks
	}
	return nil
}

func toJSONObject(raw json.RawMessage) *map[string]any {
	if len(raw) == 0 {
		return nil
	}

	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		slog.Warn(
			"Failed to unmarshal DAG param schema produced by buildRenderableParamSchema",
			"error",
			err,
			"length",
			len(raw),
		)
		return nil
	}
	return &value
}

func toParamDefs(defs []ir.ParamDef) []api.ParamDef {
	result := make([]api.ParamDef, 0, len(defs))
	for _, def := range defs {
		paramDef := api.ParamDef{
			Type:     api.ParamDefType(def.Type),
			Required: ptrOf(def.Required),
		}
		if def.Name != "" {
			paramDef.Name = ptrOf(def.Name)
		}
		if def.Default != nil {
			value, ok := toParamScalar(def.Default)
			if ok {
				paramDef.Default = &value
			}
		}
		if def.Description != "" {
			paramDef.Description = ptrOf(def.Description)
		}
		if len(def.Enum) > 0 {
			enum := make([]api.ParamScalar, 0, len(def.Enum))
			for _, item := range def.Enum {
				value, ok := toParamScalar(item)
				if !ok {
					continue
				}
				enum = append(enum, value)
			}
			if len(enum) > 0 {
				paramDef.Enum = &enum
			}
		}
		if def.Minimum != nil {
			paramDef.Minimum = ptrOf(*def.Minimum)
		}
		if def.Maximum != nil {
			paramDef.Maximum = ptrOf(*def.Maximum)
		}
		if def.MinLength != nil {
			paramDef.MinLength = def.MinLength
		}
		if def.MaxLength != nil {
			paramDef.MaxLength = def.MaxLength
		}
		if def.Pattern != nil {
			paramDef.Pattern = def.Pattern
		}
		result = append(result, paramDef)
	}
	return result
}

func toParamScalar(value any) (api.ParamScalar, bool) {
	var scalar api.ParamScalar

	switch v := value.(type) {
	case string:
		return scalar, scalar.FromParamScalar0(v) == nil
	case bool:
		return scalar, scalar.FromParamScalar3(v) == nil
	case int:
		return scalar, scalar.FromParamScalar1(v) == nil
	case int8:
		return scalar, scalar.FromParamScalar1(int(v)) == nil
	case int16:
		return scalar, scalar.FromParamScalar1(int(v)) == nil
	case int32:
		return scalar, scalar.FromParamScalar1(int(v)) == nil
	case int64:
		return toParamScalarInt64(v)
	case uint:
		return toParamScalarUint64(uint64(v))
	case uint8:
		return scalar, scalar.FromParamScalar1(int(v)) == nil
	case uint16:
		return scalar, scalar.FromParamScalar1(int(v)) == nil
	case uint32:
		return toParamScalarUint64(uint64(v))
	case uint64:
		return toParamScalarUint64(v)
	case float32:
		return scalar, scalar.FromParamScalar2(float64(v)) == nil
	case float64:
		return scalar, scalar.FromParamScalar2(v) == nil
	default:
		return scalar, false
	}
}

func toParamScalarInt64(value int64) (api.ParamScalar, bool) {
	var scalar api.ParamScalar
	if value < -int64(maxIntValue)-1 || value > int64(maxIntValue) {
		return scalar, false
	}
	return scalar, scalar.FromParamScalar1(int(value)) == nil
}

func toParamScalarUint64(value uint64) (api.ParamScalar, bool) {
	var scalar api.ParamScalar
	if value > uint64(maxIntValue) {
		return scalar, false
	}
	return scalar, scalar.FromParamScalar1(int(value)) == nil
}

func toHandlerOn(handlers ir.HandlerOn) api.HandlerOn {
	handlerOn := api.HandlerOn{}
	if handlers.Failure != nil {
		handlerOn.Failure = ptrOf(toStep(*handlers.Failure))
	}
	if handlers.Success != nil {
		handlerOn.Success = ptrOf(toStep(*handlers.Success))
	}
	if handlers.Abort != nil {
		handlerOn.Abort = ptrOf(toStep(*handlers.Abort))
	}
	if handlers.Exit != nil {
		handlerOn.Exit = ptrOf(toStep(*handlers.Exit))
	}
	return handlerOn
}

func toChatMessages(messages []ir.LLMMessage) []api.ChatMessage {
	if messages == nil {
		return []api.ChatMessage{}
	}

	result := make([]api.ChatMessage, len(messages))
	for i, msg := range messages {
		result[i] = toChatMessage(msg)
	}
	return result
}

func toChatMessage(msg ir.LLMMessage) api.ChatMessage {
	apiMsg := api.ChatMessage{
		Role:    api.ChatMessageRole(msg.Role),
		Content: msg.Content,
	}

	if len(msg.ToolCalls) > 0 {
		toolCalls := make([]api.ChatToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			toolCalls[i] = api.ChatToolCall{
				Id:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: ptrOf(tc.Function.Arguments),
			}
		}
		apiMsg.ToolCalls = &toolCalls
	}

	if msg.Metadata != nil {
		apiMsg.Metadata = &api.ChatMessageMetadata{
			Provider:         ptrOf(msg.Metadata.Provider),
			Model:            ptrOf(msg.Metadata.Model),
			PromptTokens:     ptrOf(msg.Metadata.PromptTokens),
			CompletionTokens: ptrOf(msg.Metadata.CompletionTokens),
			TotalTokens:      ptrOf(msg.Metadata.TotalTokens),
		}
	}

	return apiMsg
}

func toToolDefinitions(defs []ir.ToolDefinition) *[]api.ToolDefinition {
	if len(defs) == 0 {
		return nil
	}

	result := make([]api.ToolDefinition, len(defs))
	for i, def := range defs {
		result[i] = api.ToolDefinition{
			Name:        def.Name,
			Description: ptrOf(def.Description),
			Parameters:  ptrOf(def.Parameters),
		}
	}

	return &result
}
