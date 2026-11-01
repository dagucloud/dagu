// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package publicwebhook gives per-DAG webhooks public URLs through the Dagu
// Cloud relay: it holds the relay connection, keeps the relay's set of URLs
// in step with the webhooks, and starts a run for each delivery it accepts.
package publicwebhook

import (
	"context"
	"time"
)

// LogSize is how many deliveries a webhook's log keeps.
const LogSize = 20

// What became of a delivery, as its log shows it. A waiting delivery is back
// at the relay, which sends it again; a rejected one failed its sender's
// signature check; a skipped one is about an event that starts no run.
const (
	OutcomeStarted  = "started"
	OutcomeWaiting  = "waiting"
	OutcomeRejected = "rejected"
	OutcomeSkipped  = "skipped"
	OutcomeIgnored  = "ignored"
	OutcomeFailed   = "failed"
)

// Delivery is one request the relay handed over for a webhook, as its log
// keeps it. The body is not kept; a started delivery's body is in its run.
type Delivery struct {
	ID       string    `json:"id"`
	Received time.Time `json:"received"`
	Size     int       `json:"size"`
	// Event names what the request is about, as its sender labels it.
	Event   string `json:"event,omitempty"`
	Outcome string `json:"outcome"`
	// RunID names the run a started delivery started.
	RunID string `json:"runId,omitempty"`
	// Error says why a delivery was rejected, skipped, or failed.
	Error string `json:"error,omitempty"`
}

// Missed counts the deliveries the relay dropped for a webhook since an
// admin last dismissed them: those that waited past 7 days, and those it
// refused while 1,000 waited.
type Missed struct {
	Expired int       `json:"expired"`
	Refused int       `json:"refused"`
	Since   time.Time `json:"since,omitzero"`
}

// Log is a webhook's latest deliveries, newest first, and its missed counts.
type Log struct {
	Deliveries []Delivery `json:"deliveries,omitempty"`
	Missed     Missed     `json:"missed"`
}

// Status is the relay connection as the scheduler that holds it last saw it.
type Status struct {
	// Connected is set while the connection is open.
	Connected bool `json:"connected"`
	// URL is what this server's public webhook URLs start with, once the
	// relay has said.
	URL string `json:"url,omitempty"`
	// MaxHooks is the most public URLs the plan allows, from the latest
	// ticket.
	MaxHooks int `json:"maxHooks,omitempty"`
	// LeftOut names the DAGs whose public URLs exceed MaxHooks and are not
	// held at the relay.
	LeftOut []string `json:"leftOut,omitempty"`
	// Holding is set while the relay holds URLs this server sent, so a URL
	// turned off while the server was away is dropped when it returns.
	Holding bool `json:"holding,omitempty"`
	// Error says why the server is not connected.
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store keeps the relay's status and each public webhook's log. Several
// processes share it: the scheduler writes deliveries and status, and the
// server reads them and dismisses missed counts.
type Store interface {
	// Log returns a webhook's log; an empty one when it has none.
	Log(ctx context.Context, webhookID string) (Log, error)
	// AddDelivery puts d first in a webhook's log, replacing an entry for the
	// same delivery, and keeps the latest LogSize.
	AddDelivery(ctx context.Context, webhookID string, d Delivery) error
	// AddMissed adds to a webhook's missed counts.
	AddMissed(ctx context.Context, webhookID string, expired, refused int, at time.Time) error
	// ClearMissed dismisses a webhook's missed counts.
	ClearMissed(ctx context.Context, webhookID string) error
	// DeleteLog removes a webhook's log, as when its public URL is turned off.
	DeleteLog(ctx context.Context, webhookID string) error
	// Status returns the relay's status; the zero Status before any.
	Status(ctx context.Context) (Status, error)
	// SetStatus replaces the relay's status.
	SetStatus(ctx context.Context, status Status) error
}
