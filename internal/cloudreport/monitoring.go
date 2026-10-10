// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// redacted replaces the heartbeat secret in the last report shown to
// administrators.
const redacted = "[redacted]"

var (
	// ErrLevelConfigured is returned when configuration fixes the level.
	ErrLevelConfigured = errors.New("cloud.report is set in configuration")
	// ErrUnknownLevel is returned for a level other than off, health, or runs.
	ErrUnknownLevel = errors.New("unknown report level")
)

// Monitoring is how much a server reports to Dagu Console, and what it sent
// last. Configuration can fix the level; otherwise an administrator chooses
// it and the choice persists. A server whose level is neither configured nor
// chosen reports nothing. One reporter at a time follows a Monitoring.
type Monitoring struct {
	configured config.ReportLevel
	store      StateStore

	// mu serializes changes to the stored choice in this process.
	mu sync.Mutex

	watchMu  sync.Mutex
	onChange func(config.ReportLevel)

	sentMu sync.Mutex
	sent   *sentReport
}

// Status describes what a server reports to Dagu Console.
type Status struct {
	// Level is the level in effect.
	Level config.ReportLevel
	// Configured reports that configuration fixes Level.
	Configured bool
	// Chosen reports that an administrator chose Level.
	Chosen bool
	// NoticeDismissed reports that an administrator dismissed the notice
	// asking them to choose a level.
	NoticeDismissed bool
	// LastReportAt is when Dagu Console received the last report this
	// process sent, or zero before the first.
	LastReportAt time.Time
	// LastReport is the JSON body of that report, with the heartbeat secret
	// redacted.
	LastReport []byte
}

// choice is what an administrator chose, as persisted.
type choice struct {
	Level           config.ReportLevel `json:"level,omitempty"`
	NoticeDismissed bool               `json:"notice_dismissed,omitempty"`
}

type sentReport struct {
	at     time.Time
	report reportRequest
}

// NewMonitoring returns the reporting state of a server. A configured level
// other than empty fixes the level; otherwise store persists the choice.
func NewMonitoring(configured config.ReportLevel, store StateStore) *Monitoring {
	return &Monitoring{configured: configured, store: store}
}

// Status returns the level in effect, how it was decided, and the last
// report sent.
func (m *Monitoring) Status(ctx context.Context) (Status, error) {
	status := Status{Level: m.configured, Configured: m.configured != ""}
	if !status.Configured {
		c, err := m.load(ctx)
		if err != nil {
			return Status{}, err
		}
		status.Level, status.Chosen, status.NoticeDismissed = c.Level, c.Level != "", c.NoticeDismissed
		if !status.Chosen {
			status.Level = config.ReportOff
		}
	}

	m.sentMu.Lock()
	sent := m.sent
	m.sentMu.Unlock()
	if sent != nil {
		body, err := json.Marshal(sent.report)
		if err != nil {
			return Status{}, fmt.Errorf("failed to encode the last report: %w", err)
		}
		status.LastReportAt, status.LastReport = sent.at, body
	}
	return status, nil
}

// SetLevel chooses the level. It fails with ErrLevelConfigured when
// configuration fixes the level, and with ErrUnknownLevel for an unknown one.
func (m *Monitoring) SetLevel(ctx context.Context, level config.ReportLevel) error {
	if m.configured != "" {
		return ErrLevelConfigured
	}
	switch level {
	case config.ReportOff, config.ReportHealth, config.ReportRuns:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownLevel, level)
	}
	if err := m.update(ctx, func(c *choice) { c.Level = level }); err != nil {
		return err
	}

	m.watchMu.Lock()
	onChange := m.onChange
	m.watchMu.Unlock()
	if onChange != nil {
		onChange(level)
	}
	return nil
}

// DismissNotice records that an administrator dismissed the notice asking
// them to choose a level. It does nothing when configuration fixes the level,
// since the notice is never shown then.
func (m *Monitoring) DismissNotice(ctx context.Context) error {
	if m.configured != "" {
		return nil
	}
	return m.update(ctx, func(c *choice) { c.NoticeDismissed = true })
}

// level returns the level in effect.
func (m *Monitoring) level(ctx context.Context) (config.ReportLevel, error) {
	if m.configured != "" {
		return m.configured, nil
	}
	c, err := m.load(ctx)
	if err != nil {
		return "", err
	}
	if c.Level == "" {
		return config.ReportOff, nil
	}
	return c.Level, nil
}

// watch calls fn with each level chosen in this process.
func (m *Monitoring) watch(fn func(config.ReportLevel)) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	m.onChange = fn
}

// recordSent keeps report as the last one Dagu Console received.
func (m *Monitoring) recordSent(at time.Time, report reportRequest) {
	report.HeartbeatSecret = redacted
	m.sentMu.Lock()
	defer m.sentMu.Unlock()
	m.sent = &sentReport{at: at, report: report}
}

func (m *Monitoring) update(ctx context.Context, change func(*choice)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.load(ctx)
	if err != nil {
		return err
	}
	change(&c)
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to encode the report settings: %w", err)
	}
	if err := m.store.Save(ctx, data); err != nil {
		return fmt.Errorf("failed to save the report settings: %w", err)
	}
	return nil
}

func (m *Monitoring) load(ctx context.Context) (choice, error) {
	var c choice
	data, found, err := m.store.Load(ctx)
	if err != nil {
		return c, fmt.Errorf("failed to read the report settings: %w", err)
	}
	if !found {
		return c, nil
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("failed to decode the report settings: %w", err)
	}
	return c, nil
}
