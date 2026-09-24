// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// providerName identifies browser steps in agent sessions.
const providerName = browserhost.AgentProvider

// Timeline event types and statuses shown in the step's agent session.
const (
	eventLifecycle = "lifecycle"
	eventOperation = "tool"

	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusSkipped   = "skipped"
	statusWaiting   = "waiting"
	statusCacheHit  = "cache-hit"
	statusHealed    = "healed"
	statusBlocked   = "blocked"
)

const (
	maxEventContentBytes = 4 << 10
	maxSessionEvents     = 1000
	maxLogTextRunes      = 80
)

// timeline reports operation progress to the step log and the agent session.
type timeline struct {
	log    io.Writer
	masker *masking.Masker
	total  int
	update func(func(*ir.AgentSession))
}

// operationReport summarizes one finished operation.
type operationReport struct {
	// index is the position in with.do, or -1 for the navigation to with.url.
	index    int
	kind     string
	subject  string
	status   string
	detail   string
	tokens   int
	duration time.Duration
	files    []string
}

func (t *timeline) lifecycle(status, content string) {
	content = t.masker.MaskString(content)
	_, _ = fmt.Fprintf(t.log, "%s\n", content)
	t.appendEvent(ir.AgentSessionEvent{Type: eventLifecycle, Status: status, Content: content})
}

func (t *timeline) operation(report operationReport) {
	subject := t.masker.MaskString(report.subject)
	detail := t.masker.MaskString(report.detail)
	line := fmt.Sprintf("%s %s %s", t.position(report.index), report.kind, quoteShort(subject))
	if detail != "" {
		line += " → " + detail
	}
	line += fmt.Sprintf(" (%s, %d tokens, %s)", report.status, report.tokens, report.duration.Round(100*time.Millisecond))
	_, _ = fmt.Fprintln(t.log, line)

	content := subject
	if detail != "" {
		content += "\n" + detail
	}
	t.appendEvent(ir.AgentSessionEvent{
		Type:    eventOperation,
		Name:    report.kind,
		Status:  report.status,
		Content: content,
		Files:   report.files,
	})
}

// blocked reports the requests allowed_domains blocked while the operation at
// index ran, summarized by describeBlocked.
func (t *timeline) blocked(index int, summary string) {
	summary = t.masker.MaskString(summary)
	_, _ = fmt.Fprintf(t.log, "%s %s %s %s\n", t.position(index), kindAllowedDomains, statusBlocked, summary)
	t.appendEvent(ir.AgentSessionEvent{Type: eventOperation, Name: kindAllowedDomains, Status: statusBlocked, Content: summary})
}

// position labels the operation at index, or the navigation to with.url when
// index is -1.
func (t *timeline) position(index int) string {
	if index < 0 {
		return "[start]"
	}
	return fmt.Sprintf("[%d/%d]", index+1, t.total)
}

func (t *timeline) appendEvent(event ir.AgentSessionEvent) {
	if len(event.Content) > maxEventContentBytes {
		event.Content = event.Content[:maxEventContentBytes]
	}
	event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	t.update(func(session *ir.AgentSession) {
		sequence := int64(1)
		if count := len(session.Events); count > 0 {
			sequence = session.Events[count-1].Sequence + 1
		}
		event.Sequence = sequence
		event.ID = fmt.Sprintf("browser-%d-%d", session.Generation, sequence)
		session.Events = append(session.Events, event)
		if overflow := len(session.Events) - maxSessionEvents; overflow > 0 {
			session.Events = append([]ir.AgentSessionEvent(nil), session.Events[overflow:]...)
		}
	})
}

func quoteShort(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > maxLogTextRunes {
		text = string(runes[:maxLogTextRunes]) + "…"
	}
	return fmt.Sprintf("%q", text)
}
