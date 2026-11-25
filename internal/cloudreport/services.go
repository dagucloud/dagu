// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

// A service DAG starts a new run as soon as its previous one ends, which can
// be every few seconds. Reports never carry its runs. They list each service
// DAG instead, with its state, its open outage, the failed, recovered, and gap
// events of its run history since the last accepted report, and its runs by
// the hour they started (RFC 024).

// ServiceDAGs reads the server's service DAGs.
type ServiceDAGs interface {
	// List returns the service DAGs by name, with their state.
	List(ctx context.Context) ([]ServiceDAG, error)
	// Names returns the names of the service DAGs.
	Names(ctx context.Context) ([]string, error)
	// HistoryDir returns the directory of the run history of a service DAG;
	// false when it has none.
	HistoryDir(name string) (string, bool)
}

// ServiceDAG is a service DAG with its state, as GET /service-dags gives it.
// Latest is nil before its first run, and NextRestartAt is nil unless it
// waits to restart.
type ServiceDAG struct {
	Name          string
	RestartPolicy string
	RestartWait   time.Duration
	State         string
	NextRestartAt *time.Time
	Latest        *ir.DAGRunStatus
}

// Limits on the service DAGs one report lists; service_dags_total counts
// them all.
const (
	maxServiceDAGs     = 500
	maxServiceDAGBytes = 256 << 10
	maxServiceEvents   = 20
	maxServiceText     = 256
	// serviceHourDays is how long a run history keeps hours. Older hours
	// come only by the day, so reports send none.
	serviceHourDays = 30
	// firstServiceHours are the hours the first report of a service DAG
	// sends.
	firstServiceHours = 24
	// serviceReadTimeout bounds the reading of the service DAGs, so that a
	// stalled file system leaves them out of a report rather than holding it.
	serviceReadTimeout = 10 * time.Second
	// serviceWatchChecks is how many checks a service DAG whose run ended
	// is read for a new outage: the scheduler records the run in its history
	// soon after the run ends.
	serviceWatchChecks = 3
	// serviceUrgentEvery is the least time between two early reports for one
	// service DAG. Dagu Console keeps a service's alert open until it goes
	// as long without a failure, so a service that fails now and then waits
	// for the next report.
	serviceUrgentEvery = 10 * time.Minute
)

// serviceMark is what the last accepted report sent of a service DAG: its
// newest event, the hour it ran up to, and the hour its run then in
// progress started, whose sample its history files under that hour once
// the run ends.
type serviceMark struct {
	EventAt     time.Time `json:"event_at,omitzero"`
	Hour        time.Time `json:"hour,omitzero"`
	OpenRunHour time.Time `json:"open_run_hour,omitzero"`
}

// serviceDAGEntry is a service DAG as a report lists it.
type serviceDAGEntry struct {
	Workflow       string         `json:"workflow"`
	RestartPolicy  string         `json:"restart_policy"`
	RestartWaitSec int            `json:"restart_wait_sec"`
	State          string         `json:"state"`
	NextRestartAt  string         `json:"next_restart_at,omitempty"`
	LatestRun      *serviceRun    `json:"latest_run,omitempty"`
	Outage         *serviceOutage `json:"outage,omitempty"`
	Events         []serviceEvent `json:"events,omitempty"`
	Hours          []serviceHour  `json:"hours,omitempty"`
}

type serviceRun struct {
	DAGRunID   string `json:"dag_run_id"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

type serviceOutage struct {
	Open          bool   `json:"open"`
	FailedCount   uint64 `json:"failed_count"`
	Since         string `json:"since,omitempty"`
	LastFailureAt string `json:"last_failure_at,omitempty"`
}

type serviceEvent struct {
	Type          string `json:"type"`
	At            string `json:"at"`
	DAGRunID      string `json:"dag_run_id,omitempty"`
	FailedCount   uint64 `json:"failed_count,omitempty"`
	OutageSeconds int64  `json:"outage_seconds,omitempty"`
	GapSeconds    int64  `json:"gap_seconds,omitempty"`
	Message       string `json:"message,omitempty"`
}

// serviceHour is a service DAG's runs that started in one hour, in full. UpMs
// is nil while the run history knows no uptime.
type serviceHour struct {
	Start    string `json:"start"`
	Count    uint64 `json:"count"`
	Failures uint64 `json:"failures"`
	SumMs    int64  `json:"sum_ms"`
	MinMs    int64  `json:"min_ms"`
	MaxMs    int64  `json:"max_ms"`
	P95Ms    int64  `json:"p95_ms"`
	UpMs     *int64 `json:"up_ms,omitempty"`
	start    time.Time
}

// serviceDAGs lists the service DAGs for the next report as of now, with
// their errors when withErrors, and their total; nil while they cannot be
// read. It keeps what the list sends, for the acceptance of the report to
// record.
func (f *eventFeed) serviceDAGs(ctx context.Context, withErrors bool, now time.Time) (*[]serviceDAGEntry, *int) {
	if f == nil || f.services == nil || !f.loaded {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, serviceReadTimeout)
	defer cancel()
	dags, err := f.services.List(ctx)
	if err != nil {
		f.warn(ctx, warnServices, err)
		return nil, nil
	}
	f.recovered(warnServices)
	dags = slices.DeleteFunc(dags, func(d ServiceDAG) bool { return d.Name == "" || len(d.Name) > maxNameBytes })
	total := len(dags)
	if len(dags) > maxServiceDAGs {
		dags = dags[:maxServiceDAGs]
	}
	entries := make([]serviceDAGEntry, 0, len(dags))
	marks := make(map[string]serviceMark, len(dags))
	for _, d := range dags {
		entry, mark := f.serviceDAG(ctx, d, f.state.Services[d.Name], withErrors, now)
		entries = append(entries, entry)
		marks[d.Name] = mark
	}
	entries = fitServiceDAGs(entries)
	f.sentServices = marks
	return &entries, &total
}

// serviceDAG is one service DAG as the next report lists it, and what the
// report sends of it, after mark.
func (f *eventFeed) serviceDAG(ctx context.Context, d ServiceDAG, mark serviceMark, withErrors bool, now time.Time) (serviceDAGEntry, serviceMark) {
	entry := serviceDAGEntry{
		Workflow: d.Name, RestartPolicy: d.RestartPolicy, RestartWaitSec: int(d.RestartWait / time.Second), State: d.State,
	}
	if d.NextRestartAt != nil {
		entry.NextRestartAt = formatTime(*d.NextRestartAt)
	}
	var runStart time.Time
	if latest := d.Latest; latest != nil && latest.DAGRunID != "" && len(latest.DAGRunID) <= maxIDBytes {
		run := serviceRun{
			DAGRunID: latest.DAGRunID, Status: latest.Status.String(),
			StartedAt: snapshotTime(latest.StartedAt), FinishedAt: snapshotTime(latest.FinishedAt),
		}
		if withErrors && runFailed(latest.Status) {
			if errs := runErrors(latest); len(errs) > 0 {
				run.Error = truncateBytes(errs[0].Message, maxServiceText)
			}
		}
		entry.LatestRun = &run
		if latest.Status.IsActive() && latest.Status != ir.Queued {
			runStart, _ = stringutil.ParseTime(latest.StartedAt)
		}
	}
	next := serviceMark{EventAt: mark.EventAt, Hour: now.UTC().Truncate(time.Hour)}
	if !runStart.IsZero() {
		next.OpenRunHour = runStart.UTC().Truncate(time.Hour)
	}
	dir, ok := f.services.HistoryDir(d.Name)
	if !ok {
		return entry, next
	}
	reader := samples.OpenReaderWith(dir, samples.Options{Now: func() time.Time { return now }})
	outage, err := samples.ReadOutage(dir)
	if err != nil {
		logger.Warn(ctx, "Failed to read the outage of a service DAG for Dagu Console", tag.DAG(d.Name), tag.Error(err))
		return entry, next
	}
	availability, err := reader.Availability(ctx, now)
	if err != nil {
		logger.Warn(ctx, "Failed to read the run history of a service DAG for Dagu Console", tag.DAG(d.Name), tag.Error(err))
		return entry, next
	}
	entry.Outage = &serviceOutage{Open: outage.Open, FailedCount: outage.FailedCount}
	if outage.Open {
		entry.Outage.Since = formatTime(outage.Since)
	}
	if !availability.LastFailureAt.IsZero() {
		entry.Outage.LastFailureAt = formatTime(availability.LastFailureAt)
	}
	entry.Events, next.EventAt = serviceEvents(ctx, reader, mark.EventAt, withErrors, now)
	entry.Hours = serviceHours(ctx, reader, mark, runStart, now)
	return entry, next
}

// serviceEvents are the failed, recovered, and gap events of a run history
// after since, or over the last firstServiceHours when since is zero: the
// newest maxServiceEvents of them, oldest first, with their messages when
// withErrors. It returns the time of the newest, since when there is none.
func serviceEvents(ctx context.Context, reader samples.Reader, since time.Time, withErrors bool, now time.Time) ([]serviceEvent, time.Time) {
	from := since.Add(time.Millisecond)
	if since.IsZero() {
		from = now.Add(-firstServiceHours * time.Hour)
	}
	page, err := reader.Events(ctx, samples.EventQuery{From: from, To: now.Add(time.Second), Limit: maxServiceEvents})
	if err != nil {
		return nil, since
	}
	newest := since
	var out []serviceEvent
	for _, e := range slices.Backward(page.Events) {
		if e.Kind != samples.EventFailed && e.Kind != samples.EventRecovered && e.Kind != samples.EventGap {
			continue
		}
		event := serviceEvent{
			Type: string(e.Kind), At: formatTime(e.Time), DAGRunID: e.RunID, FailedCount: e.FailedCount,
			OutageSeconds: int64(e.Outage / time.Second), GapSeconds: int64(e.Gap / time.Second),
		}
		if withErrors {
			event.Message = truncateBytes(errorMessage(e.Message), maxServiceText)
		}
		out = append(out, event)
		if e.Time.After(newest) {
			newest = e.Time
		}
	}
	return out, newest
}

// serviceHours are the hours of a run history to send after mark: from the
// hour of the last accepted report, or the last firstServiceHours, to now's
// hour, and the hours that a run in progress then or now started in, as far
// back as the history keeps hours. The run in progress since runStart counts
// in them.
func serviceHours(ctx context.Context, reader samples.Reader, mark serviceMark, runStart time.Time, now time.Time) []serviceHour {
	current := now.UTC().Truncate(time.Hour)
	oldest := current.Add(-(serviceHourDays*24 - 1) * time.Hour)
	from := mark.Hour
	if from.IsZero() {
		from = current.Add(-(firstServiceHours - 1) * time.Hour)
	}
	if from.Before(oldest) {
		from = oldest
	}
	hours := readServiceHours(ctx, reader, from, current.Add(time.Hour), runStart, now)
	for _, h := range []time.Time{mark.OpenRunHour, runStart.UTC().Truncate(time.Hour)} {
		if h.IsZero() || !h.Before(from) || h.Before(oldest) {
			continue
		}
		hours = append(hours, readServiceHours(ctx, reader, h, h.Add(time.Hour), runStart, now)...)
	}
	slices.SortFunc(hours, func(a, b serviceHour) int { return a.start.Compare(b.start) })
	return slices.CompactFunc(hours, func(a, b serviceHour) bool { return a.start.Equal(b.start) })
}

// readServiceHours reads the hours of a run history from from to to, with
// the run in progress since runStart. The uptime of an hour is known only
// after the hour the history first recorded a run's active time in, so that
// the time before a new service ran does not count as down. An hour with no
// run and no known uptime is left out.
func readServiceHours(ctx context.Context, reader samples.Reader, from, to, runStart, now time.Time) []serviceHour {
	n := int(to.Sub(from) / time.Hour)
	series, err := reader.Samples(ctx, samples.SampleQuery{From: from, To: to, MaxPoints: n})
	if err != nil || series.Tier != samples.TierHour {
		return nil
	}
	uptime, err := reader.Uptime(ctx, from, to)
	if err != nil {
		return nil
	}
	known := !uptime.Since.IsZero()
	var last time.Time
	if known && !runStart.IsZero() {
		if last, _, err = reader.NewestEnd(ctx); err != nil {
			return nil
		}
	}
	var out []serviceHour
	for _, b := range series.WithActive(runStart, last, now).Buckets {
		hourKnown := known && !b.Time.Before(uptime.Since)
		if b.Count == 0 && !hourKnown {
			continue
		}
		h := serviceHour{
			Start: formatTime(b.Time), Count: b.Count, Failures: b.Failures, SumMs: b.Sum.Milliseconds(),
			MinMs: b.Min.Milliseconds(), MaxMs: b.Max.Milliseconds(), P95Ms: b.P95.Milliseconds(), start: b.Time,
		}
		if hourKnown {
			up := b.Up.Milliseconds()
			h.UpMs = &up
		}
		out = append(out, h)
	}
	return out
}

// fitServiceDAGs cuts the entries to maxServiceDAGBytes: the oldest hours go
// first, then the oldest events, then the last entries. The size counts down
// by what each cut item takes with its comma, which never falls short of
// what the encoding loses, so the entries are encoded once.
func fitServiceDAGs(entries []serviceDAGEntry) []serviceDAGEntry {
	firstHour := func(e serviceDAGEntry) (string, bool) {
		if len(e.Hours) == 0 {
			return "", false
		}
		return e.Hours[0].Start, true
	}
	firstEvent := func(e serviceDAGEntry) (string, bool) {
		if len(e.Events) == 0 {
			return "", false
		}
		return e.Events[0].At, true
	}
	size := encodedLen(entries)
	for len(entries) > 0 && size > maxServiceDAGBytes {
		if i := oldestFirst(entries, firstHour); i >= 0 {
			size -= encodedLen(entries[i].Hours[0]) + len(",")
			entries[i].Hours = entries[i].Hours[1:]
			continue
		}
		if i := oldestFirst(entries, firstEvent); i >= 0 {
			size -= encodedLen(entries[i].Events[0]) + len(",")
			entries[i].Events = entries[i].Events[1:]
			continue
		}
		size -= encodedLen(entries[len(entries)-1]) + len(",")
		entries = entries[:len(entries)-1]
	}
	return entries
}

// encodedLen is how many bytes v takes in JSON.
func encodedLen(v any) int {
	data, _ := json.Marshal(v)
	return len(data)
}

// oldestFirst returns the entry whose first item, by key, is the oldest; -1
// when none has an item. Keys are RFC 3339 times in UTC, which sort as text.
func oldestFirst(entries []serviceDAGEntry, key func(serviceDAGEntry) (string, bool)) int {
	best, bestKey := -1, ""
	for i, e := range entries {
		if k, ok := key(e); ok && (best < 0 || k < bestKey) {
			best, bestKey = i, k
		}
	}
	return best
}

// watchService has the service DAG read for a new outage at the next
// checks, as its run just ended.
func (f *eventFeed) watchService(name string) {
	if f.services == nil {
		return
	}
	if f.serviceWatch == nil {
		f.serviceWatch = make(map[string]int)
	}
	f.serviceWatch[name] = serviceWatchChecks
}

// serviceNews reports whether a watched service DAG's run history has a
// failed event newer than what the last accepted report sent, which calls
// for an early report unless one went for the DAG within serviceUrgentEvery.
// A recovery waits for the next report: Dagu Console resolves the alert only
// once the service goes serviceUrgentEvery without a failure. Each check
// counts down the watch.
func (f *eventFeed) serviceNews(ctx context.Context) bool {
	news := false
	now := f.now()
	for name, left := range f.serviceWatch {
		if left <= 1 {
			delete(f.serviceWatch, name)
		} else {
			f.serviceWatch[name] = left - 1
		}
		dir, ok := f.services.HistoryDir(name)
		if !ok {
			continue
		}
		page, err := samples.OpenReader(dir).Events(ctx, samples.EventQuery{Limit: 1})
		if err != nil || len(page.Events) == 0 {
			continue
		}
		e := page.Events[0]
		if e.Kind != samples.EventFailed || !e.Time.After(f.state.Services[name].EventAt) {
			continue
		}
		if last, ok := f.serviceUrgent[name]; ok && now.Sub(last) < serviceUrgentEvery {
			continue
		}
		if f.serviceUrgent == nil {
			f.serviceUrgent = make(map[string]time.Time)
		}
		f.serviceUrgent[name] = now
		news = true
	}
	return news
}
