// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// CheckResult is what a check of one coordinator address found.
type CheckResult struct {
	// Address is the address dialed, as host:port.
	Address string
	// Transport is the transport the client picks for the address and what
	// selected it, as "<transport> (from <source>)".
	Transport string
	// TransportErr is why the client has no transport for the address; nil
	// when it has one.
	TransportErr error
	// Fallback is the open h2c window that selects h2c for the address; nil
	// when none is open.
	Fallback *H2CFallback
	// Proxy is the address of the HTTPS proxy the client dials through;
	// empty when it dials directly.
	Proxy string
	// DialErr is why the address did not accept a connection; nil when it
	// did.
	DialErr error
	// TLS is the TLS handshake; nil over h2c and when the address did not
	// accept a connection.
	TLS *TLSHandshake
	// Credential is what the client presents to the coordinator.
	Credential Credential
	// AnswerErr is why the coordinator did not serve the client's call; nil
	// when it did. It is nil as well when the check stopped earlier. A
	// refusal of a revoked worker certificate reads as the removed worker's
	// message.
	AnswerErr error
}

// H2CFallback is an open h2c window: the client uses h2c for an address
// until Until, after built-in TLS failed there with Err.
type H2CFallback struct {
	Until time.Time
	Err   string
}

// TLSHandshake is what a TLS handshake with a coordinator showed.
type TLSHandshake struct {
	// Err says why the handshake failed; nil when it succeeded. It is
	// ErrInviteCAMismatch for a coordinator of another Dagu than the one the
	// worker's invite or certificate names.
	Err error
	// Fallback is the source of the h2c the client falls back to after the
	// failed handshake, such as "tls failed on the network path"; empty when
	// it does not fall back.
	Fallback string
	// Verified reports whether the client verified the server certificate.
	// It is false on a handshake that peer.skip_tls_verify let pass.
	Verified bool
	// Certificate describes the server certificate; nil when the server sent
	// none.
	Certificate *ServerCertificate
}

// ServerCertificate describes a server certificate.
type ServerCertificate struct {
	// DNSNames are the DNS names the certificate covers.
	DNSNames []string
	// IPAddresses are the IP addresses the certificate covers.
	IPAddresses []string
	// CA names the CA the certificate chains to.
	CA string
	// SelfSigned reports whether the certificate names itself as its CA.
	SelfSigned bool
}

// Credential is what a client presents to a coordinator.
type Credential struct {
	// CertFile is the client certificate configured for TLS; empty when
	// none.
	CertFile string
	// CertSent reports whether the client sent CertFile in the handshake. It
	// is false when the coordinator did not ask for a certificate from the
	// CA that signed it.
	CertSent bool
	// PeerToken is where the peer token comes from.
	PeerToken PeerTokenSource

	// certificateID and inviteID name the worker certificate the client
	// presents and the invite it enrolled with; empty without one.
	certificateID string
	inviteID      string
	// enrolls reports a worker that enrolls with worker.invite, so its
	// calls carry no credential before it enrolls.
	enrolls bool
}

// credentialText says what the client presents to the coordinator.
func credentialText(c Credential) string {
	switch {
	case c.certificateID != "":
		return fmt.Sprintf("worker certificate %s, from invite %s; no peer token", c.certificateID, c.inviteID)
	case c.enrolls:
		return "the invite of worker.invite, which only enrollment uses; no peer token"
	}
	var token string
	switch c.PeerToken {
	case PeerTokenConfigured:
		token = "peer token from peer.token"
	case PeerTokenDataDir:
		token = "the peer token that the coordinator generated"
	case PeerTokenNone:
		token = "no peer token"
	}
	switch {
	case c.CertFile == "":
		return token
	case !c.CertSent:
		return fmt.Sprintf("%s; client certificate %s not sent, as the coordinator did not ask for one from its CA", token, c.CertFile)
	default:
		return fmt.Sprintf("client certificate %s and %s", c.CertFile, token)
	}
}

// PeerTokenSource is where a client's peer token comes from.
type PeerTokenSource int

const (
	// PeerTokenNone means the client sends no peer token.
	PeerTokenNone PeerTokenSource = iota
	// PeerTokenConfigured means the token comes from peer.token.
	PeerTokenConfigured
	// PeerTokenDataDir means the token comes from the coordinator's data
	// directory.
	PeerTokenDataDir
)

const (
	// clientCertRefusalWait bounds the read after a handshake in which the
	// server asked for a client certificate.
	clientCertRefusalWait = time.Second
	// credentialedCallSkipped ends the answer of a coordinator that failed
	// the health check, after which the check makes no call with the
	// credential.
	credentialedCallSkipped = "so the call with the credential was not tried"
)

// errServesTLS is the answer of a coordinator that serves only TLS to a
// client on h2c.
var errServesTLS = errors.New("it serves only TLS on this port: set peer.insecure: false with a TLS setting")

// requireTLSError is the answer of a coordinator whose
// coordinator.require_tls refused the client's call over h2c from source. It
// reads as the message the client logs for the refusal.
type requireTLSError struct {
	source string
}

func (e requireTLSError) Error() string {
	return requireTLSClientMessage(e.source)
}

// Passed reports whether the client has a transport for the coordinator, the
// coordinator accepted a connection, the TLS handshake completed or the
// client falls back to h2c after it, and the coordinator served the call.
func (r CheckResult) Passed() bool {
	return r.TransportErr == nil && r.DialErr == nil &&
		(r.TLS == nil || r.TLS.Err == nil || r.TLS.Fallback != "") && r.AnswerErr == nil
}

// Describe returns the check's lines about the client's credential: what
// the client presents to the coordinator, and what the coordinator answered
// its calls, once the check reached them.
func (r CheckResult) Describe() (credential, answer string) {
	return credentialText(r.Credential), answerText(r)
}

// answerText says what the coordinator answered the client's calls.
func answerText(r CheckResult) string {
	switch {
	case r.AnswerErr != nil:
		return r.AnswerErr.Error()
	case r.Credential.enrolls:
		return "serving; the worker enrolls with its invite when it starts"
	case r.Credential.certificateID != "":
		return "serving, and it accepted the worker certificate"
	}
	return "serving, and it accepted the call"
}

// Check dials the coordinator that member names with config, as a worker
// does: it picks the transport as the worker's client does, and calls with
// the worker's credential. It never enrolls, registers no worker, takes no
// task, and opens or ends no h2c window. Over h2c, the coordinator lists the
// host among its h2c callers, as for any call that is not a worker's.
func Check(ctx context.Context, config *Config, member serviceregistry.HostInfo) CheckResult {
	result := CheckResult{
		Address:    coordinatorAddress(member),
		Credential: clientCredential(config),
	}
	connectCtx, cancel := context.WithTimeout(ctx, config.DialTimeout)
	defer cancel()

	plan, fallback, err := checkPlan(connectCtx, config, result.Address)
	if err != nil {
		result.TransportErr = err
		return result
	}
	result.Transport = plan.choice.String()
	result.Fallback = fallback

	conn, proxy, err := dialCoordinator(connectCtx, result.Address)
	result.Proxy = proxy
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		err = noAnswer(config)
	}
	if err != nil {
		result.DialErr = err
		return result
	}
	// h2cSource is the source of h2c, which the calls send as their reason;
	// empty on TLS.
	var h2cSource string
	if plan.h2c() && (config.Insecure || config.picksTransportPerConnection()) {
		h2cSource = plan.choice.source
	} else {
		result.TLS, result.Credential.CertSent = checkHandshake(connectCtx, config, plan, conn, member.Host)
		h2cSource = result.TLS.Fallback
	}
	_ = conn.Close()
	if result.TLS != nil && result.TLS.Err != nil && h2cSource == "" {
		return result
	}
	// The calls get their own time, whatever the handshake took.
	callCtx, cancelCall := context.WithTimeout(ctx, config.DialTimeout)
	defer cancelCall()
	result.AnswerErr = checkAnswer(callCtx, config, plan, member.Host, result.Address, h2cSource)
	return result
}

// checkPlan returns the transport the client picks for address, through the
// decision its connections make, and the open h2c window that picks h2c.
// It reads the h2c windows without opening or ending one.
func checkPlan(ctx context.Context, config *Config, address string) (connectionPlan, *H2CFallback, error) {
	if !config.picksTransportPerConnection() {
		choice, err := config.startTransport()
		return connectionPlan{choice: choice}, nil, err
	}
	transport := newClientTransport(config)
	plan, err := transport.plan(ctx, address)
	if err != nil || !plan.windowed {
		return plan, nil, err
	}
	window, ok := transport.windows.active(address, transport.now())
	if !ok {
		return plan, nil, nil
	}
	return plan, &H2CFallback{Until: window.Until, Err: window.Error}, nil
}

// checkAnswer asks the coordinator for its health and its worker list with
// the client's credential, over h2c from h2cSource or, when it is empty,
// over the TLS of plan. A worker that enrolls has no credential yet, so it
// gets the health check only.
func checkAnswer(ctx context.Context, config *Config, plan connectionPlan, host, address, h2cSource string) error {
	opts, err := getDialOptions(config)
	if err != nil {
		return err
	}
	h2c := h2cSource != ""
	switch {
	case !config.picksTransportPerConnection():
	case h2c:
		opts = append(opts, grpc.WithTransportCredentials(h2cCredentials{source: h2cSource}))
	default:
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(builtinTLSConfig(plan))))
	}
	conn, err := grpc.NewClient(address, opts...)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	health, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		if h2c && status.Code(err) == codes.Unavailable && servesTLS(ctx, address, host) {
			return errServesTLS
		}
		if status.Code(err) == codes.DeadlineExceeded {
			return fmt.Errorf("the coordinator did not answer the health check within %s, %s", config.DialTimeout, credentialedCallSkipped)
		}
		return fmt.Errorf("the health check failed, %w, %s", callError(config, err), credentialedCallSkipped)
	}
	if health.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("the coordinator is not serving (%s), %s", strings.ToLower(health.Status.String()), credentialedCallSkipped)
	}
	if plan.pin != "" {
		return nil
	}
	// The health service takes no credential; the worker list does.
	if _, err := coordinatorv1.NewCoordinatorServiceClient(conn).GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{}); err != nil {
		if isRequireTLSRefusal(err) {
			return requireTLSError{source: h2cSource}
		}
		if isWorkerRemoved(err) {
			return removedWorkerError{}
		}
		return fmt.Errorf("serving, but %w", callError(config, err))
	}
	return nil
}

// servesTLS reports whether address answers a TLS handshake. It sends host
// as the server name, as a TLS front end may need it.
func servesTLS(ctx context.Context, address, host string) bool {
	conn, _, err := dialCoordinator(ctx, address)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	probe := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Only the protocol matters here, not who the server is.
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
		NextProtos:         []string{"h2"},
	}
	err = tls.Client(conn, probe).HandshakeContext(ctx)
	return err == nil || isRemoteAlert(err)
}

// isRemoteAlert reports whether err is a TLS alert the server sent. Over
// TCP, crypto/tls reports one as a "remote error", not as tls.AlertError.
func isRemoteAlert(err error) bool {
	opErr, ok := errors.AsType[*net.OpError](err)
	return ok && opErr.Op == "remote error"
}

// callError returns the message of a gRPC call error without its code.
func callError(config *Config, err error) error {
	s, ok := status.FromError(err)
	if !ok {
		return err
	}
	if code := s.Code(); code == codes.Unauthenticated || code == codes.PermissionDenied {
		return fmt.Errorf("refused the call, %s", s.Message())
	}
	if s.Code() == codes.DeadlineExceeded {
		return noAnswer(config)
	}
	return errors.New(s.Message())
}

// noAnswer is the error of a coordinator that did not answer in time.
func noAnswer(config *Config) error {
	return fmt.Errorf("the coordinator did not answer within %s", config.DialTimeout)
}

// clientCredential returns what a client with config presents.
func clientCredential(config *Config) Credential {
	var credential Credential
	switch {
	case config.workerCert != nil:
		// An invite worker sends neither the peer token nor TLS files.
		if leaf := config.workerCert.Leaf; leaf != nil {
			credential.certificateID, credential.inviteID = leaf.Subject.CommonName, inviteIDOf(leaf)
		}
		return credential
	case config.invite != nil:
		credential.enrolls = true
		return credential
	}
	if !config.Insecure && config.CertFile != "" && config.KeyFile != "" {
		credential.CertFile = config.CertFile
	}
	switch {
	case config.Token != "":
		credential.PeerToken = PeerTokenConfigured
	case config.TokenFile != "":
		if _, err := readPeerToken(config.TokenFile); err == nil {
			credential.PeerToken = PeerTokenDataDir
		}
	}
	return credential
}
