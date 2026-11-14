// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

// The handlers of the samples, events and availability of a continuous step.
// See specs/083-continuous-runs.md.

const (
	defaultSamplePoints = 600
	minSamplePoints     = 10
	maxSamplePoints     = 2000
	defaultEventLimit   = 100
	maxEventLimit       = 500
	defaultSampleWindow = 24 * time.Hour
)

// attemptDirProvider is implemented by the attempts that live in a directory.
type attemptDirProvider interface {
	Dir() string
}

// openSamplesReader opens the reader of the samples store in dir. Tests replace
// it through the samplesReader field of API.
func (a *API) openSamplesReader(dir string) samples.Reader {
	if a.samplesReader != nil {
		return a.samplesReader(dir)
	}
	return samples.OpenReader(dir)
}

// isContinuousNode reports whether the node is a continuous step: a step that
// repeats without a limit. The runner persists the flag; a run that was
// written without it is read by its repeat policy.
func isContinuousNode(node *ir.Node) bool {
	if node.Continuous {
		return true
	}
	rp := node.Step.RepeatPolicy
	return rp.RepeatMode != "" && rp.Limit == 0 && rp.LimitStr == ""
}

// continuousStepReader finds the run attempt and the step, and opens the
// reader of the store of the step. A non-empty notFound is the message of the
// 404 response.
func (a *API) continuousStepReader(ctx context.Context, name, dagRunID, stepName string) (reader samples.Reader, notFound string, err error) {
	attempt, notFound, err := a.findVisibleAttempt(ctx, name, dagRunID)
	if err != nil || notFound != "" {
		return nil, notFound, err
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("error reading dag-run status: %w", err)
	}
	var node *ir.Node
	if status != nil {
		node = findStepOutputsNode(status, stepName)
	}
	if node == nil {
		return nil, fmt.Sprintf("step %s not found in dag-run of DAG %s", stepName, name), nil
	}
	if !isContinuousNode(node) {
		return nil, fmt.Sprintf("step %s is not a continuous step", stepName), nil
	}
	provider, ok := attempt.(attemptDirProvider)
	if !ok || provider.Dir() == "" {
		return nil, "", fmt.Errorf("attempt of dag-run %s has no directory", dagRunID)
	}
	return a.openSamplesReader(samples.Dir(provider.Dir(), node.Step.Name)), "", nil
}

func badRequest(format string, args ...any) *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    fmt.Sprintf(format, args...),
	}
}

func checkIntRange(name string, value *int, lo, hi int) error {
	if value != nil && (*value < lo || *value > hi) {
		return badRequest("%s must be between %d and %d", name, lo, hi)
	}
	return nil
}

func (a *API) GetDAGRunStepSamples(ctx context.Context, request api.GetDAGRunStepSamplesRequestObject) (api.GetDAGRunStepSamplesResponseObject, error) {
	p := request.Params
	if err := checkIntRange("points", p.Points, minSamplePoints, maxSamplePoints); err != nil {
		return nil, err
	}
	to := time.Now().UTC()
	if p.To != nil {
		to = p.To.UTC()
	}
	from := to.Add(-defaultSampleWindow)
	if p.From != nil {
		from = p.From.UTC()
	}
	if from.After(to) {
		return nil, badRequest("from must not be after to")
	}
	points := defaultSamplePoints
	if p.Points != nil {
		points = *p.Points
	}

	reader, notFound, err := a.continuousStepReader(ctx, request.Name, request.DagRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != "" {
		return api.GetDAGRunStepSamples404JSONResponse{Code: api.ErrorCodeNotFound, Message: notFound}, nil
	}

	series, err := reader.Samples(ctx, samples.SampleQuery{From: from, To: to, MaxPoints: points})
	if err != nil {
		return nil, fmt.Errorf("error reading samples: %w", err)
	}
	return api.GetDAGRunStepSamples200JSONResponse(toStepSamples(series, from, to)), nil
}

func toStepSamples(series samples.Series, from, to time.Time) api.StepSamples {
	if !series.From.IsZero() {
		from = series.From
	}
	if !series.To.IsZero() {
		to = series.To
	}
	buckets := make([]api.SampleBucket, 0, len(series.Buckets))
	for _, b := range series.Buckets {
		buckets = append(buckets, api.SampleBucket{
			Time:     b.Time.UTC(),
			Count:    countInt64(b.Count),
			Failures: countInt64(b.Failures),
			MinMs:    b.Min.Milliseconds(),
			MaxMs:    b.Max.Milliseconds(),
			P50Ms:    b.P50.Milliseconds(),
			P95Ms:    b.P95.Milliseconds(),
		})
	}
	return api.StepSamples{
		From:              from.UTC(),
		To:                to.UTC(),
		ResolutionSeconds: int(series.Tier.Width() / time.Second),
		Buckets:           buckets,
	}
}

func (a *API) GetDAGRunStepEvents(ctx context.Context, request api.GetDAGRunStepEventsRequestObject) (api.GetDAGRunStepEventsResponseObject, error) {
	p := request.Params
	if err := checkIntRange("limit", p.Limit, 1, maxEventLimit); err != nil {
		return nil, err
	}
	query := samples.EventQuery{Limit: defaultEventLimit}
	if p.Limit != nil {
		query.Limit = *p.Limit
	}
	if p.From != nil {
		query.From = p.From.UTC()
	}
	if p.To != nil {
		query.To = p.To.UTC()
	}
	if p.From != nil && p.To != nil && query.From.After(query.To) {
		return nil, badRequest("from must not be after to")
	}
	if p.Before != nil {
		query.Before = *p.Before
	}

	reader, notFound, err := a.continuousStepReader(ctx, request.Name, request.DagRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != "" {
		return api.GetDAGRunStepEvents404JSONResponse{Code: api.ErrorCodeNotFound, Message: notFound}, nil
	}

	page, err := reader.Events(ctx, query)
	if errors.Is(err, samples.ErrInvalidCursor) {
		return nil, badRequest("invalid cursor")
	}
	if err != nil {
		return nil, fmt.Errorf("error reading events: %w", err)
	}
	return api.GetDAGRunStepEvents200JSONResponse(toStepEvents(page)), nil
}

func toStepEvents(page samples.EventPage) api.StepEvents {
	events := make([]api.StepEvent, 0, len(page.Events))
	for _, e := range page.Events {
		ev := api.StepEvent{Time: e.Time.UTC(), Kind: api.StepEventKind(e.Kind)}
		if e.Kind == samples.EventFailed {
			ev.ExitCode = ptrOf(e.ExitCode)
		}
		if e.Message != "" {
			ev.Message = ptrOf(e.Message)
		}
		if e.FailedCount > 0 && (e.Kind == samples.EventFailed || e.Kind == samples.EventRecovered) {
			ev.FailedCount = ptrOf(countInt64(e.FailedCount))
		}
		if e.Kind == samples.EventRecovered {
			ev.OutageSeconds = new(int64(e.Outage / time.Second))
		}
		events = append(events, ev)
	}
	result := api.StepEvents{Events: events}
	if page.NextCursor != "" {
		result.NextCursor = ptrOf(page.NextCursor)
	}
	return result
}

// availabilityWindows lists the windows of the response, in this order.
var availabilityWindows = []struct {
	window samples.Window
	api    api.AvailabilityWindowWindow
}{
	{samples.Window24h, api.AvailabilityWindowWindowN24h},
	{samples.Window7d, api.AvailabilityWindowWindowN7d},
	{samples.Window30d, api.AvailabilityWindowWindowN30d},
	{samples.Window365d, api.AvailabilityWindowWindowN365d},
}

func (a *API) GetDAGRunStepAvailability(ctx context.Context, request api.GetDAGRunStepAvailabilityRequestObject) (api.GetDAGRunStepAvailabilityResponseObject, error) {
	reader, notFound, err := a.continuousStepReader(ctx, request.Name, request.DagRunId, request.StepName)
	if err != nil {
		return nil, err
	}
	if notFound != "" {
		return api.GetDAGRunStepAvailability404JSONResponse{Code: api.ErrorCodeNotFound, Message: notFound}, nil
	}

	availability, err := reader.Availability(ctx, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("error reading availability: %w", err)
	}
	return api.GetDAGRunStepAvailability200JSONResponse(toStepAvailability(availability)), nil
}

func toStepAvailability(av samples.Availability) api.StepAvailability {
	stats := make(map[samples.Window]samples.WindowStat, len(av.Windows))
	for _, w := range av.Windows {
		stats[w.Window] = w
	}
	windows := make([]api.AvailabilityWindow, 0, len(availabilityWindows))
	for _, w := range availabilityWindows {
		stat := stats[w.window]
		out := api.AvailabilityWindow{Window: w.api, Count: countInt64(stat.Count), Failures: countInt64(stat.Failures)}
		if stat.Count > 0 {
			out.Availability = new(float64(stat.Count-min(stat.Failures, stat.Count)) / float64(stat.Count))
		}
		windows = append(windows, out)
	}
	result := api.StepAvailability{
		Windows:    windows,
		Streak:     countInt64(av.Streak),
		TotalCount: countInt64(av.TotalCount),
	}
	if !av.LastFailureAt.IsZero() {
		result.LastFailureAt = ptrOf(av.LastFailureAt.UTC())
	}
	return result
}

// countInt64 converts a sample count for the API, saturating at MaxInt64.
func countInt64(n uint64) int64 {
	return int64(min(n, math.MaxInt64)) // #nosec G115 -- clamped to MaxInt64
}
