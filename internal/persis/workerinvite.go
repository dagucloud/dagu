// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrWorkerInviteNotFound is returned when no invite has the given ID
	// or secret hash.
	ErrWorkerInviteNotFound = errors.New("worker invite not found")

	// ErrWorkerInviteEnded is returned when an invite admits no further
	// enrollment: it expired, or it is single use and was used.
	ErrWorkerInviteEnded = errors.New("worker invite has ended")
)

// WorkerInviteStore keeps the invites that let workers enroll for a worker
// certificate. It keeps each invite's secret hash, never the secret.
type WorkerInviteStore interface {
	// Create stores invite under a new random ID and returns it with
	// that ID.
	Create(ctx context.Context, invite WorkerInvite) (WorkerInvite, error)

	// Get returns the invite with id, or [ErrWorkerInviteNotFound].
	Get(ctx context.Context, id string) (WorkerInvite, error)

	// GetBySecretHash returns the invite whose secret hash is hash,
	// whether or not it has ended, or [ErrWorkerInviteNotFound].
	GetBySecretHash(ctx context.Context, hash []byte) (WorkerInvite, error)

	// List returns every invite, oldest first.
	List(ctx context.Context) ([]WorkerInvite, error)

	// Delete removes the invite with id, or returns
	// [ErrWorkerInviteNotFound].
	Delete(ctx context.Context, id string) error

	// Use records one enrollment admitted at at and returns the invite as
	// recorded. It returns [ErrWorkerInviteEnded] when the invite has ended
	// at at, and [ErrWorkerInviteNotFound] when it does not exist. Of
	// concurrent uses of a single-use invite, across every client of the
	// store, at most one succeeds.
	Use(ctx context.Context, id string, at time.Time) (WorkerInvite, error)
}

// WorkerInvite is an invite's record.
type WorkerInvite struct {
	ID string
	// SecretHash is the SHA-256 of the invite's secret.
	SecretHash []byte
	Kind       WorkerInviteKind
	// ExpiresAt is zero for an invite that does not expire.
	ExpiresAt time.Time
	// Address is the host:port the invite was made for, or empty.
	Address string
	// CreatedBy is the user who created the invite, or "peer token".
	CreatedBy string
	CreatedAt time.Time
	// UseCount is the number of enrollments the invite admitted.
	UseCount int
	// LastUsedAt is zero before the first enrollment.
	LastUsedAt time.Time
}

// Ended reports whether the invite admits no enrollment at now.
func (i WorkerInvite) Ended(now time.Time) bool {
	if !i.ExpiresAt.IsZero() && !now.Before(i.ExpiresAt) {
		return true
	}
	return i.Kind == WorkerInviteSingleUse && i.UseCount > 0
}

// WorkerInviteKind tells how many workers an invite enrolls.
type WorkerInviteKind string

const (
	// WorkerInviteSingleUse enrolls one worker.
	WorkerInviteSingleUse WorkerInviteKind = "single_use"
	// WorkerInviteReusable enrolls any number of workers.
	WorkerInviteReusable WorkerInviteKind = "reusable"
)

// Valid reports whether k is a known kind.
func (k WorkerInviteKind) Valid() bool {
	return k == WorkerInviteSingleUse || k == WorkerInviteReusable
}
