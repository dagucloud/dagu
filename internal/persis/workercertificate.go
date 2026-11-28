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
	// Create records a newly issued certificate, or returns
	// [ErrWorkerCertificateExists] when a record with its ID exists. The
	// certificate is active unless its ID was removed before; certificate
	// IDs are random, so a new one never was.
	Create(ctx context.Context, cert WorkerCertificate) error

	// Get returns the record with id, or [ErrWorkerCertificateNotFound].
	Get(ctx context.Context, id string) (WorkerCertificate, error)

	// List returns every record, oldest first.
	List(ctx context.Context) ([]WorkerCertificate, error)

	// WorkerRemovals returns how many removals of the worker ID workerID
	// began, zero when none did.
	WorkerRemovals(ctx context.Context, workerID string) (int64, error)

	// BeginWorkerRemoval counts one more removal of the worker ID
	// workerID, before the removal looks for its certificates, and returns
	// how many removals of it began, this one included.
	BeginWorkerRemoval(ctx context.Context, workerID string) (int64, error)

	// ConfirmEnrollment reports whether the removals of the worker ID
	// workerID still number removals, and records an enrollment of it if
	// so, in one write that a removal beginning at the same time cannot
	// pass unnoticed. A false result means a removal of the worker began
	// during the enrollment.
	ConfirmEnrollment(ctx context.Context, workerID string, removals int64) (bool, error)

	// ListRemovals returns the certificates in state removing or revoked.
	// Its cost grows with those certificates only, not with every record.
	// It fails when one cannot be read, so no revoked certificate is left
	// out.
	ListRemovals(ctx context.Context) ([]WorkerCertificateRemoval, error)

	// RecordHeartbeat records a heartbeat sent with the certificate at
	// cert.LastHeartbeatAt, with the worker ID, host, and environment it
	// reported. A report not newer than the stored heartbeat writes nothing.
	// Otherwise it writes when the stored heartbeat is more than a day
	// older, when the worker ID is not among the stored worker IDs, which
	// it joins, when it returns after another ID and was last seen more
	// than a minute before, or when it is at least a minute older and the
	// host or environment changed. Of the worker IDs, the least recently
	// seen goes beyond eight. It keeps the stored state. A pruned or
	// unreadable record is recorded again from cert.
	RecordHeartbeat(ctx context.Context, cert WorkerCertificate) error

	// MarkRemoving moves an active certificate to removing, naming workerID
	// as the worker that holds it, at the time now gives as the record is
	// written. With stopRuns, it also records, in the same write, that the
	// removal asks the worker's runs to stop, and that time. A removing
	// certificate keeps the time it became removing and the time of its
	// first stop request, and takes workerID; a revoked one stays as it
	// is. It returns the record as stored, or
	// [ErrWorkerCertificateNotFound].
	MarkRemoving(ctx context.Context, id, workerID string, now func() time.Time, stopRuns bool) (WorkerCertificate, error)

	// ConfirmAcceptance records an acceptance of a task with the
	// certificate id on its removal record, when it has one that is not
	// revoked, in one write from the version it read. It returns the
	// certificate as stored, also when there is nothing to record.
	ConfirmAcceptance(ctx context.Context, id string) (WorkerCertificate, error)

	// RevokeIfIdle revokes the removing certificate id when idle, which
	// runs after the removal record is read and gets the certificate as
	// read, reports that it may be. The revocation is written from that
	// version, so an acceptance recorded after the read makes it read and
	// ask idle again. It returns the record as stored and whether it
	// revoked.
	RevokeIfIdle(ctx context.Context, id string, idle func(context.Context, WorkerCertificate) (bool, error)) (WorkerCertificate, bool, error)

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
	// WorkerIDs are the worker IDs the certificate's recorded heartbeats
	// reported, the least recently seen first, at most eight. A record
	// written before the field has none, and names only WorkerID.
	WorkerIDs []WorkerIDSeen
	// Host is the host name the worker reported.
	Host        string
	Environment WorkerEnvironment
	// CreatedAt is the enrollment time.
	CreatedAt time.Time
	// LastHeartbeatAt is zero before the first recorded heartbeat.
	LastHeartbeatAt time.Time
	// EnrolledAs is the worker ID the certificate enrolled as, and
	// EnrolledRemovals the [WorkerCertificateStore.WorkerRemovals] of it
	// when the enrollment started. A record written before the fields has
	// neither.
	EnrolledAs       string
	EnrolledRemovals int64
	// RemovingAt is when the certificate became removing, or zero.
	RemovingAt time.Time
	// HolderID is the worker a removal named as holding the certificate, or
	// empty. Heartbeats leave it as it is.
	HolderID string
	// StopRequested is set when its removal asks the worker's runs to
	// stop, and StopRunsAt is when it first asked.
	StopRequested bool
	StopRunsAt    time.Time
}

// WorkerCertificateRemoval is a certificate in state removing or revoked.
type WorkerCertificateRemoval struct {
	ID string
	// KeyFingerprint is the certificate's [WorkerCertificate.KeyFingerprint].
	KeyFingerprint []byte
	State          WorkerCertificateState
	// WorkerID is the worker the removal named, or the certificate's
	// [WorkerCertificate.WorkerID] when it named none.
	WorkerID      string
	RemovingAt    time.Time
	StopRequested bool
	StopRunsAt    time.Time
}

// WorkerIDSeen is a worker ID a certificate's heartbeats reported, and when
// one last did, as recorded.
type WorkerIDSeen struct {
	ID     string
	SeenAt time.Time
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
