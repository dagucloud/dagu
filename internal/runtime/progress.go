// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

// ProgressUpdate carries a node state change to the agent that owns the
// progress channel.
//
// When ack is non-nil the sender is waiting for the status snapshot covering
// this change to be persisted, so a node's state reaches durable storage
// before execution continues past it. Receivers must call Ack exactly once
// per update, passing the outcome of the persistence attempt; a zero value
// carries no ack and releases nobody.
type ProgressUpdate struct {
	Node *Node

	ack chan error
}

// Ack releases the sender waiting on this update. err reports whether the
// status snapshot was persisted. Calling it on an update without an ack
// channel is a no-op.
func (u ProgressUpdate) Ack(err error) {
	if u.ack != nil {
		// Buffered with room for this single send, so an abandoned waiter
		// (see Runner.report) can never block the receiver.
		u.ack <- err
	}
}
