// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

// ProgressUpdate carries a node state change to the agent that owns the
// progress channel.
//
// When ack is non-nil the sender is waiting for the status snapshot covering
// this change to be persisted, so a node's state reaches durable storage
// before execution continues past it. Receivers release the sender by calling
// Ack with the outcome of that attempt; a zero value carries no ack and
// releases nobody.
type ProgressUpdate struct {
	Node *Node

	ack chan error
}

// Ack releases the sender waiting on this update. err reports whether the
// status snapshot was persisted. The first call decides the outcome; later
// calls, and calls on an update carrying no ack channel, are no-ops.
//
// Ack never blocks, so a receiver cannot be wedged by a sender that stopped
// waiting.
func (u ProgressUpdate) Ack(err error) {
	if u.ack == nil {
		return
	}
	select {
	case u.ack <- err:
	default:
	}
}
