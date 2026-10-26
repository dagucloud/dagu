// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package writegate decides every write a browser sends: it lets the write
// through, or blocks it and keeps what it would have sent. A write is any
// request whose method is not GET, HEAD, or OPTIONS, and any request a
// policy declares a write.
//
//	step or session ── loopback HTTP + token ──▶ write gate process
//	                                               │ DevTools Fetch on every request
//	                                               ▼
//	                                             browser
//
// The gate runs as a process of its own, beside the browser it guards, and
// answers each paused request at once: a request it held would be sent the
// moment its connection to the browser ended. It arms itself on the browser
// target before any page loads, so pages, frames, popups, workers, and
// service workers opened later are covered without arming each one. When
// its connection to a browser that still runs is lost, it closes that
// browser.
package writegate

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
)

// Modes, from the most permissive. A browser runs in the stricter of the
// mode its step or session asks for and the host's floor.
const (
	// ModeOff runs no gate.
	ModeOff = "off"
	// ModeRehearse blocks every write and keeps what it would have sent.
	ModeRehearse = "rehearse"
)

// modeRank orders the modes by strictness.
var modeRank = map[string]int{ModeOff: 0, ModeRehearse: 1}

// ParseMode returns the mode text names; empty is ModeOff.
func ParseMode(text string) (string, error) {
	if text == "" {
		return ModeOff, nil
	}
	if _, ok := modeRank[text]; !ok {
		return "", fmt.Errorf("write gate mode %q must be %s or %s", text, ModeOff, ModeRehearse)
	}
	return text, nil
}

// Stricter returns the stricter of two modes.
func Stricter(a, b string) string {
	if modeRank[b] > modeRank[a] {
		return b
	}
	return a
}

// Policy names the writes a gate treats apart from the others.
type Policy struct {
	// SignIn are the sign-in endpoints whose writes are sent.
	SignIn []Rule `json:"sign_in,omitempty" yaml:"sign_in,omitempty"`
	// Writes declare requests that save although their method only reads,
	// such as a delete link.
	Writes []Rule `json:"writes,omitempty" yaml:"writes,omitempty"`
	// NoiseHosts are hosts whose writes, such as analytics, are blocked
	// without counting as the work's writes.
	NoiseHosts []string `json:"noise_hosts,omitempty" yaml:"noise_hosts,omitempty"`
}

// Rule matches requests by host and path.
type Rule struct {
	// Host is a host pattern, as the hostpattern package matches it.
	Host string `json:"host" yaml:"host"`
	// Path is the request path; one ending in * matches every path it
	// starts. Empty matches every path.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Secret names a secret whose value the request must carry, as a
	// sign-in carries its password.
	Secret string `json:"secret,omitempty" yaml:"secret,omitempty"`
}

// Validate rejects a policy whose rules cannot match.
func (p Policy) Validate() error {
	for _, rules := range [][]Rule{p.SignIn, p.Writes} {
		for _, rule := range rules {
			if err := hostpattern.Validate(rule.Host); err != nil {
				return err
			}
		}
	}
	for _, host := range p.NoiseHosts {
		if err := hostpattern.Validate(host); err != nil {
			return err
		}
	}
	return nil
}

// Secrets returns the names of the secrets the policy's rules name.
func (p Policy) Secrets() []string {
	var names []string
	for _, rule := range p.SignIn {
		if rule.Secret != "" {
			names = append(names, rule.Secret)
		}
	}
	return names
}

// Kinds of Event.
const (
	// KindWrite is a write the gate decided.
	KindWrite = "write"
	// KindSocket is a WebSocket a service worker or shared worker opened,
	// whose writes the gate cannot see.
	KindSocket = "socket"
)

// Classes of a write.
const (
	// ClassWrite is a write of the work, one the gate exists for.
	ClassWrite = "write"
	// ClassSignIn is a write to a sign-in endpoint the policy lists.
	ClassSignIn = "sign_in"
	// ClassNoise is a beacon, a report, or a write to a noise host.
	ClassNoise = "noise"
	// ClassPerson is a write sent while a person holds the browser.
	ClassPerson = "person"
)

// Outcomes of a write.
const (
	OutcomeBlocked = "blocked"
	OutcomeSent    = "sent"
)

// Event is a write the gate decided, or a socket it saw open.
type Event struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	// Method and URL are the request's, with sensitive query values
	// hidden.
	Method       string `json:"method,omitempty"`
	URL          string `json:"url"`
	ResourceType string `json:"resource_type,omitempty"`
	Class        string `json:"class,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	// Signature identifies the kind of write across runs, apart from the
	// values it carries.
	Signature   string `json:"signature,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	// Fields are the names of the body's fields.
	Fields []string `json:"fields,omitempty"`
	// Body is the start of the body, with sensitive fields hidden.
	Body string `json:"body,omitempty"`
	// BodyBytes is the size of the whole body as the browser reported it.
	BodyBytes int64 `json:"body_bytes,omitempty"`
	// Target is the kind of worker that opened a socket.
	Target string `json:"target,omitempty"`
}

// Consequential reports whether the event is a write of the work the gate
// kept from being sent.
func (e Event) Consequential() bool {
	return e.Kind == KindWrite && e.Class == ClassWrite && e.Outcome == OutcomeBlocked
}

// String describes the event in a few words, such as "POST
// https://example.com/deals".
func (e Event) String() string {
	if e.Kind == KindSocket {
		return fmt.Sprintf("a %s WebSocket to %s", e.Target, e.URL)
	}
	return e.Method + " " + e.URL
}

// IsWrite reports whether a request of method writes, before any policy.
func IsWrite(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// errNoToken reports a config without the token of the control paths.
var errNoToken = errors.New("the write gate config has no token")
