// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package webhooksig

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

const (
	// MaxEventLength bounds an event name a webhook takes or a log keeps.
	MaxEventLength = 120
	// MaxEvents bounds how many events a webhook names.
	MaxEvents = 50
)

// eventHeaders are the headers senders name their event in, as GitHub,
// GitLab, Shopify, and Bitbucket do.
var eventHeaders = []string{"X-Github-Event", "X-Gitlab-Event", "X-Shopify-Topic", "X-Event-Key"}

// Event names what a request is about, as its sender labels it: an event
// header, with GitHub's action after a dot, or else the type or event the
// body names, as Stripe and Standard Webhooks senders do, or the type of the
// event Slack wraps. It is empty when the request names none.
func Event(header http.Header, body []byte) string {
	var fields struct {
		Action string          `json:"action"`
		Type   string          `json:"type"`
		Event  json.RawMessage `json:"event"`
	}
	_ = json.Unmarshal(body, &fields)
	var event string
	for _, name := range eventHeaders {
		if event = header.Get(name); event != "" {
			break
		}
	}
	// Slack wraps each event in an event_callback; others name it as text.
	var named string
	var wrapped struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(fields.Event, &named) != nil && fields.Type == "event_callback" &&
		json.Unmarshal(fields.Event, &wrapped) == nil {
		fields.Type = wrapped.Type
	}
	switch {
	case event != "" && fields.Action != "":
		event += "." + fields.Action
	case event == "":
		event = cmp.Or(fields.Type, named)
	}
	if runes := []rune(event); len(runes) > MaxEventLength {
		event = string(runes[:MaxEventLength])
	}
	return event
}

// IsGitHubPing reports whether a request is the ping GitHub sends when a
// webhook is created.
func IsGitHubPing(header http.Header) bool {
	return header.Get("X-Github-Event") == "ping"
}

// Events returns the events a webhook takes as written: trimmed, once each,
// in the order written, and nil when none is named.
func Events(written []string) ([]string, error) {
	events := []string{}
	for _, event := range written {
		event = strings.TrimSpace(event)
		if event == "" || slices.Contains(events, event) {
			continue
		}
		if len([]rune(event)) > MaxEventLength {
			return nil, fmt.Errorf("an event name is at most %d characters", MaxEventLength)
		}
		events = append(events, event)
	}
	if len(events) > MaxEvents {
		return nil, fmt.Errorf("a webhook takes at most %d events", MaxEvents)
	}
	if len(events) == 0 {
		return nil, nil
	}
	return events, nil
}

// Takes reports whether a webhook taking events takes a request about event:
// every request when it names none, else one about an event it names or one
// under it, so issues takes issues.opened.
func Takes(events []string, event string) bool {
	if len(events) == 0 {
		return true
	}
	return slices.ContainsFunc(events, func(taken string) bool {
		return event == taken || strings.HasPrefix(event, taken+".")
	})
}
