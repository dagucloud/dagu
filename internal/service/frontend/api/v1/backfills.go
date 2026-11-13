// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

const (
	defaultBackfillSlotPage = 100
	maxBackfillSlotPage     = 1000
)

func (a *API) SubmitBackfill(ctx context.Context, request api.SubmitBackfillRequestObject) (api.SubmitBackfillResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	store, err := a.requireBackfillStore()
	if err != nil {
		return nil, err
	}
	dag, sched, err := a.backfillDAG(ctx, request.FileName, valueOf(request.Body.Profile))
	if err != nil {
		return nil, err
	}
	body := request.Body
	dryRun := valueOf(body.DryRun)
	if !dryRun && !a.config.Queues.Enabled {
		return nil, badRequest("a backfill runs through the DAG's queue; set queues.enabled to true")
	}
	breq := backfill.Request{
		From:           valueOf(body.From),
		To:             valueOf(body.To),
		Rerun:          string(valueOf(body.Rerun)),
		MaxConcurrency: valueOf(body.MaxConcurrency),
		OnFailure:      string(valueOf(body.OnFailure)),
		Params:         valueOf(body.Params),
		Profile:        valueOf(body.Profile),
	}
	if body.Slots != nil {
		breq.Slots = *body.Slots
	}
	planReq, options, err := breq.Resolve(dag, sched, a.config.Scheduler.BackfillMaxSlots, time.Now())
	if err != nil {
		return nil, backfillError(err)
	}
	result, err := a.backfillService(store).Submit(ctx, backfill.SubmitRequest{
		Plan:     planReq,
		Options:  options,
		DAGFile:  dag.Location,
		Actor:    triggerActorFromContext(ctx),
		DryRun:   dryRun,
		Revision: valueOf(body.Revision),
	})
	response, respErr := a.backfillSubmitResponse(ctx, store, dag, options.MaxConcurrency, result)
	if respErr != nil {
		return nil, respErr
	}
	if errors.Is(err, backfill.ErrStalePlan) {
		return api.SubmitBackfill409JSONResponse(response), nil
	}
	if err != nil {
		return nil, backfillError(err)
	}
	if result.Record != nil && !result.Existing && !dryRun {
		a.logAudit(ctx, audit.CategoryDAG, "backfill_submit", map[string]any{
			"dag_name":    dag.Name,
			"backfill_id": result.Record.ID,
			"slots":       len(result.Record.Slots),
		})
	}
	return api.SubmitBackfill200JSONResponse(response), nil
}

func (a *API) ListDAGSlots(ctx context.Context, request api.ListDAGSlotsRequestObject) (api.ListDAGSlotsResponseObject, error) {
	dag, sched, err := a.backfillDAG(ctx, request.FileName, "")
	if err != nil {
		return nil, err
	}
	planReq, _, err := backfill.Request{From: request.Params.From, To: valueOf(request.Params.To)}.Resolve(dag, sched, a.config.Scheduler.BackfillMaxSlots, time.Now())
	if err != nil {
		return nil, backfillError(err)
	}
	rng, err := backfill.SlotsOf(planReq)
	if err != nil {
		return nil, backfillError(err)
	}
	slots := make([]api.DAGSlot, 0, len(rng.Slots))
	if len(rng.Slots) > 0 {
		history, err := backfill.LoadHistory(ctx, a.dagRunRepository, dag, sched, rng.Slots[0], rng.Slots[len(rng.Slots)-1])
		if err != nil {
			return nil, err
		}
		for _, state := range backfill.SlotStates(rng.Slots, sched, history) {
			window := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, state.At)
			slots = append(slots, api.DAGSlot{
				Slot:        state.At,
				WindowStart: timeOrNil(window.Start),
				Status:      dagSlotStatus(state.Status),
				RunId:       stringOrNil(state.RunID),
				CoveredBy:   timeOrNil(state.CoveredAt),
			})
		}
	}
	return api.ListDAGSlots200JSONResponse{Slots: slots}, nil
}

func (a *API) ListBackfills(ctx context.Context, request api.ListBackfillsRequestObject) (api.ListBackfillsResponseObject, error) {
	store, err := a.requireBackfillStore()
	if err != nil {
		return nil, err
	}
	records, err := store.List(ctx, valueOf(request.Params.Dag))
	if err != nil {
		return nil, err
	}
	backfills := make([]api.Backfill, 0, len(records))
	running := a.schedulerActive(ctx)
	for _, record := range records {
		if !a.canAccessWorkspace(ctx, backfillWorkspace(record)) {
			continue
		}
		report, err := backfill.Describe(ctx, store, a.readRun, record)
		if err != nil {
			return nil, err
		}
		backfills = append(backfills, toAPIBackfill(report, running))
	}
	return api.ListBackfills200JSONResponse{Backfills: backfills}, nil
}

func (a *API) GetBackfill(ctx context.Context, request api.GetBackfillRequestObject) (api.GetBackfillResponseObject, error) {
	item, err := a.backfillDetails(ctx, request.BackfillId)
	if err != nil {
		return nil, err
	}
	return api.GetBackfill200JSONResponse(item), nil
}

// GetBackfillData returns a backfill for SSE.
// Identifier format: the backfill ID.
func (a *API) GetBackfillData(ctx context.Context, id string) (any, error) {
	return a.backfillDetails(ctx, id)
}

// backfillDetails reports one backfill with why it waits.
func (a *API) backfillDetails(ctx context.Context, id string) (api.Backfill, error) {
	_, report, err := a.backfillReport(ctx, id)
	if err != nil {
		return api.Backfill{}, err
	}
	item := toAPIBackfill(report, a.schedulerActive(ctx))
	if a.queueStore != nil && a.procRepository != nil {
		wait, err := backfill.WaitReason(ctx, report, backfill.QueueView{
			Items: a.queueStore,
			Procs: aliveRuns{a.procRepository},
			Limit: a.config.QueueLimit,
		}, a.config.Core.Location)
		if err != nil {
			return api.Backfill{}, err
		}
		item.Waiting = stringOrNil(wait)
	}
	return item, nil
}

// aliveRuns counts a queue's runs in progress on this server's processes.
type aliveRuns struct{ procs processRepository }

func (r aliveRuns) CountAlive(ctx context.Context, groupName string) (int, error) {
	alive, err := r.procs.ListAllAlive(ctx)
	return len(alive[groupName]), err
}

func (a *API) ListBackfillSlots(ctx context.Context, request api.ListBackfillSlotsRequestObject) (api.ListBackfillSlotsResponseObject, error) {
	_, report, err := a.backfillReport(ctx, request.BackfillId)
	if err != nil {
		return nil, err
	}
	var matching []backfill.SlotReport
	for _, slot := range report.Slots {
		if request.Params.State == nil || string(*request.Params.State) == string(slot.State) {
			matching = append(matching, slot)
		}
	}
	offset := min(max(valueOf(request.Params.Offset), 0), len(matching))
	limit := valueOf(request.Params.Limit)
	if limit <= 0 {
		limit = defaultBackfillSlotPage
	}
	limit = min(limit, maxBackfillSlotPage)
	page := matching[offset:min(offset+limit, len(matching))]
	slots := make([]api.BackfillSlot, 0, len(page))
	for _, slot := range page {
		slots = append(slots, api.BackfillSlot{Slot: slot.At, State: api.BackfillSlotState(slot.State), RunId: stringOrNil(slot.RunID)})
	}
	return api.ListBackfillSlots200JSONResponse{Slots: slots, Total: len(matching)}, nil
}

func (a *API) CancelBackfill(ctx context.Context, request api.CancelBackfillRequestObject) (api.CancelBackfillResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	store, err := a.requireBackfillStore()
	if err != nil {
		return nil, err
	}
	if err := a.requireBackfillExecute(ctx, store, request.BackfillId); err != nil {
		return nil, err
	}
	stopRunning := request.Body != nil && valueOf(request.Body.StopRunning)
	record, err := a.backfillService(store).Cancel(ctx, request.BackfillId, a.backfillRunControl(), stopRunning)
	switch {
	case errors.Is(err, backfill.ErrNotFound):
		return nil, backfillNotFound(request.BackfillId)
	case errors.Is(err, backfill.ErrEnded):
		return api.CancelBackfill409JSONResponse{Code: api.ErrorCodeBadRequest, Message: fmt.Sprintf("backfill %s has already ended %s", record.ID, record.Status)}, nil
	case err != nil:
		return nil, err
	}
	a.logAudit(ctx, audit.CategoryDAG, "backfill_cancel", map[string]any{
		"dag_name":     record.DAGName,
		"backfill_id":  record.ID,
		"stop_running": stopRunning,
	})
	report, err := backfill.Describe(ctx, store, a.readRun, record)
	if err != nil {
		return nil, err
	}
	return api.CancelBackfill200JSONResponse(toAPIBackfill(report, a.schedulerActive(ctx))), nil
}

func (a *API) ResumeBackfill(ctx context.Context, request api.ResumeBackfillRequestObject) (api.ResumeBackfillResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	store, err := a.requireBackfillStore()
	if err != nil {
		return nil, err
	}
	if err := a.requireBackfillExecute(ctx, store, request.BackfillId); err != nil {
		return nil, err
	}
	record, err := store.Get(ctx, request.BackfillId)
	if err != nil {
		return nil, err
	}
	dag, sched, err := a.backfillDAG(ctx, backfillFileName(record), record.Options.Profile)
	if err != nil {
		return nil, err
	}
	req := backfill.ResumeRequest{ID: record.ID, DAG: dag, Schedule: sched, MaxSlots: a.config.Scheduler.BackfillMaxSlots}
	if body := request.Body; body != nil {
		req.MaxConcurrency = valueOf(body.MaxConcurrency)
		req.OnFailure = backfill.OnFailure(valueOf(body.OnFailure))
		req.DryRun = valueOf(body.DryRun)
		req.Revision = valueOf(body.Revision)
	}
	if !req.DryRun && !a.config.Queues.Enabled {
		return nil, badRequest("a backfill runs through the DAG's queue; set queues.enabled to true")
	}
	result, err := a.backfillService(store).Resume(ctx, req, a.backfillRunControl())
	maxConcurrency := record.Options.MaxConcurrency
	if req.MaxConcurrency > 0 {
		maxConcurrency = req.MaxConcurrency
	}
	response, respErr := a.backfillSubmitResponse(ctx, store, dag, maxConcurrency, result)
	if respErr != nil {
		return nil, respErr
	}
	if errors.Is(err, backfill.ErrStalePlan) {
		return api.ResumeBackfill409JSONResponse(response), nil
	}
	if err != nil {
		return nil, backfillError(err)
	}
	if result.Record != nil && !req.DryRun {
		a.logAudit(ctx, audit.CategoryDAG, "backfill_resume", map[string]any{
			"dag_name":    record.DAGName,
			"backfill_id": record.ID,
			"slots":       len(result.Record.Slots),
		})
	}
	return api.ResumeBackfill200JSONResponse(response), nil
}

// backfillDAG loads the DAG of a backfill, checks the caller may run it, and
// returns how its slots fire.
func (a *API) backfillDAG(ctx context.Context, fileName, profile string) (*ir.DAG, backfill.SlotSchedule, error) {
	dag, err := a.dagRepository.GetDetails(ctx, fileName, persis.DAGLoadOptions{AllowBuildErrors: true})
	if err != nil {
		return nil, backfill.SlotSchedule{}, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", fileName),
		}
	}
	if err := a.requireExecuteForWorkspace(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, backfill.SlotSchedule{}, err
	}
	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return nil, backfill.SlotSchedule{}, err
	}
	schedules := slices.Clone(dag.Schedule)
	spec.BindScheduleCalendars(schedules, a.calendarSource())
	return dag, backfill.SlotSchedule{Schedules: schedules, Profile: profile, Location: a.serverLocation()}, nil
}

// requireBackfillExecute allows changes to a backfill to a user who may run
// DAGs in the workspace its labels name. The record keeps them, so a
// backfill whose DAG is gone or does not build is checked alike.
func (a *API) requireBackfillExecute(ctx context.Context, store backfill.Store, id string) error {
	record, err := store.Get(ctx, id)
	if errors.Is(err, backfill.ErrNotFound) {
		return backfillNotFound(id)
	}
	if err != nil {
		return err
	}
	if err := a.requireWorkspaceVisible(ctx, backfillWorkspace(record)); err != nil {
		return err
	}
	return a.requireExecuteForWorkspace(ctx, backfillWorkspace(record))
}

// backfillWorkspace returns the workspace a backfill belongs to.
func backfillWorkspace(record *backfill.Record) string {
	return dagWorkspaceName(&ir.DAG{Labels: record.Labels})
}

func (a *API) backfillReport(ctx context.Context, id string) (backfill.Store, backfill.Report, error) {
	store, err := a.requireBackfillStore()
	if err != nil {
		return nil, backfill.Report{}, err
	}
	record, err := store.Get(ctx, id)
	if errors.Is(err, backfill.ErrNotFound) {
		return nil, backfill.Report{}, backfillNotFound(id)
	}
	if err != nil {
		return nil, backfill.Report{}, err
	}
	if err := a.requireWorkspaceVisible(ctx, backfillWorkspace(record)); err != nil {
		return nil, backfill.Report{}, err
	}
	report, err := backfill.Describe(ctx, store, a.readRun, record)
	return store, report, err
}

// backfillSubmitResponse reports a submit's plan, with how it would run at
// maxConcurrency, and the backfill it created or found.
func (a *API) backfillSubmitResponse(ctx context.Context, store backfill.Store, dag *ir.DAG, maxConcurrency int, result backfill.SubmitResult) (api.BackfillSubmitResponse, error) {
	outlook, err := backfill.LoadOutlook(ctx, a.dagRunRepository, dag, maxConcurrency, a.config.QueueLimit(dag.ProcGroup()))
	if err != nil {
		return api.BackfillSubmitResponse{}, err
	}
	suspended, err := a.dagRepository.IsSuspended(ctx, dag.SuspendFlagName())
	warnings := result.Plan.Warnings(backfill.WarningInput{
		SchedulerRunning: a.schedulerActive(ctx),
		Suspended:        err == nil && suspended,
		DAG:              dag,
		Outlook:          outlook,
	})
	response := api.BackfillSubmitResponse{Plan: toAPIBackfillPlan(result.Plan, outlook, warnings)}
	if result.Record != nil {
		report, err := backfill.Describe(ctx, store, a.readRun, result.Record)
		if err != nil {
			return response, err
		}
		item := toAPIBackfill(report, a.schedulerActive(ctx))
		response.Backfill = &item
		response.Existing = &result.Existing
	}
	return response, nil
}

// latestBackfill reports the backfill of a DAG an operator should see, as
// backfill.Latest picks it, or nil.
func (a *API) latestBackfill(ctx context.Context, fileName string) (*api.LatestBackfill, error) {
	if a.backfillStore == nil {
		return nil, nil
	}
	dag, err := a.dagRepository.GetDetails(ctx, fileName, persis.DAGLoadOptions{AllowBuildErrors: true})
	if err != nil {
		return nil, err
	}
	schedules := slices.Clone(dag.Schedule)
	spec.BindScheduleCalendars(schedules, a.calendarSource())
	sched := backfill.SlotSchedule{Schedules: schedules, Location: a.serverLocation()}
	report, err := backfill.Latest(ctx, a.backfillStore, a.readRun, a.dagRunRepository, dag, sched)
	if err != nil || report == nil {
		return nil, err
	}
	item := toAPIBackfill(*report, a.schedulerActive(ctx))
	return &item, nil
}

// backfillFileName returns the API name of the DAG file a backfill records
// by its path.
func backfillFileName(record *backfill.Record) string {
	return fileutil.TrimYAMLFileExtension(filepath.Base(record.DAGFile))
}

func (a *API) requireBackfillStore() (backfill.Store, error) {
	if a.backfillStore == nil {
		return nil, &Error{HTTPStatus: http.StatusServiceUnavailable, Code: api.ErrorCodeInternalError, Message: "backfills are not available on this server"}
	}
	return a.backfillStore, nil
}

func (a *API) backfillService(store backfill.Store) *backfill.Service {
	return &backfill.Service{Store: store, History: a.dagRunRepository}
}

func (a *API) readRun(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, ref)
	if err != nil {
		return nil, err
	}
	return attempt.ReadStatus(ctx)
}

// backfillRunControl withdraws a queued run as the dequeue endpoint does
// and stops an active run as the stop endpoint does.
func (a *API) backfillRunControl() backfill.RunControl {
	return backfill.RunControl{
		ReadRun: a.readRun,
		Withdraw: func(ctx context.Context, ref ir.DAGRunRef) error {
			queueName, err := a.queueNameForDAGRun(ctx, ref)
			if err != nil {
				return err
			}
			return a.procRepository.WithLock(ctx, queueName, func() error {
				if err := queue.AbortQueuedDAGRun(ctx, a.dagRunRepository, ref); err != nil {
					return err
				}
				if _, err := a.queueStore.DequeueByDAGRunID(ctx, queueName, ref); err != nil && !errors.Is(err, queue.ErrQueueItemNotFound) {
					return err
				}
				return nil
			})
		},
		Stop: func(ctx context.Context, ref ir.DAGRunRef) error {
			attempt, err := a.dagRunRepository.FindAttempt(ctx, ref)
			if err != nil {
				return err
			}
			dag, err := attempt.ReadDAG(ctx)
			if err != nil {
				return err
			}
			return a.dagRunMgr.Stop(ctx, dag, ref.ID)
		},
	}
}

// schedulerActive reports whether a scheduler is known to run backfills.
func (a *API) schedulerActive(ctx context.Context) bool {
	running, _ := a.schedulerRunning(ctx)
	return running
}

func toAPIBackfill(report backfill.Report, schedulerRunning bool) api.Backfill {
	record := report.Record
	status := api.BackfillStatus(record.Status)
	if record.Open() {
		status = api.BackfillStatusRunning
		if !schedulerRunning {
			status = api.BackfillStatusWaitingForScheduler
		}
	}
	counts := make(map[string]int, len(report.Counts))
	for state, n := range report.Counts {
		counts[string(state)] = n
	}
	return api.Backfill{
		Id:             record.ID,
		Dag:            record.DAGName,
		Status:         status,
		SubmittedAt:    record.CreatedAt,
		EndedAt:        timeOrNil(record.EndedAt),
		Actor:          stringOrNil(record.Actor),
		Generation:     &record.Generation,
		Rerun:          api.BackfillRerun(record.Options.Rerun),
		MaxConcurrency: record.Options.MaxConcurrency,
		OnFailure:      api.BackfillOnFailure(record.Options.OnFailure),
		SlotCount:      len(record.Slots),
		Counts:         counts,
	}
}

func toAPIBackfillPlan(plan backfill.Plan, outlook backfill.Outlook, warnings []string) api.BackfillPlan {
	out := api.BackfillPlan{
		Dag:         plan.DAGName,
		From:        timeOrNil(plan.From),
		To:          timeOrNil(plan.To),
		Clamped:     &plan.Clamped,
		Rerun:       api.BackfillRerun(plan.Rerun),
		Concurrency: outlook.Concurrency,
		QueueLimit:  outlook.QueueLimit,
		Revision:    plan.Revision,
		Slots:       make([]api.BackfillPlanSlot, 0, len(plan.Slots)),
	}
	if estimate := outlook.Estimate(len(plan.Runs())); estimate > 0 {
		out.EstimateSeconds = new(int(estimate.Round(time.Second).Seconds()))
	}
	if out.Rerun == "" {
		out.Rerun = api.BackfillRerun(backfill.RerunNone)
	}
	out.RunCounts.None = plan.RunCounts[backfill.RerunNone]
	out.RunCounts.Failed = plan.RunCounts[backfill.RerunFailed]
	out.RunCounts.All = plan.RunCounts[backfill.RerunAll]
	if plan.OtherCadence > 0 {
		out.OtherCadence = &plan.OtherCadence
	}
	if len(warnings) > 0 {
		out.Warnings = &warnings
	}
	for _, slot := range plan.Slots {
		out.Slots = append(out.Slots, api.BackfillPlanSlot{
			Slot:        slot.At,
			WindowStart: timeOrNil(slot.Window.Start),
			WindowEnd:   slot.At,
			Action:      api.BackfillPlanSlotAction(slot.Action),
			RunId:       stringOrNil(slot.RunID),
			CoveredBy:   timeOrNil(slot.CoveredAt),
		})
	}
	return out
}

func dagSlotStatus(status backfill.SlotStatus) api.DAGSlotStatus {
	switch status {
	case backfill.SlotSucceeded:
		return api.DAGSlotStatusSucceeded
	case backfill.SlotFailed:
		return api.DAGSlotStatusFailed
	case backfill.SlotActive:
		return api.DAGSlotStatusActive
	case backfill.SlotMissing:
		return api.DAGSlotStatusMissing
	}
	return api.DAGSlotStatusMissing
}

func backfillError(err error) error {
	if errors.Is(err, backfill.ErrInvalidRequest) {
		return badRequest("%s", err.Error())
	}
	return err
}

func backfillNotFound(id string) error {
	return &Error{HTTPStatus: http.StatusNotFound, Code: api.ErrorCodeNotFound, Message: fmt.Sprintf("backfill %s not found", id)}
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func stringOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
