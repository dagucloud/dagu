// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"os"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const maxIntValue = int(^uint(0) >> 1)

func toSchedule(s core.Schedule) api.Schedule {
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
	return schedule
}

func workspaceResponseNameFromLabels(labels core.Labels) *string {
	workspaceName, ok := exec.WorkspaceNameFromLabels(labels)
	if !ok {
		return nil
	}
	return ptrOf(workspaceName)
}

func workspaceResponseNameFromLabelStrings(labels []string) *string {
	return workspaceResponseNameFromLabels(core.NewLabels(labels))
}

func toDAG(dag *core.DAG) api.DAG {
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
		Labels:        ptrOf(dag.Labels.Strings()),
		Tags:          ptrOf(dag.Labels.Strings()),
		Schedule:      ptrOf(schedules),
	}
}

func toStep(obj core.Step) api.Step {
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

func toPrecondition(obj *core.Condition) api.Condition {
	return api.Condition{
		Condition: obj.Condition,
		Expected:  ptrOf(obj.Expected),
		Negate:    ptrOf(obj.Negate),
		Error:     ptrOf(obj.GetErrorMessage()),
	}
}

func toTriggerType(t core.TriggerType) *api.TriggerType {
	if t == core.TriggerTypeUnknown {
		return nil
	}
	return new(api.TriggerType(t.String()))
}

func toDAGRunSummary(s exec.DAGRunStatus) api.DAGRunSummary {
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
		QueuedAt:           ptrOf(s.QueuedAt),
		AutoRetryCount:     s.AutoRetryCount,
		AutoRetryLimit:     autoRetryLimit,
		ScheduleTime:       ptrOf(s.ScheduleTime),
		StartedAt:          s.StartedAt,
		FinishedAt:         s.FinishedAt,
		ArtifactsAvailable: artifactsAvailable,
		Status:             api.Status(s.Status),
		StatusLabel:        api.StatusLabel(s.Status.String()),
		WorkerId:           ptrOf(s.WorkerID),
		TriggerType:        toTriggerType(s.TriggerType),
		Labels:             &s.Labels,
		Tags:               &s.Labels,
	}
}

func toDAGRunsPageResponse(page exec.DAGRunStatusPage) api.DAGRunsPageResponse {
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
func ToDAGRunDetails(s exec.DAGRunStatus) api.DAGRunDetails {
	preconditions := make([]api.Condition, len(s.Preconditions))
	for i, p := range s.Preconditions {
		preconditions[i] = toPrecondition(p)
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

	return api.DAGRunDetails{
		RootDAGRunName:     s.Root.Name,
		RootDAGRunId:       s.Root.ID,
		ParentDAGRunName:   ptrOf(s.Parent.Name),
		ParentDAGRunId:     ptrOf(s.Parent.ID),
		ArtifactsAvailable: artifactsAvailable,
		Log:                s.Log,
		Name:               s.Name,
		Params:             ptrOf(s.Params),
		DagRunId:           s.DAGRunID,
		Workspace:          workspaceResponseNameFromLabelStrings(s.Labels),
		QueuedAt:           ptrOf(s.QueuedAt),
		AutoRetryCount:     s.AutoRetryCount,
		AutoRetryLimit:     autoRetryLimit,
		ScheduleTime:       ptrOf(s.ScheduleTime),
		StartedAt:          s.StartedAt,
		FinishedAt:         s.FinishedAt,
		Status:             api.Status(s.Status),
		StatusLabel:        api.StatusLabel(s.Status.String()),
		WorkerId:           ptrOf(s.WorkerID),
		TriggerType:        toTriggerType(s.TriggerType),
		Preconditions:      ptrOf(preconditions),
		Nodes:              nodes,
		OnSuccess:          ptrOf(toNode(s.OnSuccess)),
		OnFailure:          ptrOf(toNode(s.OnFailure)),
		OnAbort:            ptrOf(toNode(s.OnAbort)),
		OnExit:             ptrOf(toNode(s.OnExit)),
		Labels:             &s.Labels,
		Tags:               &s.Labels,
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

func toNode(node *exec.Node) api.Node {
	if node == nil {
		return api.Node{}
	}
	return api.Node{
		DoneCount:         node.DoneCount,
		FinishedAt:        node.FinishedAt,
		Stdout:            node.Stdout,
		Stderr:            node.Stderr,
		RetryCount:        node.RetryCount,
		StartedAt:         node.StartedAt,
		Status:            api.NodeStatus(node.Status),
		StatusLabel:       api.NodeStatusLabel(node.Status.String()),
		Step:              toStep(node.Step),
		Error:             ptrOf(node.Error),
		SubRuns:           ptrOf(toSubDAGRuns(node.SubRuns)),
		SubRunsRepeated:   ptrOf(toSubDAGRuns(node.SubRunsRepeated)),
		ApprovedAt:        ptrOf(node.ApprovedAt),
		ApprovedBy:        ptrOf(node.ApprovedBy),
		ApprovalInputs:    ptrOf(node.ApprovalInputs),
		PushBackInputs:    ptrOf(node.PushBackInputs),
		PushBackHistory:   ptrOf(toPushBackHistory(node)),
		RejectedAt:        ptrOf(node.RejectedAt),
		RejectedBy:        ptrOf(node.RejectedBy),
		RejectionReason:   ptrOf(node.RejectionReason),
		ApprovalIteration: ptrOf(node.ApprovalIteration),
	}
}

func toPushBackHistory(node *exec.Node) []api.PushBackHistoryEntry {
	if node == nil {
		return nil
	}

	var allowedInputs []string
	if node.Step.Approval != nil {
		allowedInputs = node.Step.Approval.Input
	}
	history := exec.NormalizePushBackHistory(
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
			At:        ptrOf(entry.At),
			Inputs:    ptrOf(entry.Inputs),
		}
	}
	return items
}

func toSubDAGRuns(subDAGRuns []exec.SubDAGRun) []api.SubDAGRun {
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

func toLocalDAG(dag *core.DAG) api.LocalDag {
	return api.LocalDag{
		Name:   dag.Name,
		Dag:    toDAGDetails(dag),
		Errors: []string{},
	}
}

func toDAGDetails(dag *core.DAG) *api.DAGDetails {
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

	var artifacts *api.DAGArtifactsConfig
	if dag.Artifacts != nil {
		artifacts = &api.DAGArtifactsConfig{
			Enabled: dag.Artifacts.Enabled,
			Dir:     ptrOf(dag.Artifacts.Dir),
		}
	}

	return &api.DAGDetails{
		Artifacts:         artifacts,
		Name:              dag.Name,
		Description:       ptrOf(dag.Description),
		DefaultParams:     ptrOf(dag.DefaultParams),
		Delay:             ptrOf(int(dag.Delay.Seconds())),
		Env:               ptrOf(dag.Env),
		Group:             ptrOf(dag.Group),
		HandlerOn:         ptrOf(handlerOn),
		HistRetentionDays: ptrOf(dag.HistRetentionDays),
		LogDir:            ptrOf(dag.LogDir),
		MaxActiveRuns:     ptrOf(dag.MaxActiveRuns),
		MaxActiveSteps:    ptrOf(dag.MaxActiveSteps),
		Params:            ptrOf(dag.Params),
		ParamDefs:         paramDefs,
		Preconditions:     ptrOf(preconditions),
		Schedule:          ptrOf(schedules),
		Steps:             ptrOf(steps),
		Labels:            ptrOf(dag.Labels.Strings()),
		Tags:              ptrOf(dag.Labels.Strings()),
		RunConfig:         runConfig,
	}
}

func toParamDefs(defs []core.ParamDef) []api.ParamDef {
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

func toHandlerOn(handlers core.HandlerOn) api.HandlerOn {
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

func toChatMessages(messages []exec.LLMMessage) []api.ChatMessage {
	if messages == nil {
		return []api.ChatMessage{}
	}

	result := make([]api.ChatMessage, len(messages))
	for i, msg := range messages {
		result[i] = toChatMessage(msg)
	}
	return result
}

func toChatMessage(msg exec.LLMMessage) api.ChatMessage {
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

func toToolDefinitions(defs []exec.ToolDefinition) *[]api.ToolDefinition {
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
