// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// failure is a worker failure as Spec 108 §Messages states it: the cause
// and the next step.
type failure struct {
	cause string
	next  string
}

func (f failure) Error() string {
	return f.cause + "\n  " + f.next
}

// logLine is the failure on one line, for a log.
func (f failure) logLine() string {
	return f.cause + ". " + f.next
}

var errInviteRefused = failure{
	cause: "The invite is invalid, expired, or already used",
	next:  `Get a new command from "Add worker". For several hosts, use a reusable invite.`,
}

func errCannotReach(address string) failure {
	return failure{
		cause: "Cannot reach coordinator at " + address,
		next: fmt.Sprintf("Check that the coordinator accepts workers from other hosts, that port %s is open on the way, and that the address is right.",
			portOf(address)),
	}
}

func errOtherDagu(address string) failure {
	return failure{
		cause: address + " belongs to a different Dagu (its CA does not match the invite)",
		next:  "Check the coordinator address.",
	}
}

func errOwnCertificates(address string) failure {
	return failure{
		cause: "Coordinator at " + address + " serves TLS with its own certificates, so this worker cannot join it with an invite",
		next:  "Remove peer.cert_file and peer.key_file from the coordinator's config, so it serves built-in TLS.",
	}
}

func errNoBuiltinTLS(address string) failure {
	return failure{
		cause: "Coordinator at " + address + " does not serve built-in TLS, so this worker cannot join it",
		next:  `Upgrade the coordinator, and check that its start line reads "Coordinator transport: built-in TLS".`,
	}
}

func errAnswered(address, message string) failure {
	return failure{
		cause: "Coordinator at " + address + " could not enroll this worker: " + message,
		next:  "Check the coordinator's log for the cause.",
	}
}

// answered reports an error that a coordinator returned, rather than one of
// reaching it.
func answered(err error) bool {
	code := status.Code(err)
	return code != codes.Unavailable && code != codes.DeadlineExceeded && code != codes.Canceled && code != codes.Unknown
}

func errInvalidReply(address string) failure {
	return failure{
		cause: "Coordinator at " + address + " returned a certificate that does not match this worker's request",
		next:  "Check the coordinator address.",
	}
}

// Membership is how a worker calls its coordinators: with the peer token, as
// before Spec 108, or with the worker certificate it holds or obtains with
// worker.invite. Close releases what it holds.
type Membership struct {
	cfg    *config.Config
	opts   joinOptions
	store  credentialStore
	config *coordinator.Config
	// invite is worker.invite, nil without one.
	invite *coordinator.Invite
	// writable reports that the worker keeps its credential in its data
	// directory. Otherwise it enrolls with a key in memory.
	writable bool
	// cred is the credential the worker uses, nil until it enrolls or on
	// the peer token.
	cred *credential
	lock *fileutil.FileLock
}

// joinOptions are what a Membership waits, enrolls, and retries with.
type joinOptions struct {
	lock        lockTiming
	enroll      func(context.Context, *coordinator.Config, string, *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error)
	retryPolicy backoff.RetryPolicy
}

// OpenMembership prepares how the worker calls its coordinators, by Spec 108
// §Worker Start. A worker that uses a kept worker certificate, or enrolls
// for one that it keeps, locks it first, and fails when another live worker
// keeps it for 30 seconds or the lock cannot be taken. Every other worker
// behaves as before Spec 108. localRegistry is the service registry in the
// worker's data directory.
func OpenMembership(ctx context.Context, cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (*Membership, error) {
	return openMembership(ctx, cfg, localRegistry, joinOptions{
		lock:        defaultLockTiming,
		enroll:      coordinator.Enroll,
		retryPolicy: enrollRetryPolicy(),
	})
}

func openMembership(ctx context.Context, cfg *config.Config, localRegistry serviceregistry.ServiceRegistry, opts joinOptions) (*Membership, error) {
	if len(cfg.Worker.Coordinators) == 0 {
		return nil, ErrNoCoordinators
	}
	// Whatever stops the worker at start does so before it enrolls, so it
	// never uses up a single-use invite.
	if _, err := workerLabels(cfg.Worker.Labels); err != nil {
		return nil, err
	}
	m := &Membership{
		cfg:    cfg,
		opts:   opts,
		store:  newCredentialStore(cfg.Paths.DataDir),
		config: clientConfigOf(cfg, localRegistry),
	}
	if err := m.open(ctx); err != nil {
		_ = m.Close()
		return nil, err
	}
	if err := m.config.Validate(); err != nil {
		_ = m.Close()
		return nil, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	return m, nil
}

func (m *Membership) open(ctx context.Context) error {
	if m.cfg.Worker.Invite == "" {
		return m.openWithoutInvite(ctx)
	}
	invite, ok := coordinator.ParseInvite(m.cfg.Worker.Invite)
	if !ok {
		return errInviteRefused
	}
	m.invite = &invite
	if err := checkCACertFile(m.cfg.Core.Peer.CACertFile, invite); err != nil {
		return err
	}
	if m.cfg.Core.Peer.Token != "" {
		logger.Info(ctx, "peer.token is not sent: this worker calls the coordinator with the worker certificate it enrolls for with worker.invite")
	}
	m.config = m.config.WithInvite(invite)
	kept, err := m.store.kept()
	if err != nil {
		return m.errUnreadable(err)
	}
	if !kept && !m.store.writable() {
		// Nothing is kept to share, so the worker enrolls with a key in
		// memory, at each start.
		return nil
	}
	if err := m.takeLock(ctx); err != nil {
		return err
	}
	m.writable = m.store.writable()
	cred, err := m.store.load()
	if err != nil {
		return m.errUnreadable(err)
	}
	// A certificate from the invite's CA makes a restart keep it, so a
	// single-use invite is not used up. Another CA's certificate gives way
	// to a new enrollment.
	if cred != nil && invite.Names(cred.ca) {
		m.use(cred)
	}
	return nil
}

// openWithoutInvite uses the worker certificate in the data directory, if
// any, unless peer.insecure: true or own TLS files leave it unused.
func (m *Membership) openWithoutInvite(ctx context.Context) error {
	peer := m.cfg.Core.Peer
	switch {
	case peer.Insecure && !peer.InsecureUnset:
		if kept, _ := m.store.kept(); kept {
			logger.Warn(ctx, "Worker certificate unused: peer.insecure is true, so this worker uses h2c and the peer token; remove peer.insecure: true to use it")
		}
		return nil
	case !peer.Insecure && (peer.CertFile != "" || peer.KeyFile != "" || peer.ClientCaFile != "" || peer.SkipTLSVerify):
		return nil
	}
	kept, err := m.store.kept()
	if err != nil {
		return m.errUnreadable(err)
	}
	if !kept {
		return nil
	}
	if err := m.takeLock(ctx); err != nil {
		return err
	}
	cred, err := m.store.load()
	if err != nil {
		return m.errUnreadable(err)
	}
	if cred != nil {
		m.use(cred)
	}
	return nil
}

// takeLock locks the certificate and removes what an interrupted save left.
func (m *Membership) takeLock(ctx context.Context) error {
	lock, err := lockCertificate(ctx, m.store.dir, m.opts.lock, func(err error) error {
		return failure{
			cause: fmt.Sprintf("This worker cannot lock its certificate in its data directory %s: %s", m.cfg.Paths.DataDir, reason(err)),
			next:  "Make the data directory writable, so that one worker at a time uses the certificate.",
		}
	})
	if err != nil {
		return err
	}
	m.lock = lock
	m.store.removeLeftovers()
	return nil
}

// errUnreadable is the failure of a kept credential that cannot be read.
// The worker stops rather than call without it.
func (m *Membership) errUnreadable(err error) error {
	return failure{
		cause: fmt.Sprintf("This worker cannot read the certificate it keeps in its data directory %s: %s", m.cfg.Paths.DataDir, reason(err)),
		next:  "Let this user read the data directory, or start the worker with a new data directory and a new invite.",
	}
}

// reason is err without the path of the file it is about.
func reason(err error) string {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// use makes the worker's client present cred.
func (m *Membership) use(cred *credential) {
	m.cred = cred
	m.config = m.config.WithWorkerCertificate(cred.cert, cred.ca)
}

// StartTransport returns the transport the worker starts with and what
// selected it, as "<transport> (from <source>)".
func (m *Membership) StartTransport() (string, error) {
	return m.config.StartTransport()
}

// Connect returns the worker's client of its coordinators. A worker with
// worker.invite that holds no worker certificate enrolls for one first: it
// tries each coordinator in worker.coordinators, and retries while none is
// reachable. It fails, with the message of Spec 108 §Messages, when a
// coordinator refuses the invite or no reachable coordinator has the
// invite's CA.
func (m *Membership) Connect(ctx context.Context, workerID string) (coordinator.Client, error) {
	if m.invite != nil && m.cred == nil {
		if err := m.enroll(ctx, workerID); err != nil {
			return nil, err
		}
	}
	return newCoordinatorClient(ctx, m.cfg, m.config)
}

func (m *Membership) enroll(ctx context.Context, workerID string) error {
	key, csr, err := newWorkerKey()
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	resp, err := m.enrollAny(ctx, &coordinatorv1.EnrollRequest{
		Csr:              csr,
		WorkerId:         workerID,
		Host:             host,
		KeepsCertificate: m.writable,
	})
	if err != nil {
		return err
	}
	cred, err := enrolledCredential(key, resp, *m.invite)
	if err != nil {
		return err
	}
	if m.writable {
		if err := m.store.save(cred); err != nil {
			return fmt.Errorf("keep the worker certificate: %s", reason(err))
		}
	}
	m.use(cred)
	logger.Info(ctx, "Worker enrolled", slog.String("certificate-id", cred.cert.Leaf.Subject.CommonName))
	return nil
}

// enrollAny sends req to the coordinators, in rounds, until one enrolls the
// worker or a refusal ends the attempt. Between rounds it logs the last
// failure.
func (m *Membership) enrollAny(ctx context.Context, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error) {
	retrier := backoff.NewRetrier(m.opts.retryPolicy)
	for {
		resp, last, err := m.enrollRound(ctx, req)
		if resp != nil || err != nil {
			return resp, err
		}
		interval, _ := retrier.Next(last)
		logger.Warn(ctx, last.logLine(), tag.Interval(interval))
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// The ranks of the failures to retry after. Of a round's failures, the
// worker reports the one of the highest rank, the last among equals: a
// coordinator's answer says more than a coordinator without built-in TLS,
// which says more than one it cannot reach.
const (
	rankUnreachable = iota + 1
	rankNoBuiltinTLS
	rankAnswered
)

// enrollRound sends req to each coordinator once. It returns the response
// of the first that enrolls the worker, or the error that ends the attempt,
// or else the failure to retry after.
func (m *Membership) enrollRound(ctx context.Context, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, failure, error) {
	var last failure
	lastRank := 0
	note := func(f failure, rank int) {
		if rank >= lastRank {
			last, lastRank = f, rank
		}
	}
	var otherDagu string
	for _, address := range m.cfg.Worker.Coordinators {
		resp, err := m.opts.enroll(ctx, m.config, address, req)
		switch {
		case err == nil:
			return resp, failure{}, nil
		case ctx.Err() != nil:
			return nil, failure{}, ctx.Err()
		case errors.Is(err, coordinator.ErrInviteCAMismatch):
			if otherDagu == "" {
				otherDagu = address
			}
			continue
		case errors.Is(err, coordinator.ErrInvalidEnrollReply):
			logger.Error(ctx, "Enrollment reply refused", tag.Addr(address), tag.Error(err))
			return nil, failure{}, errInvalidReply(address)
		case errors.Is(err, coordinator.ErrOwnCertificates):
			note(errOwnCertificates(address), rankNoBuiltinTLS)
		case errors.Is(err, coordinator.ErrNoBuiltinTLS):
			note(errNoBuiltinTLS(address), rankNoBuiltinTLS)
		case status.Code(err) == codes.PermissionDenied:
			return nil, failure{}, errInviteRefused
		case status.Code(err) == codes.FailedPrecondition && !req.KeepsCertificate:
			// The invite is single use, and the coordinator kept it.
			return nil, failure{}, fmt.Errorf("a single-use invite needs a writable data directory: use a reusable invite, or make %s writable", m.cfg.Paths.DataDir)
		case answered(err):
			note(errAnswered(address, status.Convert(err).Message()), rankAnswered)
		default:
			note(errCannotReach(address), rankUnreachable)
		}
		logger.Debug(ctx, "Enrollment failed", tag.Addr(address), tag.Error(err))
	}
	if otherDagu != "" {
		return nil, failure{}, errOtherDagu(otherDagu)
	}
	return nil, last, nil
}

// Close releases the certificate lock.
func (m *Membership) Close() error {
	if m.lock == nil {
		return nil
	}
	lock := m.lock
	m.lock = nil
	return lock.Unlock()
}

// checkCACertFile fails when peer.ca_cert_file is set to a CA other than
// the one invite names.
func checkCACertFile(path string, invite coordinator.Invite) error {
	if path == "" {
		return nil
	}
	ca, err := readCertificateFile(path)
	if err != nil {
		return fmt.Errorf("peer.ca_cert_file %s: %w", path, err)
	}
	if !invite.Names(ca) {
		return errors.New("peer.ca_cert_file is not the CA that worker.invite names: remove peer.ca_cert_file or use an invite from this CA")
	}
	return nil
}

// enrollRetryPolicy paces enrollment while no coordinator is reachable, as
// the worker paces its heartbeats.
func enrollRetryPolicy() backoff.RetryPolicy {
	policy := backoff.NewExponentialBackoffPolicy(time.Second)
	policy.BackoffFactor = 1.5
	policy.MaxInterval = 15 * time.Second
	policy.MaxRetries = 0
	return backoff.WithJitter(policy, backoff.Jitter)
}

func portOf(address string) string {
	if _, port, err := net.SplitHostPort(address); err == nil {
		return port
	}
	return address
}
