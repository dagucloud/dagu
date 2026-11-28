// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
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

func errNoBuiltinTLS(address string) failure {
	return failure{
		cause: "Coordinator at " + address + " does not serve built-in TLS, so this worker cannot join it",
		next:  `Upgrade the coordinator, and check that its start line reads "Coordinator transport: built-in TLS".`,
	}
}

// Membership is how a worker calls its coordinators: with the peer token, as
// before Spec 108, or with the worker certificate it holds or obtains with
// worker.invite. Close releases what it holds.
type Membership struct {
	cfg          *config.Config
	clientConfig *coordinator.Config
	store        credentialStore
	// writable reports that the worker keeps its credential in its data
	// directory. Otherwise it holds it in memory and takes no lock.
	writable bool
	// cred is the credential the worker uses, nil until it enrolls or on
	// the peer token.
	cred *credential
	lock *certificateLock

	enroll      func(context.Context, *coordinator.Config, string, *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error)
	retryPolicy backoff.RetryPolicy
}

// OpenMembership prepares how the worker calls its coordinators, by Spec 108
// §Worker Start. A worker with worker.invite, or with a worker certificate
// in its data directory, takes the lock on its certificate first, and fails
// when another live worker keeps it for 30 seconds. Every other worker
// behaves as before Spec 108. localRegistry is the service registry in the
// worker's data directory.
func OpenMembership(ctx context.Context, cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (*Membership, error) {
	if len(cfg.Worker.Coordinators) == 0 {
		return nil, ErrNoCoordinators
	}
	m := &Membership{
		cfg:          cfg,
		clientConfig: clientConfigOf(cfg, localRegistry),
		store:        newCredentialStore(cfg.Paths.DataDir),
		enroll:       coordinator.Enroll,
		retryPolicy:  enrollRetryPolicy(),
	}
	if err := m.open(ctx); err != nil {
		_ = m.Close()
		return nil, err
	}
	if err := m.clientConfig.Validate(); err != nil {
		_ = m.Close()
		return nil, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	return m, nil
}

func (m *Membership) open(ctx context.Context) error {
	invite := m.cfg.Worker.Invite
	if invite == "" {
		return m.openWithoutInvite(ctx)
	}
	pin, ok := coordinator.InvitePin(invite)
	if !ok {
		return errInviteRefused
	}
	if err := checkCACertFile(m.cfg.Core.Peer.CACertFile, pin); err != nil {
		return err
	}
	if m.cfg.Core.Peer.Token != "" {
		logger.Info(ctx, "peer.token is not sent: this worker calls the coordinator with the worker certificate it enrolls for with worker.invite")
	}
	m.clientConfig.Invite = invite
	m.writable = m.store.writable()
	if !m.writable {
		// The worker enrolls again at each start, with a key in memory.
		return nil
	}
	if err := m.takeLock(ctx); err != nil {
		return err
	}
	cred, err := m.store.load()
	if err != nil {
		logger.Warn(ctx, "Enrolling again, since the kept worker certificate cannot be read", tag.Error(err))
		return nil
	}
	// A certificate from the invite's CA makes a restart keep it, so a
	// single-use invite is not used up. Another CA's certificate gives way
	// to a new enrollment.
	if cred != nil && cred.pin() == pin {
		m.use(cred)
	}
	return nil
}

// openWithoutInvite uses the worker certificate in the data directory, if
// any, unless peer.insecure: true or own TLS files leave it unused.
func (m *Membership) openWithoutInvite(ctx context.Context) error {
	if !m.store.exists() {
		return nil
	}
	peer := m.cfg.Core.Peer
	switch {
	case peer.Insecure && !peer.InsecureUnset:
		logger.Warn(ctx, "Worker certificate unused: peer.insecure is true, so this worker uses h2c and the peer token; remove peer.insecure: true to use it")
		return nil
	case !peer.Insecure && (peer.CertFile != "" || peer.KeyFile != "" || peer.ClientCaFile != "" || peer.SkipTLSVerify):
		return nil
	}
	m.writable = m.store.writable()
	if m.writable {
		if err := m.takeLock(ctx); err != nil {
			return err
		}
	}
	cred, err := m.store.load()
	if err != nil {
		return err
	}
	if cred != nil {
		m.use(cred)
	}
	return nil
}

func (m *Membership) takeLock(ctx context.Context) error {
	lock, err := lockCertificate(ctx, m.store.dir, defaultLockTiming)
	if err != nil {
		return err
	}
	m.lock = lock
	return nil
}

// use makes the worker's client present cred.
func (m *Membership) use(cred *credential) {
	m.cred = cred
	m.clientConfig.Invite = ""
	m.clientConfig.WorkerCertificate = &cred.cert
	m.clientConfig.WorkerCA = cred.ca
}

// ClientConfig returns the configuration the worker's coordinator client
// dials with. Enroll changes it.
func (m *Membership) ClientConfig() *coordinator.Config {
	return m.clientConfig
}

// Enroll obtains a worker certificate with worker.invite when the worker
// holds none. It tries each coordinator in worker.coordinators, and retries
// while none is reachable. It fails, with the message of Spec 108
// §Messages, when a coordinator refuses the invite or no reachable
// coordinator has the invite's CA.
func (m *Membership) Enroll(ctx context.Context, workerID string) error {
	invite := m.cfg.Worker.Invite
	if m.cred != nil || invite == "" {
		return nil
	}
	key, csr, err := newWorkerKey()
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	resp, err := m.enrollAny(ctx, &coordinatorv1.EnrollRequest{
		Invite:           invite,
		Csr:              csr,
		WorkerId:         workerID,
		Host:             host,
		KeepsCertificate: m.writable,
	})
	if err != nil {
		return err
	}
	cred, err := enrolledCredential(key, resp.Certificate, resp.CaCertificate, invite)
	if err != nil {
		return err
	}
	if m.writable {
		if err := m.store.save(cred); err != nil {
			return err
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
	retrier := backoff.NewRetrier(m.retryPolicy)
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

// enrollRound sends req to each coordinator once. It returns the response
// of the first that enrolls the worker, or the error that ends the attempt,
// or else the failure to retry after.
func (m *Membership) enrollRound(ctx context.Context, req *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, failure, error) {
	var last failure
	var otherDagu string
	for _, address := range m.cfg.Worker.Coordinators {
		resp, err := m.enroll(ctx, m.clientConfig, address, req)
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
		case errors.Is(err, coordinator.ErrNoBuiltinTLS):
			last = errNoBuiltinTLS(address)
		case status.Code(err) == codes.PermissionDenied:
			return nil, failure{}, errInviteRefused
		case status.Code(err) == codes.FailedPrecondition && !req.KeepsCertificate:
			// The invite is single use, and the coordinator kept it.
			return nil, failure{}, fmt.Errorf("a single-use invite needs a writable data directory: use a reusable invite, or make %s writable", m.cfg.Paths.DataDir)
		default:
			last = errCannotReach(address)
		}
		logger.Debug(ctx, "Enrollment failed", tag.Addr(address), tag.Error(err))
	}
	if otherDagu != "" {
		return nil, failure{}, errOtherDagu(otherDagu)
	}
	return nil, last, nil
}

// Lost is closed when another worker took the certificate lock over, as it
// does from a worker that stopped refreshing it for 30 seconds. It is nil
// for a worker without the lock.
func (m *Membership) Lost() <-chan struct{} {
	if m.lock == nil {
		return nil
	}
	return m.lock.lost
}

// ErrLockLost is the error of a worker whose certificate lock another
// worker took over.
var ErrLockLost = error(errAnotherWorker)

// Close releases the certificate lock.
func (m *Membership) Close() error {
	if m.lock == nil {
		return nil
	}
	lock := m.lock
	m.lock = nil
	return lock.release()
}

// checkCACertFile fails when peer.ca_cert_file is set to a CA other than
// the one with pin, the pin of worker.invite.
func checkCACertFile(path, pin string) error {
	if path == "" {
		return nil
	}
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("peer.ca_cert_file: %w", err)
	}
	ca, err := parseCertificatePEM(string(data))
	if err != nil {
		return fmt.Errorf("peer.ca_cert_file %s: %w", path, err)
	}
	if coordinator.CAPin(ca) != pin {
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
