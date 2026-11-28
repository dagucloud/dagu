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
	"sync"
	"syscall"
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

// statusMessage returns the message of the gRPC status that err carries.
func statusMessage(err error) string {
	if s, ok := errors.AsType[interface {
		error
		GRPCStatus() *status.Status
	}](err); ok {
		return s.GRPCStatus().Message()
	}
	return err.Error()
}

func errOtherDaguJoined(address string) failure {
	return failure{
		cause: address + " belongs to a different Dagu than the one this worker joined",
		next:  `Get a new command from "Add worker" to join it.`,
	}
}

// removedFailure is the failure of a removed worker, which errors.Is
// matches with ErrRemoved.
type removedFailure struct {
	failure
}

func (removedFailure) Is(target error) bool {
	return target == ErrRemoved
}

// errRemoved is the failure of a worker whose certificate a coordinator
// refused as revoked. In Kubernetes the pod restarts the worker, which
// exits again, until its Deployment is scaled down.
func errRemoved(kubernetes bool) removedFailure {
	f := failure{
		cause: "This worker was removed from the coordinator",
		next:  `Get a new command from "Add worker" to add it again.`,
	}
	if kubernetes {
		f.next = `Scale its Deployment down to stop it, or get a new command from "Add worker" to add it again.`
	}
	return removedFailure{f}
}

// terminationLogPath is where Kubernetes reads a container's termination
// message, unless the pod sets another terminationMessagePath. The chart
// keeps the default.
const terminationLogPath = "/dev/termination-log"

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
	// revoked is closed once a coordinator refused the certificate of the
	// worker's client as revoked, after the refusal is recorded.
	revoked     chan struct{}
	revokedOnce sync.Once
}

// joinOptions are what a Membership waits, enrolls, and retries with.
type joinOptions struct {
	lock        lockTiming
	enroll      func(context.Context, *coordinator.Config, string, *coordinatorv1.EnrollRequest) (*coordinatorv1.EnrollResponse, error)
	confirm     func(context.Context, *coordinator.Config, string) error
	retryPolicy backoff.RetryPolicy
	// environment is where the worker runs, and terminationLog the file
	// Kubernetes reads the termination message from.
	environment    func() coordinatorv1.HostEnvironment
	terminationLog string
}

// OpenMembership prepares how the worker calls its coordinators, by Spec 108
// §Worker Start. A worker that uses a kept worker certificate, or enrolls
// for one that it keeps, locks it first, and fails when another live worker
// keeps it for 30 seconds or the lock cannot be taken. Every other worker
// behaves as before Spec 108. localRegistry is the service registry in the
// worker's data directory.
func OpenMembership(ctx context.Context, cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (*Membership, error) {
	return openMembership(ctx, cfg, localRegistry, joinOptions{
		lock:           defaultLockTiming,
		enroll:         coordinator.Enroll,
		confirm:        coordinator.ConfirmWorkerCertificate,
		retryPolicy:    enrollRetryPolicy(),
		environment:    coordinator.ProcessEnvironment,
		terminationLog: terminationLogPath,
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
		cfg:     cfg,
		opts:    opts,
		store:   newCredentialStore(cfg.Paths.DataDir),
		config:  clientConfigOf(cfg, localRegistry),
		revoked: make(chan struct{}),
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
		return errUnreadable(err)
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
		return errUnreadable(err)
	}
	return m.start(ctx, cred)
}

// keptUse is what a worker does at start with the credential it keeps, by
// Spec 108 §Worker Start.
type keptUse int

const (
	// keptUnused leaves the credential to a new enrollment with
	// worker.invite, or, without one, the peer token.
	keptUnused keptUse = iota
	// keptConnect calls the coordinators with the kept certificate.
	keptConnect
	// keptRemoved exits, since a coordinator refused the certificate as
	// revoked.
	keptRemoved
)

// keptUseOf returns what a worker with worker.invite invite, nil without
// one, does with cred, nil when it keeps none.
func keptUseOf(cred *credential, invite *coordinator.Invite) keptUse {
	switch {
	case cred == nil:
		return keptUnused
	case invite == nil:
	case !invite.Names(cred.ca):
		// Another CA's certificate gives way to a new enrollment.
		return keptUnused
	case cred.removed && cred.inviteDigest != invite.Digest():
		// A different invite brings a removed worker back with a new key.
		return keptUnused
	}
	// A certificate from the invite's CA makes a restart keep it, so a
	// single-use invite is not used up.
	if cred.removed {
		return keptRemoved
	}
	return keptConnect
}

// start applies the kept credential cred to the worker's start.
func (m *Membership) start(ctx context.Context, cred *credential) error {
	switch keptUseOf(cred, m.invite) {
	case keptConnect:
		m.use(cred)
	case keptRemoved:
		m.cred = cred
		return m.removedFailure(ctx)
	case keptUnused:
		// A new certificate that cannot be kept would leave the old one for
		// a restart without the invite to reuse.
		if cred != nil && !m.writable {
			return failure{
				cause: "This worker cannot replace the certificate it enrolled for with one from the invite's CA",
				next:  fmt.Sprintf("Make %s writable, then start the worker again.", m.cfg.Paths.DataDir),
			}
		}
	}
	return nil
}

// openWithoutInvite uses the worker certificate in the data directory, if
// any, unless peer.insecure: true or own TLS files leave it unused.
func (m *Membership) openWithoutInvite(ctx context.Context) error {
	peer := m.cfg.Core.Peer
	if !usesKeptCertificate(peer) {
		if peer.Insecure && !peer.InsecureUnset {
			if kept, _ := m.store.kept(); kept {
				logger.Warn(ctx, "Worker certificate unused: peer.insecure is true, so this worker uses h2c and the peer token; remove peer.insecure: true to use it")
			}
		}
		return nil
	}
	kept, err := m.store.kept()
	if err != nil {
		return errUnreadable(err)
	}
	if !kept {
		return nil
	}
	if err := m.takeLock(ctx); err != nil {
		return err
	}
	cred, err := m.store.load()
	if err != nil {
		return errUnreadable(err)
	}
	return m.start(ctx, cred)
}

// takeLock locks the certificate and removes what an interrupted save left.
func (m *Membership) takeLock(ctx context.Context) error {
	lock, err := lockCertificate(ctx, m.store.dir, m.opts.lock, m.errCannotLock)
	if err != nil {
		return err
	}
	m.lock = lock
	m.store.removeLeftovers()
	return nil
}

// errCannotLock is the failure of a certificate lock that cannot be taken.
func (m *Membership) errCannotLock(err error) error {
	return failure{
		cause: "This worker cannot lock its certificate: " + reason(err),
		next:  fmt.Sprintf("Make %s writable, so that one worker at a time uses the certificate.", m.cfg.Paths.DataDir),
	}
}

// usesKeptCertificate reports whether a worker without worker.invite uses
// the worker certificate it keeps: not with peer.insecure: true, nor with
// its own TLS files.
func usesKeptCertificate(peer config.Peer) bool {
	insecure := peer.Insecure && !peer.InsecureUnset
	ownTLS := !peer.Insecure && (peer.CertFile != "" || peer.KeyFile != "" || peer.ClientCaFile != "" || peer.SkipTLSVerify)
	return !insecure && !ownTLS
}

// errUnreadable is the failure of a kept credential that cannot be read.
// The worker stops rather than call without it.
func errUnreadable(err error) error {
	return failure{
		cause: "This worker cannot read the certificate it keeps: " + reason(err),
		next:  "Let this user read the data directory, or start the worker with a new data directory and a new invite.",
	}
}

// reason is err without the paths of the files it is about: the system
// error alone when there is one.
func reason(err error) string {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno.Error()
	}
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err.Error()
	}
	if linkErr, ok := errors.AsType[*os.LinkError](err); ok {
		return linkErr.Err.Error()
	}
	return err.Error()
}

// use makes the worker's client present cred.
func (m *Membership) use(cred *credential) {
	m.cred = cred
	m.config = m.config.WithWorkerCertificate(cred.cert, cred.ca, nil)
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
// invite's CA. A worker that holds a worker certificate first asks the
// coordinators whether they accept it. It fails when one refuses it as
// revoked, unless worker.invite is another invite, with which it enrolls
// again, and when no reachable coordinator's CA signed it.
func (m *Membership) Connect(ctx context.Context, workerID string) (coordinator.Client, error) {
	if m.cred != nil {
		if err := m.confirm(ctx); err != nil {
			return nil, err
		}
	}
	if m.invite != nil && m.cred == nil {
		if err := m.enroll(ctx, workerID); err != nil {
			return nil, err
		}
	}
	clientConfig := m.config
	if m.cred != nil {
		// Only the client the worker runs with reports a removal, not
		// the confirmation above.
		clientConfig = m.config.WithWorkerCertificate(m.cred.cert, m.cred.ca, func() { m.revoke(ctx) })
	}
	client, err := newCoordinatorClient(ctx, m.cfg, clientConfig)
	if err != nil {
		return nil, err
	}
	// The worker that runs with the client stops on its removal.
	return &memberClient{Client: client, revoked: m.revoked, removed: m.removed}, nil
}

// revoke handles a refusal of the worker's certificate as revoked once: it
// records the refusal and writes the termination message before it closes
// revoked, so a worker that a signal or a kill ends right after it still
// exits at its next start.
func (m *Membership) revoke(ctx context.Context) {
	m.revokedOnce.Do(func() {
		m.recordRemoval(ctx)
		_ = m.removedFailure(ctx)
		close(m.revoked)
	})
}

// removed returns the failure a worker exits with once a coordinator refused
// its certificate as revoked, with the message of Spec 108 §Messages, after
// revoke has handled the refusal.
func (m *Membership) removed(ctx context.Context) error {
	m.revoke(ctx)
	return errRemoved(m.kubernetes())
}

// kubernetes reports whether the worker runs in Kubernetes.
func (m *Membership) kubernetes() bool {
	return m.opts.environment() == coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
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
			return failure{
				cause: "This worker enrolled but cannot keep its certificate: " + reason(err),
				next:  fmt.Sprintf(`Make %s writable. A single-use invite is now used up: get a new command from "Add worker".`, m.cfg.Paths.DataDir),
			}
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

// How a round of enrollment ends, by what the coordinators gave:
//
//	enrolled                        enrolled, at once
//	invite refused, reply refused,  stop, at once
//	  single use while read-only
//	answer of this Dagu             retry, reporting the answer, also
//	                                  beside another Dagu
//	another Dagu                    stop, unless an answer came
//	no built-in TLS, own TLS        retry, reporting it
//	unreachable                     retry, reporting it
//
// An answer is a status from a coordinator of the invite's CA, such as one
// that asks to try again. The ranks order the failures to retry after: the
// worker reports the one of the highest rank, the last among equals.
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
		case errors.Is(err, coordinator.ErrCoordinatorAnswered) || answered(err):
			note(errAnswered(address, statusMessage(err)), rankAnswered)
		default:
			note(errCannotReach(address), rankUnreachable)
		}
		logger.Debug(ctx, "Enrollment failed", tag.Addr(address), tag.Error(err))
	}
	// Another Dagu at one address ends the attempt only when no coordinator
	// answered: an answer says the worker reached its own.
	if otherDagu != "" && lastRank < rankAnswered {
		return nil, failure{}, errOtherDagu(otherDagu)
	}
	return nil, last, nil
}

// confirm asks each coordinator whether it accepts the kept certificate,
// before the worker starts, until one answers. While none answers, the
// worker starts, and its own calls retry.
func (m *Membership) confirm(ctx context.Context) error {
	var otherDagu string
	for _, address := range m.cfg.Worker.Coordinators {
		err := m.opts.confirm(ctx, m.config, address)
		switch {
		case err == nil:
			return nil
		case coordinator.IsWorkerRemoved(err):
			// Before the context: a refusal that arrives with a signal is
			// still recorded.
			return m.removedAtStart(ctx)
		case ctx.Err() != nil:
			return ctx.Err()
		case answered(err):
			// The coordinator accepted the handshake with the certificate
			// and did not refuse it, so it is of this Dagu.
			logger.Debug(ctx, "Worker certificate confirmed with an error", tag.Addr(address), tag.Error(err))
			return nil
		case errors.Is(err, coordinator.ErrOtherDagu):
			if otherDagu == "" {
				otherDagu = address
			}
		}
		logger.Debug(ctx, "Worker certificate not confirmed", tag.Addr(address), tag.Error(err))
	}
	switch {
	case otherDagu == "":
		return nil
	case m.invite != nil:
		// The kept certificate is from the invite's CA, which that
		// coordinator does not have.
		return errOtherDagu(otherDagu)
	}
	return errOtherDaguJoined(otherDagu)
}

// removedAtStart handles a kept certificate that a coordinator refused as
// revoked before the worker started. Another invite than the one it enrolled
// with enrolls the worker again.
func (m *Membership) removedAtStart(ctx context.Context) error {
	m.recordRemoval(ctx)
	if keptUseOf(m.cred, m.invite) != keptUnused {
		return m.removedFailure(ctx)
	}
	logger.Info(ctx, "This worker was removed from the coordinator; it enrolls again with worker.invite")
	m.cred = nil
	m.config = m.config.WithInvite(*m.invite)
	return nil
}

// recordRemoval marks the worker's certificate as refused as revoked, and
// keeps the mark with the certificate when the worker keeps one.
func (m *Membership) recordRemoval(ctx context.Context) {
	if m.cred == nil || m.cred.removed {
		return
	}
	removed := *m.cred
	removed.removed = true
	m.cred = &removed
	if m.lock == nil {
		return
	}
	if err := m.store.save(&removed); err != nil {
		logger.Debug(ctx, "Failed to record the removal of the worker certificate", tag.Error(err))
	}
}

// removedFailure is the failure of a removed worker. In Kubernetes, it is
// also the container's termination message, which kubectl describe pod
// shows.
func (m *Membership) removedFailure(ctx context.Context) error {
	kubernetes := m.kubernetes()
	f := errRemoved(kubernetes)
	if kubernetes {
		writeTerminationMessage(ctx, m.opts.terminationLog, f.logLine())
	}
	return f
}

// writeTerminationMessage replaces the content of the termination log at
// path with message. Kubernetes creates the file in each container, so a
// missing one is left missing.
func writeTerminationMessage(ctx context.Context, path, message string) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0) //nolint:gosec // The termination log path, not user input.
	if err == nil {
		_, err = file.WriteString(message)
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		logger.Debug(ctx, "Failed to write the termination message", tag.File(path), tag.Error(err))
	}
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
