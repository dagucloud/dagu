// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import "time"

// Reasons a FeedLoss gives.
const (
	// LossDeleted reports entries removed before they were read, as by local
	// retention.
	LossDeleted = "deleted"
	// LossRejected reports a stored line that is not a readable entry.
	LossRejected = "rejected"
)

// FeedQuery asks a store's feed for what it holds after a position.
type FeedQuery struct {
	// Position is the previous chunk's Position. Empty starts at the oldest
	// entry the store holds.
	Position []byte
	// Now decides the store's current day.
	Now time.Time
	// Full checks every day the store holds, not only the current one, and
	// reports days removed since the last full read. A read that returned
	// More continues with the same Full.
	Full bool
	// Keep selects the entries to return; nil returns every entry. The rest
	// are passed over, and Position still moves past them.
	Keep func(*Entry) bool
	// Limit caps the entries one read returns. A Limit of 0 returns no
	// entries, only a Position to start from.
	Limit int
}

// FeedChunk is what one feed read returns, in stored order.
type FeedChunk struct {
	Entries []*Entry
	// Bytes is the stored size of Entries.
	Bytes int64
	// Losses are entries the store lost, or lines it could not read, after
	// the query's position.
	Losses []FeedLoss
	// Position resumes after this chunk.
	Position []byte
	// More reports that the read stopped before the end of what the store
	// holds, at its limit or its byte budget.
	More bool
}

// FeedLoss is a time range whose entries a feed cannot return.
type FeedLoss struct {
	// Reason is LossDeleted or LossRejected.
	Reason string
	From   time.Time
	To     time.Time
}
