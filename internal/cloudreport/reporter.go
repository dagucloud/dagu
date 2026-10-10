// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cloudreport reports the health and DAG-run events of a server with
// an online license to Dagu Console.
package cloudreport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

const (
	reportPath      = "/api/v1/servers/report"
	protocolVersion = 1

	defaultInterval = time.Minute
	minInterval     = 30 * time.Second
	maxInterval     = time.Hour
	// maxStartDelay spreads the first reports of servers started together.
	maxStartDelay = time.Minute
	// intervalJitter is the largest fraction by which a wait varies.
	intervalJitter    = 0.1
	defaultRetryAfter = time.Minute
	initialBackoff    = time.Minute
	maxBackoff        = 5 * time.Minute

	requestTimeout  = 30 * time.Second
	maxResponseSize = 1 << 20
)

// processStartedAt approximates when this process started.
var processStartedAt = time.Now()

// Reporter sends reports to Dagu Console until stopped.
type Reporter struct {
	credentials func() (license.CloudCredentials, bool)
	registry    serviceregistry.ServiceRegistry
	monitoring  *Monitoring
	// events is nil when reports carry no events.
	events    *eventFeed
	client    *http.Client
	startedAt time.Time

	// after and random decide when reports are sent; tests replace them.
	after  func(time.Duration) <-chan time.Time
	random func() float64

	cancel context.CancelFunc
	done   chan struct{}
	// changes wakes the report loop when an administrator changes the level.
	changes chan struct{}

	sendMu sync.Mutex
	// stopSend cancels the report in flight; it is nil between reports.
	stopSend context.CancelFunc

	// The report loop owns the fields below.
	interval time.Duration
	backoff  time.Duration
	last     outcome
	// level is the level the loop last read.
	level config.ReportLevel
	// levelWarned records that the level could not be read.
	levelWarned bool
	// noEventsLogged records that reports were said to carry no events.
	noEventsLogged bool
}

// Start reports in the background until Stop is called or ctx is done.
// Reports authenticate with what credentials returns and are skipped while it
// returns false. Services are counted in registry; a nil registry leaves them
// out of reports. Each report sends what the level in monitoring allows at the
// time: nothing at off, health at health, and also the DAG-run events that
// events configures at runs. Choosing off stops a report in flight.
func Start(
	ctx context.Context,
	credentials func() (license.CloudCredentials, bool),
	registry serviceregistry.ServiceRegistry,
	events Events,
	monitoring *Monitoring,
) *Reporter {
	r := newReporter(credentials, registry, monitoring)
	r.events = newEventFeed(events)
	r.start(ctx)
	return r
}

// Stop ends reporting and waits for a report in flight to finish.
func (r *Reporter) Stop() {
	r.cancel()
	<-r.done
}

func newReporter(
	credentials func() (license.CloudCredentials, bool),
	registry serviceregistry.ServiceRegistry,
	monitoring *Monitoring,
) *Reporter {
	return &Reporter{
		credentials: credentials,
		registry:    registry,
		monitoring:  monitoring,
		client:      &http.Client{Timeout: requestTimeout},
		startedAt:   processStartedAt,
		after:       time.After,
		random:      rand.Float64,
		changes:     make(chan struct{}, 1),
		interval:    defaultInterval,
	}
}

func (r *Reporter) start(ctx context.Context) {
	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	r.monitoring.watch(r.levelChanged)
	go r.run(ctx)
}

// levelChanged stops a report in flight when level is off, and wakes the
// report loop to apply level.
func (r *Reporter) levelChanged(level config.ReportLevel) {
	if !level.Reports() {
		r.sendMu.Lock()
		if r.stopSend != nil {
			r.stopSend()
		}
		r.sendMu.Unlock()
	}
	select {
	case r.changes <- struct{}{}:
	default:
	}
}

func (r *Reporter) run(ctx context.Context) {
	defer close(r.done)
	defer r.events.close(ctx)
	// until is the time left before the next scheduled report.
	until := time.Duration(r.random() * float64(maxStartDelay))
	r.pollEvents(ctx, r.readLevel(ctx), false)
	for {
		wait := until
		if r.events != nil && r.level == config.ReportRuns {
			wait = min(wait, eventCheckInterval)
		}
		if !r.sleep(ctx, wait) {
			return
		}
		until -= wait
		if early := r.pollEvents(ctx, r.readLevel(ctx), until <= 0); until > 0 && !early {
			continue
		}
		until = r.report(ctx)
	}
}

// sleep waits for d and reports whether ctx is still live. A level changed
// meanwhile is applied at once, without changing when the wait ends.
func (r *Reporter) sleep(ctx context.Context, d time.Duration) bool {
	timer := r.after(d)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer:
			return true
		case <-r.changes:
			r.readLevel(ctx)
		}
	}
}

// readLevel returns the level in effect, reading it as off when it cannot be
// read. Below runs, it gives up the lease to report DAG-run events, and the
// events that occur until runs is chosen again are never reported.
func (r *Reporter) readLevel(ctx context.Context) config.ReportLevel {
	level, err := r.monitoring.level(ctx)
	switch {
	case err != nil:
		if !r.levelWarned {
			logger.Warn(ctx, "Failed to read what to report to Dagu Console; reporting nothing", tag.Error(err))
		}
		r.levelWarned = true
		level = config.ReportOff
	case !level.Reports():
		r.levelWarned = false
		level = config.ReportOff
	default:
		r.levelWarned = false
	}

	if level != config.ReportRuns {
		r.events.forget(ctx)
	}
	if level != r.level {
		if r.level != "" {
			logger.Info(ctx, "Changed what this server reports to Dagu Console", slog.String("level", string(level)))
		}
		r.level = level
		if !level.Reports() {
			// Reporting again later logs that it works.
			r.last = outcome{kind: outcomeNone}
		}
	}
	return level
}

// pollEvents prepares the events of the next report and reports whether one
// calls for an early report. It reads the event store only when the report is
// due or could be sent early, so that a console that fails or throttles
// reports does not cause a read every few seconds.
func (r *Reporter) pollEvents(ctx context.Context, level config.ReportLevel, due bool) bool {
	if r.events == nil || level != config.ReportRuns {
		return false
	}
	creds, ok := r.credentials()
	if !ok {
		return false
	}
	early := r.last.kind == outcomeNone || r.last.kind == outcomeAccepted
	return r.events.poll(ctx, creds.ServerID, due || early) && early
}

// outcome classifies a report so that each change is logged once.
type outcome struct {
	kind   outcomeKind
	status int
}

type outcomeKind int

const (
	outcomeNone outcomeKind = iota // nothing was sent
	outcomeAccepted
	outcomeThrottled
	outcomeRejected
	outcomeUnavailable
)

// report sends one report, unless the level is off, and returns how long to
// wait before the next.
func (r *Reporter) report(ctx context.Context) time.Duration {
	sendCtx, stop := context.WithCancel(ctx)
	r.sendMu.Lock()
	r.stopSend = stop
	r.sendMu.Unlock()
	defer func() {
		r.sendMu.Lock()
		r.stopSend = nil
		r.sendMu.Unlock()
		stop()
	}()

	// The level is read after stopSend is set, so that choosing off either
	// comes before the read or stops the report.
	level := r.readLevel(ctx)
	creds, ok := r.credentials()
	if !ok || !level.Reports() {
		r.last = outcome{kind: outcomeNone}
		return r.jitter(r.interval)
	}

	res, err := r.send(sendCtx, creds)
	switch {
	case err != nil && ctx.Err() == nil && sendCtx.Err() != nil:
		// Reporting was turned off; nothing is retried.
		return r.jitter(r.interval)

	case err != nil:
		// A report cut short by Stop is not a failure worth logging.
		if ctx.Err() == nil && r.changed(outcome{kind: outcomeUnavailable}) {
			logger.Warn(ctx, "Failed to report server health to Dagu Console", tag.Error(err))
		}
		return r.jitter(r.nextBackoff())

	case res.status >= http.StatusInternalServerError:
		if r.changed(outcome{kind: outcomeUnavailable}) {
			logger.Warn(ctx, "Failed to report server health to Dagu Console",
				slog.Int("status", res.status), tag.Error(res.message()))
		}
		return r.jitter(r.nextBackoff())

	case res.status == http.StatusTooManyRequests:
		// Backoff counts only consecutive failures to reach the console.
		r.backoff = 0
		wait := retryAfter(res.retryAfter)
		if r.changed(outcome{kind: outcomeThrottled}) {
			logger.Info(ctx, "Dagu Console asked to delay the server report", tag.Interval(wait))
		}
		return wait

	case res.status < http.StatusOK || res.status >= http.StatusMultipleChoices:
		// The license manager acts on rejected activations through its own
		// heartbeat, so the reporter keeps its schedule.
		r.backoff = 0
		if r.changed(outcome{kind: outcomeRejected, status: res.status}) {
			logger.Warn(ctx, "Dagu Console rejected the server report",
				slog.Int("status", res.status), tag.Error(res.message()))
		}
		return r.jitter(r.interval)

	default:
		r.backoff = 0
		var accepted acceptance
		// A body that is not JSON asks for nothing.
		_ = json.Unmarshal(res.body, &accepted)
		r.interval = accepted.interval()
		more := r.events.acknowledge(ctx, accepted.Ack)
		if r.changed(outcome{kind: outcomeAccepted}) {
			logger.Info(ctx, "Reporting server health to Dagu Console", tag.URL(creds.CloudURL))
		}
		if r.events == nil && level == config.ReportRuns && !r.noEventsLogged {
			r.noEventsLogged = true
			logger.Info(ctx, "Reports to Dagu Console carry no DAG-run events because the event store is disabled")
		}
		if more {
			return eventCheckInterval
		}
		return r.jitter(r.interval)
	}
}

// changed records o as the latest outcome and reports whether it differs from
// the previous one.
func (r *Reporter) changed(o outcome) bool {
	if o == r.last {
		return false
	}
	r.last = o
	return true
}

// nextBackoff doubles the wait after consecutive failures, up to maxBackoff.
func (r *Reporter) nextBackoff() time.Duration {
	r.backoff = min(max(2*r.backoff, initialBackoff), maxBackoff)
	return r.backoff
}

// jitter varies d randomly by up to intervalJitter in either direction.
func (r *Reporter) jitter(d time.Duration) time.Duration {
	return d + time.Duration((2*r.random()-1)*intervalJitter*float64(d))
}

// acceptance is the body of a successful response.
type acceptance struct {
	NextReportSeconds float64 `json:"next_report_seconds"`
	// Ack is the cursor that Dagu Console stored.
	Ack string `json:"ack"`
}

// interval returns the interval the response asks for, within
// [minInterval, maxInterval], or defaultInterval when it asks for none.
func (a acceptance) interval() time.Duration {
	if a.NextReportSeconds <= 0 {
		return defaultInterval
	}
	seconds := min(a.NextReportSeconds, maxInterval.Seconds())
	return max(time.Duration(seconds*float64(time.Second)), minInterval)
}

// retryAfter returns the wait a Retry-After header in seconds asks for, up to
// maxInterval, or defaultRetryAfter when the header is missing or invalid.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return defaultRetryAfter
	}
	return time.Duration(min(seconds, int(maxInterval/time.Second))) * time.Second
}

// reportRequest is the body of POST /api/v1/servers/report.
type reportRequest struct {
	Protocol        int    `json:"protocol"`
	LicenseID       string `json:"license_id"`
	ServerID        string `json:"server_id"`
	HeartbeatSecret string `json:"heartbeat_secret"`
	Health          health `json:"health"`
	// Events is set whenever Cursor is; it is empty when the report moves
	// past events that reports leave out.
	Events []reportEvent `json:"events,omitzero"`
	// Cursor identifies the position after Events.
	Cursor string     `json:"cursor,omitempty"`
	Gap    *reportGap `json:"gap,omitempty"`
}

type health struct {
	Version   string    `json:"version"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	StartedAt string    `json:"started_at"`
	Services  []service `json:"services,omitempty"`
}

type service struct {
	Name      string `json:"name"`
	Instances int    `json:"instances"`
}

type response struct {
	status     int
	retryAfter string
	body       []byte
}

// message returns the explanation in an error response from Dagu Console.
func (r *response) message() string {
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(r.body, &body) == nil {
		if body.Message != "" {
			return body.Message
		}
		if body.Error != "" {
			return body.Error
		}
	}
	return strings.TrimSpace(string(r.body))
}

func (r *Reporter) send(ctx context.Context, creds license.CloudCredentials) (*response, error) {
	report := reportRequest{
		Protocol:        protocolVersion,
		LicenseID:       creds.LicenseID,
		ServerID:        creds.ServerID,
		HeartbeatSecret: creds.HeartbeatSecret,
		Health: health{
			Version:   config.Version,
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			StartedAt: r.startedAt.UTC().Format(time.RFC3339),
			Services:  r.services(ctx),
		},
	}
	r.events.fill(&report)
	body, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal report: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, creds.CloudURL+reportPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dagu-oss/"+config.Version)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	r.monitoring.recordSent(time.Now(), report)

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	return &response{
		status:     resp.StatusCode,
		retryAfter: resp.Header.Get("Retry-After"),
		body:       data,
	}, nil
}

// services counts the schedulers holding the scheduler lock and the
// registered coordinators. It returns nil when the registry cannot be read.
func (r *Reporter) services(ctx context.Context) []service {
	if r.registry == nil {
		return nil
	}
	schedulers, err := r.registry.GetServiceMembers(ctx, serviceregistry.ServiceNameScheduler)
	if err != nil {
		logger.Debug(ctx, "Failed to read schedulers for the server report", tag.Error(err))
		return nil
	}
	coordinators, err := r.registry.GetServiceMembers(ctx, serviceregistry.ServiceNameCoordinator)
	if err != nil {
		logger.Debug(ctx, "Failed to read coordinators for the server report", tag.Error(err))
		return nil
	}

	activeSchedulers := 0
	for _, member := range schedulers {
		if member.Status == serviceregistry.ServiceStatusActive {
			activeSchedulers++
		}
	}
	return []service{
		{Name: string(serviceregistry.ServiceNameScheduler), Instances: activeSchedulers},
		{Name: string(serviceregistry.ServiceNameCoordinator), Instances: len(coordinators)},
	}
}
