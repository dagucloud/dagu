// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Timeline event types and statuses shown in a step's agent session.
const (
	EventLifecycle = "lifecycle"
	EventOperation = "tool"

	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
	StatusWaiting   = "waiting"
	StatusCacheHit  = "cache-hit"
	StatusHealed    = "healed"

	// How an operation ran, carried as the via of its event: a recording
	// replayed on the screen without a model, the model, or an exact check
	// that read the window's elements without one.
	ViaScreen = "screen"
	ViaModel  = "model"
	ViaExact  = "exact"
)

const (
	maxEventContentBytes = 4 << 10
	maxSessionEvents     = 1000
	maxLogTextRunes      = 80
)

// Timeline reports operation progress to the step log and the agent
// session.
type Timeline struct {
	Log    io.Writer
	Masker *masking.Masker
	// Total is the number of operations in with.do.
	Total int
	// Update applies a change to the agent session.
	Update func(func(*ir.AgentSession))
	// Provider prefixes event IDs.
	Provider string
}

// Report summarizes one finished operation.
type Report struct {
	// Index is the position in with.do, or -1 for work before the first
	// operation.
	Index   int
	Kind    string
	Subject string
	Status  string
	// Via is how the operation ran, ViaScreen, ViaModel, or ViaExact, or
	// empty for one that decides nothing, such as a wait.
	Via      string
	Detail   string
	Tokens   int
	Duration time.Duration
	Files    []string
}

// Lifecycle logs a step-level event.
func (t *Timeline) Lifecycle(status, content string) {
	t.lifecycle("", status, content)
}

// Waiting logs that the step waits, naming the reason so programs can tell
// waits apart without reading the content.
func (t *Timeline) Waiting(reason, content string) {
	t.lifecycle(reason, StatusWaiting, content)
}

func (t *Timeline) lifecycle(name, status, content string) {
	content = t.Masker.MaskString(content)
	_, _ = fmt.Fprintf(t.Log, "%s\n", content)
	t.AppendEvent(ir.AgentSessionEvent{Type: EventLifecycle, Name: name, Status: status, Content: content})
}

// Operation logs a finished operation.
func (t *Timeline) Operation(report Report) {
	subject := t.Masker.MaskString(report.Subject)
	detail := t.Masker.MaskString(report.Detail)
	line := fmt.Sprintf("%s %s %s", t.Position(report.Index), report.Kind, QuoteShort(subject))
	if detail != "" {
		line += " → " + detail
	}
	facts := []string{report.Status, fmt.Sprintf("%d tokens", report.Tokens), report.Duration.Round(100 * time.Millisecond).String()}
	if report.Via != "" {
		facts = append(facts, "via "+report.Via)
	}
	_, _ = fmt.Fprintf(t.Log, "%s (%s)\n", line, strings.Join(facts, ", "))

	content := subject
	if detail != "" {
		content += "\n" + detail
	}
	t.AppendEvent(ir.AgentSessionEvent{
		Type:    EventOperation,
		Name:    report.Kind,
		Status:  report.Status,
		Content: content,
		Files:   report.Files,
		Via:     report.Via,
		// Milliseconds suit a timeline; a sub-millisecond operation reads
		// as instant.
		DurationMs: report.Duration.Milliseconds(),
		Tokens:     int64(report.Tokens),
	})
}

// Position labels the operation at index, or the work before the first
// operation when index is -1.
func (t *Timeline) Position(index int) string {
	if index < 0 {
		return "[start]"
	}
	return fmt.Sprintf("[%d/%d]", index+1, t.Total)
}

// AppendEvent adds an event to the agent session, keeping the latest
// events when there are too many.
func (t *Timeline) AppendEvent(event ir.AgentSessionEvent) {
	event.Content = truncate(event.Content, maxEventContentBytes)
	event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	t.Update(func(session *ir.AgentSession) {
		sequence := int64(1)
		if count := len(session.Events); count > 0 {
			sequence = session.Events[count-1].Sequence + 1
		}
		event.Sequence = sequence
		event.ID = fmt.Sprintf("%s-%d-%d", t.Provider, session.Generation, sequence)
		session.Events = append(session.Events, event)
		if overflow := len(session.Events) - maxSessionEvents; overflow > 0 {
			session.Events = append([]ir.AgentSessionEvent(nil), session.Events[overflow:]...)
		}
	})
}

// truncate cuts text to at most limit bytes without splitting a character.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// QuoteShort quotes text on one line, cut to a readable length.
func QuoteShort(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > maxLogTextRunes {
		text = string(runes[:maxLogTextRunes]) + "…"
	}
	return fmt.Sprintf("%q", text)
}
