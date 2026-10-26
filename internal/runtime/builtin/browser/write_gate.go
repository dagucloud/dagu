// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/goccy/go-yaml"
)

const (
	// kindWrite labels a write the gate decided.
	kindWrite = "write"
	// kindWriteGate labels what the gate reports about itself.
	kindWriteGate = "write_gate"
	// statusUnseen marks a socket whose writes the gate cannot see.
	statusUnseen = "unseen"
	// writeGateFile is the artifact a gated step leaves: what the gate
	// decided and where a rehearsal stopped.
	writeGateFile = "write-gate.json"
)

// commitLeaseSlack lets an operation's approved writes be sent a little
// past its timeout, which the runtime reports before its call ends.
const commitLeaseSlack = 30 * time.Second

// writeBlocked is the failure of an operation in record mode whose write
// the gate blocked: no approved commit point of the operation sends it.
// Nothing was saved; approving the point lets later runs send it.
type writeBlocked struct {
	write writegate.Event
}

func (w writeBlocked) Error() string {
	return fmt.Sprintf("%s: the write gate blocked %s, which no approved commit point of this operation sends; nothing was saved (signature %s)",
		CodeWriteBlocked, w.write.String(), w.write.Signature)
}

// commitAskTimeout bounds how long a step in ask mode waits for a person
// before a commit operation.
const commitAskTimeout = 4 * time.Hour

const commitQuestionHeader = "Browser write"

// commitForSession is the answer that sends a commit operation's writes
// and approves them for later runs.
const commitForSession = "session"

// pointsAt returns the commit points of the operation at key.
func pointsAt(points []writegate.CommitPoint, key string) []writegate.CommitPoint {
	var at []writegate.CommitPoint
	for _, point := range points {
		if key != "" && point.Key == key {
			at = append(at, point)
		}
	}
	return at
}

// signatures returns the signatures of points, each once.
func signatures(points []writegate.CommitPoint) []string {
	var out []string
	for _, point := range points {
		if !slices.Contains(out, point.Signature) {
			out = append(out, point.Signature)
		}
	}
	return out
}

// commitQuestion asks a person whether the operation at index sends the
// writes of its commit points.
func commitQuestion(index int, op operation, points []writegate.CommitPoint) string {
	var writes []string
	for _, point := range points {
		write := point.Signature
		if point.Example != nil {
			write = point.Example.String()
		}
		if !slices.Contains(writes, write) {
			writes = append(writes, write)
		}
	}
	return fmt.Sprintf("do[%d] %q is about to send %s. Send it once, for the session, skip it and end the step without sending, or reject it.",
		index, opWords(op), strings.Join(writes, ", "))
}

// commitKey identifies the operation at index on pageURL as a commit point
// does, the way the replay cache keys acts and uploads; "" for an
// operation of another kind.
func commitKey(index int, op operation, pageURL string) string {
	switch {
	case op.Act != nil:
		return replayKey(index, op.Act.Instruction, pageURL)
	case op.Upload != nil && op.Upload.Into != "":
		return uploadReplayKey(index, op.Upload.Into, pageURL)
	}
	return ""
}

// opWords describes an operation that may be a commit point.
func opWords(op operation) string {
	switch {
	case op.Act != nil:
		return op.Act.Instruction
	case op.Upload != nil:
		return "upload into " + op.Upload.Into
	}
	return op.kind()
}

// errWriteGateLost reports a gate that stopped answering; its browser is
// closed, since nothing decides its writes.
var errWriteGateLost = fmt.Errorf("the write gate stopped answering, so the browser was closed")

// writeGateMode returns the mode a browser runs in: the stricter of asked
// and the host's browser.write_gate.
func writeGateMode(ctx context.Context, asked string) (string, error) {
	floor, err := writegate.ParseMode(cmnconfig.GetConfig(ctx).Browser.WriteGate)
	if err != nil {
		return "", fmt.Errorf("browser.write_gate in the Dagu config: %w", err)
	}
	mode, err := writegate.ParseMode(asked)
	if err != nil {
		return "", err
	}
	return writegate.Stricter(mode, floor), nil
}

// loadWritePolicy reads a write gate policy file, YAML or JSON. An empty
// path is an empty policy.
func loadWritePolicy(path string) (writegate.Policy, error) {
	var policy writegate.Policy
	if path == "" {
		return policy, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the policy file is the user's configuration
	if err != nil {
		return policy, fmt.Errorf("read the write policy: %w", err)
	}
	if err := yaml.UnmarshalWithOptions(data, &policy, yaml.Strict()); err != nil {
		return policy, fmt.Errorf("read the write policy %s: %w", path, err)
	}
	if err := policy.Validate(); err != nil {
		return policy, fmt.Errorf("write policy %s: %w", path, err)
	}
	return policy, nil
}

// writeGateFor returns the gate a browser starts with in mode, following
// policy, or nil for mode off. lookup finds the secrets the policy's
// sign-in rules name; one it cannot find fails, since the rule would never
// match.
func writeGateFor(mode string, policy writegate.Policy, lookup func(name string) (string, bool)) (*writeGateOptions, error) {
	if mode == writegate.ModeOff {
		return nil, nil
	}
	secrets := map[string]string{}
	for _, name := range policy.Secrets() {
		value, ok := lookup(name)
		if !ok || value == "" {
			return nil, fmt.Errorf("the write policy's sign-in rule names the secret %s, which has no value here", name)
		}
		secrets[name] = value
	}
	return &writeGateOptions{Mode: mode, Policy: policy, Secrets: secrets}, nil
}

// logWrite reports what the gate decided while the operation at index ran.
func logWrite(t *agentstep.Timeline, index int, event writegate.Event) {
	if event.Kind == writegate.KindSocket {
		content := t.Masker.MaskString(event.String() + " opened; the write gate cannot see its writes, so the site cannot be rehearsed")
		_, _ = fmt.Fprintf(t.Log, "%s %s %s %s\n", t.Position(index), kindWriteGate, statusUnseen, content)
		t.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventOperation, Name: kindWriteGate, Status: statusUnseen, Content: content})
		return
	}
	content := t.Masker.MaskString(event.String())
	detail := t.Masker.MaskString(event.Class + "; " + event.Signature)
	_, _ = fmt.Fprintf(t.Log, "%s %s %s %s (%s)\n", t.Position(index), kindWrite, event.Outcome, content, detail)
	t.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventOperation, Name: kindWrite, Status: event.Outcome, Content: content + "\n" + detail})
}

// wouldSend describes where a rehearsal stopped: the write it kept back
// and the operations after index, of total, it left.
func wouldSend(event writegate.Event, index, total int) string {
	message := "would send " + event.String() + "; not sent"
	switch first, last := index+1, total-1; {
	case first == last:
		message += fmt.Sprintf("; do[%d] did not run", first)
	case first < last:
		message += fmt.Sprintf("; do[%d]–do[%d] did not run", first, last)
	}
	return message
}

// writeGateReport is what a gated step leaves in writeGateFile.
type writeGateReport struct {
	Mode string `json:"mode"`
	// Rehearsable is false once a socket opened whose writes the gate
	// cannot see.
	Rehearsable bool `json:"rehearsable"`
	// StoppedAt is the operation after which a rehearsal stopped, -1 for
	// the start, and nil when it did not stop.
	StoppedAt *int `json:"stopped_at,omitempty"`
	// WouldSend is the write a rehearsal stopped at.
	WouldSend *writegate.Event `json:"would_send,omitempty"`
	// NotRun are the operations a rehearsal left.
	NotRun []int `json:"not_run,omitempty"`
	// Skipped reports that a person chose not to run the commit operation
	// at StoppedAt, so nothing was sent.
	Skipped bool              `json:"skipped,omitempty"`
	Writes  []writegate.Event `json:"writes"`
}

// maskEvents hides the values masker knows in what the gate reported.
func maskEvents(masker *masking.Masker, events []writegate.Event) []writegate.Event {
	for i := range events {
		events[i].URL = masker.MaskString(events[i].URL)
		events[i].Body = masker.MaskString(events[i].Body)
		events[i].Signature = masker.MaskString(events[i].Signature)
	}
	return events
}

// encodeReport renders a writeGateReport for its file.
func encodeReport(report writeGateReport) ([]byte, error) {
	if report.Writes == nil {
		report.Writes = []writegate.Event{}
	}
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
