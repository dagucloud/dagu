// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
	NotRun []int             `json:"not_run,omitempty"`
	Writes []writegate.Event `json:"writes"`
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
