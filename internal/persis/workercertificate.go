// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrWorkerCertificateNotFound is returned when no certificate record
	// has the given ID.
	ErrWorkerCertificateNotFound = errors.New("worker certificate not found")

	// ErrWorkerCertificateExists is returned when a certificate record
	// with the given ID already exists.
	ErrWorkerCertificateExists = errors.New("worker certificate already exists")

	// ErrWorkerCertificateStateBackward is returned when a state change
	// would move a certificate back, such as from revoked to active.
	ErrWorkerCertificateStateBackward = errors.New("worker certificate state cannot move back")
)

// WorkerCertificateStore keeps a record of each worker certificate a
// coordinator issued.
type WorkerCertificateStore interface {
	// Create records a newly issued certificate in state active, or
	// returns [ErrWorkerCertificateExists].
	Create(ctx context.Context, cert WorkerCertificate) error

	// Get returns the record with id, or [ErrWorkerCertificateNotFound].
	Get(ctx context.Context, id string) (WorkerCertificate, error)

	// List returns every record, oldest first. It fails when a record
	// cannot be read, so no revoked certificate is left out.
	List(ctx context.Context) ([]WorkerCertificate, error)

	// RecordHeartbeat records a heartbeat sent with the certificate at
	// cert.LastHeartbeatAt, with the worker ID, host, and environment it
	// reported. It writes only when the stored heartbeat is more than a day
	// older or a reported field changed, and keeps the stored state. A
	// pruned record is recorded again from cert, in state active.
	RecordHeartbeat(ctx context.Context, cert WorkerCertificate) error

	// SetState moves the certificate to state and returns the record as
	// stored. States only move forward, from active to removing to revoked;
	// setting the current state changes nothing. It returns
	// [ErrWorkerCertificateStateBackward] for a move back and
	// [ErrWorkerCertificateNotFound] when no record exists.
	SetState(ctx context.Context, id string, state WorkerCertificateState) (WorkerCertificate, error)

	// Prune deletes the records in state active whose last heartbeat, or
	// enrollment before the first, is more than 90 days before now. It
	// returns the number deleted. Records in other states stay.
	Prune(ctx context.Context, now time.Time) (int, error)
}

// WorkerCertificate is a worker certificate's record.
type WorkerCertificate struct {
	ID string
	// InviteID is the invite the worker enrolled with.
	InviteID string
	// KeyFingerprint is the SHA-256 of the certificate's public key in
	// DER form.
	KeyFingerprint []byte
	State          WorkerCertificateState
	// WorkerID is the worker ID at the last recorded heartbeat.
	WorkerID string
	// Host is the host name the worker reported.
	Host        string
	Environment WorkerEnvironment
	// CreatedAt is the enrollment time.
	CreatedAt time.Time
	// LastHeartbeatAt is zero before the first recorded heartbeat.
	LastHeartbeatAt time.Time
}

// WorkerCertificateState is where a certificate is in its removal.
type WorkerCertificateState string

const (
	// WorkerCertificateActive is a certificate in use.
	WorkerCertificateActive WorkerCertificateState = "active"
	// WorkerCertificateRemoving is a certificate whose worker gets no new
	// tasks and is revoked once its tasks end.
	WorkerCertificateRemoving WorkerCertificateState = "removing"
	// WorkerCertificateRevoked is a certificate no coordinator accepts.
	WorkerCertificateRevoked WorkerCertificateState = "revoked"
)

// WorkerEnvironment is where a worker runs, as its heartbeat reports.
type WorkerEnvironment string

const (
	WorkerEnvironmentHost       WorkerEnvironment = "host"
	WorkerEnvironmentContainer  WorkerEnvironment = "container"
	WorkerEnvironmentKubernetes WorkerEnvironment = "kubernetes"
)
