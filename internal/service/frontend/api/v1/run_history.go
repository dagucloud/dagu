// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

// The handlers of the run history of a service DAG. See
// specs/097-service-run-history.md.

// hasRunHistory reports whether dir holds a run history.
func hasRunHistory(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// runHistoryReader opens the reader of the run history of a DAG. It reports
// false when a DAG that is not a service DAG has no history. A service DAG
// with no history gets a reader on an empty store.
func (a *API) runHistoryReader(dag *ir.DAG) (samples.Reader, bool) {
	dir, ok := a.dagRunRepository.RunHistoryDir(dag.Name)
	if !ok {
		return nil, false
	}
	if !dag.RestartPolicy.Enabled() && !hasRunHistory(dir) {
		return nil, false
	}
	return a.openSamplesReader(dir), true
}

// runHistoryTarget loads the DAG of a run history route and opens its reader.
// A non-empty notFound is the message of the 404 response.
func (a *API) runHistoryTarget(ctx context.Context, fileName string) (dag *ir.DAG, reader samples.Reader, notFound string, err error) { //nolint:revive // the 404 message is part of the result
	dag, err = a.dagRepository.GetDetails(ctx, fileName, persis.DAGLoadOptions{AllowBuildErrors: true})
	if err != nil {
		return nil, nil, fmt.Sprintf("DAG %s not found", fileName), nil
	}
	if err := a.requireWorkspaceVisible(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, nil, "", err
	}
	reader, ok := a.runHistoryReader(dag)
	if !ok {
		return nil, nil, fmt.Sprintf("DAG %s has no run history", dag.Name), nil
	}
	return dag, reader, "", nil
}

// activeRunStart returns the start of the run that is in progress, or the zero
// time when the status is not of a run that has started and not ended.
func activeRunStart(status ir.DAGRunStatus) time.Time {
	if !status.Status.IsActive() || status.Status == ir.Queued {
		return time.Time{}
	}
	return runTime(status.StartedAt)
}

func (a *API) GetDAGRunHistorySamples(ctx context.Context, request api.GetDAGRunHistorySamplesRequestObject) (api.GetDAGRunHistorySamplesResponseObject, error) {
	p := request.Params
	query, err := sampleQueryOf(p.From, p.To, p.Points)
	if err != nil {
		return nil, err
	}
	dag, reader, notFound, err := a.runHistoryTarget(ctx, request.FileName)
	if err != nil {
		return nil, err
	}
	if notFound != "" {
		return api.GetDAGRunHistorySamples404JSONResponse{Code: api.ErrorCodeNotFound, Message: notFound}, nil
	}
	series, err := reader.Samples(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error reading run history samples: %w", err)
	}
	uptime, err := reader.Uptime(ctx, query.From, query.To)
	if err != nil {
		return nil, fmt.Errorf("error reading run history uptime: %w", err)
	}
	start := activeRunStart(a.latestRunStatus(ctx, dag))
	result := toStepSamples(series.WithActive(start, uptime.Last), query.From, query.To)
	if ratio, ok := uptime.WithActive(start).Ratio(); ok {
		result.Uptime = &ratio
	}
	return api.GetDAGRunHistorySamples200JSONResponse(result), nil
}

func (a *API) GetDAGRunHistoryEvents(ctx context.Context, request api.GetDAGRunHistoryEventsRequestObject) (api.GetDAGRunHistoryEventsResponseObject, error) {
	p := request.Params
	query, err := eventQueryOf(p.From, p.To, p.Limit, p.Before)
	if err != nil {
		return nil, err
	}
	_, reader, notFound, err := a.runHistoryTarget(ctx, request.FileName)
	if err != nil {
		return nil, err
	}
	if notFound != "" {
		return api.GetDAGRunHistoryEvents404JSONResponse{Code: api.ErrorCodeNotFound, Message: notFound}, nil
	}
	page, err := reader.Events(ctx, query)
	if errors.Is(err, samples.ErrInvalidCursor) {
		return nil, badRequest("invalid cursor")
	}
	if err != nil {
		return nil, fmt.Errorf("error reading run history events: %w", err)
	}
	return api.GetDAGRunHistoryEvents200JSONResponse(toRunHistoryEvents(page)), nil
}

// toRunHistoryEvents maps a page of events. A run history has no started and
// no stopped event: a store that gives one is ignored.
func toRunHistoryEvents(page samples.EventPage) api.RunHistoryEvents {
	events := make([]api.RunHistoryEvent, 0, len(page.Events))
	for _, e := range page.Events {
		ev := api.RunHistoryEvent{Time: e.Time.UTC(), Kind: api.RunHistoryEventKind(e.Kind), RunId: e.RunID}
		switch e.Kind {
		case samples.EventFailed:
			if e.Message != "" {
				ev.Message = ptrOf(e.Message)
			}
			if e.FailedCount > 0 {
				ev.FailedCount = ptrOf(countInt64(e.FailedCount))
			}
		case samples.EventRecovered:
			ev.FailedCount = ptrOf(countInt64(e.FailedCount))
			ev.OutageSeconds = ptrOf(int64(e.Outage / time.Second))
		case samples.EventGap:
			ev.GapSeconds = ptrOf(int64(e.Gap / time.Second))
		case samples.EventStarted, samples.EventStopped:
			continue
		default:
			continue
		}
		events = append(events, ev)
	}
	result := api.RunHistoryEvents{Events: events}
	if page.NextCursor != "" {
		result.NextCursor = ptrOf(page.NextCursor)
	}
	return result
}

func (a *API) GetDAGRunHistoryAvailability(ctx context.Context, request api.GetDAGRunHistoryAvailabilityRequestObject) (api.GetDAGRunHistoryAvailabilityResponseObject, error) {
	dag, reader, notFound, err := a.runHistoryTarget(ctx, request.FileName)
	if err != nil {
		return nil, err
	}
	if notFound != "" {
		return api.GetDAGRunHistoryAvailability404JSONResponse{Code: api.ErrorCodeNotFound, Message: notFound}, nil
	}
	availability, err := reader.Availability(ctx, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("error reading run history availability: %w", err)
	}
	// A sample is written when a run ends, so the run in progress is added
	// from the latest status.
	if start := activeRunStart(a.latestRunStatus(ctx, dag)); !start.IsZero() {
		for i := range availability.Windows {
			availability.Windows[i].Uptime = availability.Windows[i].Uptime.WithActive(start)
		}
	}
	return api.GetDAGRunHistoryAvailability200JSONResponse(toStepAvailability(availability)), nil
}

// latestRunStatus reads the status of the latest run of a DAG, as the DAG
// details do. A DAG with no run, or a status that cannot be read, gives the
// initial status.
func (a *API) latestRunStatus(ctx context.Context, dag *ir.DAG) ir.DAGRunStatus {
	status, err := a.dagRunMgr.GetLatestStatus(ctx, dag)
	if err != nil {
		return ir.DAGRunStatus{}
	}
	return status
}
