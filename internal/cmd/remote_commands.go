// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func toCoreDAG(name string) *core.DAG {
	return &core.DAG{Name: name}
}

func toExecStatus(detail *api.DAGRunDetails) (*exec.DAGRunStatus, error) {
	if detail == nil {
		return nil, fmt.Errorf("remote DAG run details are empty")
	}
	status := &exec.DAGRunStatus{
		Name:         detail.Name,
		DAGRunID:     detail.DagRunId,
		Status:       core.Status(detail.Status),
		QueuedAt:     derefString(detail.QueuedAt),
		ScheduleTime: derefString(detail.ScheduleTime),
		StartedAt:    detail.StartedAt,
		FinishedAt:   detail.FinishedAt,
		Log:          detail.Log,
		Params:       derefString(detail.Params),
		WorkerID:     derefString(detail.WorkerId),
		Labels:       labelsFromAPI(detail.Labels, detail.Tags),
		Nodes:        make([]*exec.Node, 0, len(detail.Nodes)),
	}
	status.Root = exec.NewDAGRunRef(detail.RootDAGRunName, detail.RootDAGRunId)
	if detail.ParentDAGRunName != nil && detail.ParentDAGRunId != nil {
		status.Parent = exec.NewDAGRunRef(*detail.ParentDAGRunName, *detail.ParentDAGRunId)
	}
	for _, node := range detail.Nodes {
		status.Nodes = append(status.Nodes, mapAPINode(node))
	}
	status.OnExit = mapAPINodePtr(detail.OnExit)
	status.OnSuccess = mapAPINodePtr(detail.OnSuccess)
	status.OnFailure = mapAPINodePtr(detail.OnFailure)
	status.OnAbort = mapAPINodePtr(detail.OnAbort)
	return status, nil
}

func mapAPINodePtr(node *api.Node) *exec.Node {
	if node == nil {
		return nil
	}
	return mapAPINode(*node)
}

func mapAPINode(node api.Node) *exec.Node {
	return &exec.Node{
		Step:       mapAPIStep(node.Step),
		Stdout:     node.Stdout,
		Stderr:     node.Stderr,
		StartedAt:  node.StartedAt,
		FinishedAt: node.FinishedAt,
		Status:     core.NodeStatus(node.Status),
		RetryCount: node.RetryCount,
		DoneCount:  node.DoneCount,
		Error:      derefString(node.Error),
		SubRuns:    mapAPISubRuns(node.SubRuns),
	}
}

func mapAPISubRuns(subRuns *[]api.SubDAGRun) []exec.SubDAGRun {
	if subRuns == nil {
		return nil
	}
	out := make([]exec.SubDAGRun, 0, len(*subRuns))
	for _, sub := range *subRuns {
		out = append(out, exec.SubDAGRun{
			DAGRunID: sub.DagRunId,
			Params:   derefString(sub.Params),
			DAGName:  derefString(sub.DagName),
		})
	}
	return out
}

func mapAPIStep(step api.Step) core.Step {
	mapped := core.Step{
		Name:        step.Name,
		Description: derefString(step.Description),
		Dir:         derefString(step.Dir),
		Script:      derefString(step.Script),
		Stdout:      derefString(step.Stdout),
		Stderr:      derefString(step.Stderr),
		Output:      derefString(step.Output),
		Depends:     derefStringSlice(step.Depends),
		MailOnError: derefBool(step.MailOnError),
	}
	if step.Id != nil {
		mapped.ID = *step.Id
	}
	if step.ExecutorConfig != nil {
		mapped.ExecutorConfig = core.ExecutorConfig{
			Type:   derefString(step.ExecutorConfig.Type),
			Config: derefMap(step.ExecutorConfig.Config),
		}
	}
	if step.Call != nil {
		mapped.SubDAG = &core.SubDAG{
			Name: *step.Call,
		}
	}
	if step.Params != nil {
		mapped.Params = core.NewRawParams([]byte(*step.Params))
	}
	if step.Commands != nil {
		mapped.Commands = make([]core.CommandEntry, 0, len(*step.Commands))
		for _, cmd := range *step.Commands {
			entry := core.CommandEntry{Command: cmd.Command}
			if cmd.Args != nil {
				entry.Args = append([]string{}, (*cmd.Args)...)
			}
			mapped.Commands = append(mapped.Commands, entry)
		}
	}
	return mapped
}

func validateRemoteStartLikeFlags(ctx *Context) error {
	disallowed := []string{"parent", "root", "worker-id", "attempt-id", "schedule-time"}
	for _, flag := range disallowed {
		if ctx.Command.Flags().Changed(flag) {
			return fmt.Errorf("--%s is only supported in the local context", flag)
		}
	}
	if ctx.Command.Flags().Changed("trigger-type") {
		triggerType, err := ctx.StringParam("trigger-type")
		if err != nil {
			return err
		}
		if triggerType != "" && triggerType != "manual" {
			return fmt.Errorf("--trigger-type=%s is only supported in the local context", triggerType)
		}
	}
	return nil
}

func remoteResolveDAG(ctx *Context, arg string) (*api.DAGFile, error) {
	return ctx.Remote.resolveDAG(ctx, arg)
}

func remoteRunStart(ctx *Context, args []string) error {
	if err := validateRemoteStartLikeFlags(ctx); err != nil {
		return err
	}
	fromRunID, err := ctx.StringParam("from-run-id")
	if err != nil {
		return err
	}
	if fromRunID != "" {
		if err := validateRunID(fromRunID); err != nil {
			return fmt.Errorf("invalid from-run-id: %w", err)
		}
		if len(args) != 1 || ctx.Command.Flags().Changed("params") || ctx.Command.ArgsLenAtDash() != -1 {
			return fmt.Errorf("parameters cannot be provided when using --from-run-id")
		}
		dag, err := remoteResolveDAG(ctx, args[0])
		if err != nil {
			return err
		}
		nameOverride, _ := ctx.StringParam("name")
		resp, err := ctx.Remote.rescheduleDAGRun(ctx, dag.Dag.Name, fromRunID, api.RescheduleDAGRunJSONBody{
			DagName:  stringPtrOrNil(nameOverride),
			DagRunId: nil,
		})
		if err != nil {
			return err
		}
		fmt.Println(resp.DagRunId)
		return nil
	}

	if err := validateStartArgumentSeparator(ctx, args); err != nil {
		return err
	}
	dag, err := remoteResolveDAG(ctx, args[0])
	if err != nil {
		return err
	}
	params := ""
	if ctx.Command.ArgsLenAtDash() >= 0 {
		params = joinNonEmpty(args[1:])
	}
	if flagParams, _ := ctx.StringParam("params"); flagParams != "" {
		params = flagParams
	}
	nameOverride, _ := ctx.StringParam("name")
	runID, _ := ctx.StringParam("run-id")
	if runID != "" {
		if err := validateRunID(runID); err != nil {
			return fmt.Errorf("invalid run-id: %w", err)
		}
	}
	labels, err := remoteLabelsFromFlag(ctx)
	if err != nil {
		return err
	}
	resp, err := ctx.Remote.startDAG(ctx, dag.FileName, api.ExecuteDAGJSONBody{
		DagName:  stringPtrOrNil(nameOverride),
		DagRunId: stringPtrOrNil(runID),
		Params:   stringPtrOrNil(params),
		Labels:   labels,
	})
	if err != nil {
		return err
	}
	fmt.Println(resp.DagRunId)
	return nil
}

func remoteRunEnqueue(ctx *Context, args []string) error {
	if err := validateRemoteStartLikeFlags(ctx); err != nil {
		return err
	}
	dag, err := remoteResolveDAG(ctx, args[0])
	if err != nil {
		return err
	}
	params := ""
	if ctx.Command.ArgsLenAtDash() >= 0 {
		params = joinNonEmpty(args[1:])
	}
	if flagParams, _ := ctx.StringParam("params"); flagParams != "" {
		params = flagParams
	}
	nameOverride, _ := ctx.StringParam("name")
	runID, _ := ctx.StringParam("run-id")
	if runID != "" {
		if err := validateRunID(runID); err != nil {
			return fmt.Errorf("invalid run-id: %w", err)
		}
	}
	queueOverride, _ := ctx.StringParam("queue")
	labels, err := remoteLabelsFromFlag(ctx)
	if err != nil {
		return err
	}
	resp, err := ctx.Remote.enqueueDAG(ctx, dag.FileName, api.EnqueueDAGDAGRunJSONBody{
		DagName:  stringPtrOrNil(nameOverride),
		DagRunId: stringPtrOrNil(runID),
		Params:   stringPtrOrNil(params),
		Queue:    stringPtrOrNil(queueOverride),
		Labels:   labels,
	})
	if err != nil {
		return err
	}
	fmt.Println(resp.DagRunId)
	return nil
}

func remoteRunStatus(ctx *Context, args []string) error {
	subRunID, _ := ctx.StringParam("sub-run-id")
	if subRunID != "" {
		return fmt.Errorf("--sub-run-id is not supported for remote contexts")
	}
	dag, err := remoteResolveDAG(ctx, args[0])
	if err != nil {
		return err
	}
	runID, _ := ctx.StringParam("run-id")
	if runID == "" {
		runID = "latest"
	}
	detail, err := ctx.Remote.getDAGRunDetails(ctx, dag.Dag.Name, runID)
	if err != nil {
		return err
	}
	coreDAG := toCoreDAG(dag.Dag.Name)
	status, err := toExecStatus(detail)
	if err != nil {
		return err
	}
	displayTreeStatus(coreDAG, status)
	return nil
}

func remoteRunHistory(ctx *Context, args []string) error {
	format, err := ctx.StringParam("format")
	if err != nil {
		return err
	}
	if err := validateFormat(format); err != nil {
		return err
	}
	query, limit, err := buildRemoteHistoryQuery(ctx, args)
	if err != nil {
		return err
	}
	runs, err := ctx.Remote.listDAGRuns(ctx, query)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return renderEmptyHistory(format)
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}
	statuses := make([]*exec.DAGRunStatus, 0, len(runs))
	for _, run := range runs {
		status := &exec.DAGRunStatus{
			Name:         run.Name,
			DAGRunID:     run.DagRunId,
			Status:       core.Status(run.Status),
			StartedAt:    run.StartedAt,
			FinishedAt:   run.FinishedAt,
			QueuedAt:     derefString(run.QueuedAt),
			ScheduleTime: derefString(run.ScheduleTime),
			Params:       derefString(run.Params),
			Labels:       labelsFromAPI(run.Labels, run.Tags),
			WorkerID:     derefString(run.WorkerId),
		}
		if format == "json" {
			detail, err := ctx.Remote.getDAGRunDetails(ctx, run.Name, run.DagRunId)
			if err != nil {
				return err
			}
			if err := enrichRemoteHistoryStatus(status, detail); err != nil {
				return err
			}
		}
		statuses = append(statuses, status)
	}
	return renderHistory(format, statuses)
}

func remoteRunStop(ctx *Context, args []string) error {
	dag, err := remoteResolveDAG(ctx, args[0])
	if err != nil {
		return err
	}
	runID, _ := ctx.StringParam("run-id")
	if runID != "" {
		if err := validateRunID(runID); err != nil {
			return fmt.Errorf("invalid run-id: %w", err)
		}
		return ctx.Remote.stopDAGRun(ctx, dag.Dag.Name, runID)
	}
	return ctx.Remote.stopAllDAGRuns(ctx, dag.FileName)
}

func remoteRunRetry(ctx *Context, args []string) error {
	runID, _ := ctx.StringParam("run-id")
	if err := validateRunID(runID); err != nil {
		return fmt.Errorf("invalid run-id: %w", err)
	}
	stepName, _ := ctx.StringParam("step")
	dag, err := remoteResolveDAG(ctx, args[0])
	if err != nil {
		return err
	}
	return ctx.Remote.retryDAGRun(ctx, dag.Dag.Name, runID, api.RetryDAGRunJSONBody{
		DagRunId: runID,
		StepName: stringPtrOrNil(stepName),
	})
}

func remoteRunRestart(ctx *Context, args []string) error {
	dag, err := remoteResolveDAG(ctx, args[0])
	if err != nil {
		return err
	}
	runID, _ := ctx.StringParam("run-id")
	if runID == "" {
		runID = "latest"
	} else if err := validateRunID(runID); err != nil {
		return fmt.Errorf("invalid run-id: %w", err)
	}
	detail, err := ctx.Remote.getDAGRunDetails(ctx, dag.Dag.Name, runID)
	if err != nil {
		return err
	}
	if core.Status(detail.Status) != core.Running {
		return fmt.Errorf("DAG %s is not running, current status: %s", dag.Dag.Name, core.Status(detail.Status))
	}
	if err := ctx.Remote.stopDAGRun(ctx, dag.Dag.Name, detail.DagRunId); err != nil {
		return err
	}
	if err := waitForRemoteStop(ctx, dag.Dag.Name, detail.DagRunId); err != nil {
		return err
	}
	resp, err := ctx.Remote.rescheduleDAGRun(ctx, dag.Dag.Name, detail.DagRunId, api.RescheduleDAGRunJSONBody{})
	if err != nil {
		return err
	}
	fmt.Println(resp.DagRunId)
	return nil
}

func remoteRunDequeue(ctx *Context, args []string) error {
	queueName := args[0]
	dagRunRef, _ := ctx.StringParam("dag-run")
	if dagRunRef != "" {
		ref, err := exec.ParseDAGRunRef(dagRunRef)
		if err != nil {
			return err
		}
		return ctx.Remote.dequeueDAGRun(ctx, ref.Name, ref.ID)
	}
	items, err := ctx.Remote.listQueueItems(ctx, queueName, 1, "")
	if err != nil {
		return err
	}
	if len(items.Items) == 0 {
		return fmt.Errorf("no dag-run found in queue %s", queueName)
	}
	item := items.Items[0]
	return ctx.Remote.dequeueDAGRun(ctx, item.Name, item.DagRunId)
}

func remoteLabelsFromFlag(ctx *Context) (*api.Labels, error) {
	labelsStr, err := labelsParam(ctx)
	if err != nil {
		return nil, err
	}
	if labelsStr == "" {
		return nil, nil
	}
	labels := core.NewLabels(parseLabels(labelsStr))
	if err := core.ValidateLabels(labels); err != nil {
		return nil, fmt.Errorf("invalid labels: %w", err)
	}
	labelStrings := labels.Strings()
	converted := make(api.Labels, len(labelStrings))
	copy(converted, labelStrings)
	return &converted, nil
}

func buildRemoteHistoryQuery(ctx *Context, args []string) (remoteHistoryQuery, int, error) {
	var query remoteHistoryQuery
	limit := 100
	if len(args) > 0 {
		if isLikelyLocalDAGArg(args[0]) {
			return query, 0, fmt.Errorf("remote history requires a deployed DAG name, not a local YAML path")
		}
		query.Name = args[0]
	}
	lastDuration, _ := ctx.StringParam("last")
	fromDate, _ := ctx.StringParam("from")
	toDate, _ := ctx.StringParam("to")
	if lastDuration != "" && (fromDate != "" || toDate != "") {
		return query, 0, fmt.Errorf("cannot use --last with --from or --to (conflicting time range specifications)")
	}
	if lastDuration != "" {
		d, err := parseRelativeDuration(lastDuration)
		if err != nil {
			return query, 0, err
		}
		from := time.Now().UTC().Add(-d).Unix()
		query.From = &from
	}
	if fromDate != "" {
		t, err := parseAbsoluteDateTime(fromDate)
		if err != nil {
			return query, 0, err
		}
		from := t.Unix()
		query.From = &from
	}
	if toDate != "" {
		t, err := parseAbsoluteDateTime(toDate)
		if err != nil {
			return query, 0, err
		}
		to := t.Unix()
		query.To = &to
	}
	statusValue, _ := ctx.StringParam("status")
	if statusValue != "" {
		statuses, err := remoteStatusValues(statusValue)
		if err != nil {
			return query, 0, err
		}
		query.Statuses = statuses
	}
	runID, _ := ctx.StringParam("run-id")
	query.RunID = runID
	labelsStr, err := labelsParam(ctx)
	if err != nil {
		return query, 0, err
	}
	query.Labels = parseLabels(labelsStr)
	limitStr, _ := ctx.StringParam("limit")
	if limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil {
			return query, 0, fmt.Errorf("invalid --limit value %q: must be an integer", limitStr)
		}
		if parsed <= 0 {
			return query, 0, fmt.Errorf("--limit must be greater than 0")
		}
		limit = parsed
	}
	return query, limit, nil
}

func remoteStatusValue(s string) (int, error) {
	if strings.EqualFold(strings.TrimSpace(s), "none") {
		return 0, fmt.Errorf("status %q is not supported in remote history", s)
	}

	status, err := parseStatus(s)
	if err != nil {
		return 0, err
	}
	return int(status), nil
}

func remoteStatusValues(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	statuses := make([]int, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		status, err := remoteStatusValue(trimmed)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	if len(statuses) == 0 {
		return nil, fmt.Errorf("invalid status %q", s)
	}
	return statuses, nil
}

func waitForRemoteStop(ctx *Context, name, dagRunID string) error {
	timeout := defaultRemoteTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	} else if ctx.Remote != nil && ctx.Remote.client != nil && ctx.Remote.client.Timeout > 0 {
		timeout = ctx.Remote.client.Timeout
	}
	if timeout <= 0 {
		timeout = defaultRemoteTimeout
	}

	timer := time.NewTimer(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("timed out waiting for remote DAG run %s to stop", dagRunID)
		case <-ticker.C:
			detail, err := ctx.Remote.getDAGRunDetails(ctx, name, dagRunID)
			if err != nil {
				return err
			}
			if core.Status(detail.Status) != core.Running {
				return nil
			}
		}
	}
}

func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func derefBool(v *bool) bool {
	return v != nil && *v
}

func derefMap(v *map[string]any) map[string]any {
	if v == nil {
		return nil
	}
	out := make(map[string]any, len(*v))
	maps.Copy(out, *v)
	return out
}

func derefStringSlice(v *[]string) []string {
	if v == nil {
		return nil
	}
	return append([]string{}, (*v)...)
}

func joinNonEmpty(parts []string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, " ")
}

func enrichRemoteHistoryStatus(status *exec.DAGRunStatus, detail *api.DAGRunDetails) error {
	remoteStatus, err := toExecStatus(detail)
	if err != nil {
		return err
	}
	status.Labels = remoteStatus.Labels
	status.WorkerID = remoteStatus.WorkerID
	if errs := remoteStatus.Errors(); len(errs) > 0 {
		status.Error = errs[0].Error()
	}
	return nil
}

func labelsFromAPI(labels, deprecatedTags *[]string) []string {
	if labels != nil {
		return derefStringSlice(labels)
	}
	return derefStringSlice(deprecatedTags)
}
