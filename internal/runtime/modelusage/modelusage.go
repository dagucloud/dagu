// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package modelusage keeps what a step's calls to models use while the step
// runs. A step's context carries its meter, and every model call made with
// that context adds to it.
package modelusage

import (
	"context"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
)

// Meter adds up a step's calls to models, on top of what the step's earlier
// attempts used.
type Meter struct {
	mu       sync.Mutex
	models   []ir.ModelUsage
	recorded uint64
	onRecord func()
}

// NewMeter returns a meter that starts from what the step used before, which
// may be nil.
func NewMeter(base *ir.StepModelUsage) *Meter {
	m := &Meter{}
	if base != nil {
		m.models = ir.MergeModelUsage(nil, base.Models...)
	}
	return m
}

// RecordLLMUsage adds one call.
func (m *Meter) RecordLLMUsage(record llm.UsageRecord) {
	entry := ir.ModelUsage{
		Provider:          record.Provider,
		Model:             record.Model,
		Calls:             1,
		InputTokens:       int64(record.Usage.PromptTokens),
		CachedInputTokens: int64(record.Usage.CachedPromptTokens),
		CacheWriteTokens:  int64(record.Usage.CacheWritePromptTokens),
		OutputTokens:      int64(record.Usage.CompletionTokens),
		ReasoningTokens:   int64(record.Usage.ReasoningTokens),
	}
	if record.Usage.Cost != nil {
		entry.Cost = *record.Usage.Cost
		entry.CostReportedCalls = 1
	}
	m.mu.Lock()
	m.models = ir.MergeModelUsage(m.models, entry)
	m.recorded++
	onRecord := m.onRecord
	m.mu.Unlock()
	if onRecord != nil {
		onRecord()
	}
}

// Snapshot returns what the step has used so far, or nil when it has used
// nothing.
func (m *Meter) Snapshot() *ir.StepModelUsage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.models) == 0 {
		return nil
	}
	return ir.CloneStepModelUsage(&ir.StepModelUsage{Models: m.models})
}

func (m *Meter) recordedCalls() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recorded
}

// Flusher reports a meter's usage while its step runs, at most once an
// interval and only after a call was recorded since the last report, so a
// step that calls no model never reports.
type Flusher struct {
	meter  *Meter
	every  time.Duration
	report func(*ir.StepModelUsage)

	mu       sync.Mutex // guards timer and stopped
	timer    *time.Timer
	stopped  bool
	reportMu sync.Mutex // held while reporting
	reported uint64
}

// StartFlusher has report called with the meter's usage, at most once every
// interval, after the meter records a call. Stop it before the step ends.
func (m *Meter) StartFlusher(every time.Duration, report func(*ir.StepModelUsage)) *Flusher {
	f := &Flusher{meter: m, every: every, report: report}
	m.mu.Lock()
	m.onRecord = f.arm
	m.mu.Unlock()
	return f
}

func (f *Flusher) arm() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped || f.timer != nil {
		return
	}
	f.timer = time.AfterFunc(f.every, f.fire)
}

func (f *Flusher) fire() {
	// Stop is seen while holding reportMu, so either this report sees it or
	// Stop waits for this report.
	f.reportMu.Lock()
	defer f.reportMu.Unlock()
	f.mu.Lock()
	f.timer = nil
	stopped := f.stopped
	f.mu.Unlock()
	if stopped {
		return
	}
	recorded := f.meter.recordedCalls()
	if recorded == f.reported {
		return
	}
	f.reported = recorded
	f.report(f.meter.Snapshot())
}

// Stop ends the reports. When it returns, no report is under way and none
// will start.
func (f *Flusher) Stop() {
	f.meter.mu.Lock()
	f.meter.onRecord = nil
	f.meter.mu.Unlock()
	f.mu.Lock()
	f.stopped = true
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.mu.Unlock()
	f.reportMu.Lock()
	defer f.reportMu.Unlock()
}

type meterKey struct{}

// WithMeter returns a context whose model calls add to m. A nil m hides any
// meter ctx carries, so calls made with the returned context count nowhere.
func WithMeter(ctx context.Context, m *Meter) context.Context {
	ctx = context.WithValue(ctx, meterKey{}, m)
	if m == nil {
		return llm.WithUsageSink(ctx, nil)
	}
	return llm.WithUsageSink(ctx, m)
}

// FromContext returns the meter ctx carries, or nil.
func FromContext(ctx context.Context) *Meter {
	m, _ := ctx.Value(meterKey{}).(*Meter)
	return m
}
