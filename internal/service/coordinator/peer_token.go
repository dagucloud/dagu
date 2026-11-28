// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	appconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// peerTokenMetadataKey carries the peer token on coordinator calls.
	peerTokenMetadataKey = "dagu-peer-token" //nolint:gosec // A metadata key name, not a credential.
	// peerTokenRereadInterval bounds how long a client keeps a token read from
	// the token file, so it picks up a token the coordinator regenerated.
	peerTokenRereadInterval = 30 * time.Second
	// peerTokenRetryInterval bounds how often a client without a token reads
	// the file again, such as one that dialed before the coordinator wrote it.
	peerTokenRetryInterval = time.Second
	// peerTokenWarnInterval bounds how often the coordinator logs that it
	// accepted a call without a valid peer token.
	peerTokenWarnInterval = 10 * time.Minute
	// grpcHealthMethodPrefix names the health service, which probes call
	// without credentials.
	grpcHealthMethodPrefix = "/grpc.health.v1.Health/"
)

// peerTokenOnlyMethods take the peer token whatever coordinator.require_token
// says, and refuse worker certificates, since they serve administration
// rather than workers.
var peerTokenOnlyMethods = map[string]bool{
	coordinatorv1.CoordinatorService_GetJoinReadiness_FullMethodName: true,
	coordinatorv1.CoordinatorService_CreateInvite_FullMethodName:     true,
	coordinatorv1.CoordinatorService_ListInvites_FullMethodName:      true,
	coordinatorv1.CoordinatorService_DeleteInvite_FullMethodName:     true,
}

// LoadOrCreatePeerToken returns the peer token stored in path, generating and
// storing one first when the file does not exist. Coordinators that share the
// file agree on one token.
func LoadOrCreatePeerToken(path string) (string, error) {
	token, err := readPeerToken(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return token, err
	}

	token, err = newPeerToken()
	if err != nil {
		return "", err
	}
	if err := fileutil.MkdirAll(filepath.Dir(path), peerDirMode); err != nil {
		return "", fmt.Errorf("create peer token directory: %w", err)
	}
	err = fileutil.WriteFileAtomicExclusive(path, []byte(token+"\n"), peerFileMode)
	if errors.Is(err, fs.ErrExist) {
		return readPeerToken(path)
	}
	if err != nil {
		return "", fmt.Errorf("write peer token: %w", err)
	}
	return token, nil
}

// newPeerToken returns a random token of 64 lowercase hex characters.
func newPeerToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate peer token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func readPeerToken(path string) (string, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("peer token file %s is empty", path)
	}
	if err := appconfig.ValidatePeerToken(token); err != nil {
		return "", fmt.Errorf("peer token file %s %w", path, err)
	}
	return token, nil
}

// peerTokenCredentials attaches the peer token to every call: the configured
// token, or the content of the token file when this process can read it.
type peerTokenCredentials struct {
	configured string
	file       string

	mu     sync.Mutex
	token  string
	readAt time.Time
}

func newPeerTokenCredentials(configured, file string) *peerTokenCredentials {
	return &peerTokenCredentials{configured: configured, file: file}
}

func (c *peerTokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	token := c.current()
	if token == "" {
		return nil, nil
	}
	return map[string]string{peerTokenMetadataKey: token}, nil
}

// RequireTransportSecurity returns false because peers connect over plaintext
// h2c unless TLS is configured.
func (*peerTokenCredentials) RequireTransportSecurity() bool {
	return false
}

func (c *peerTokenCredentials) current() string {
	if c.configured != "" {
		return c.configured
	}
	if c.file == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	interval := peerTokenRereadInterval
	if c.token == "" {
		interval = peerTokenRetryInterval
	}
	if !c.readAt.IsZero() && time.Since(c.readAt) < interval {
		return c.token
	}
	// A failed read keeps the last token, so a passing file error does not
	// leave calls without one.
	if token, err := readPeerToken(c.file); err == nil {
		c.token = token
	}
	c.readAt = time.Now()
	return c.token
}

// PeerTokenAuth checks the peer token on coordinator calls.
//
// A call with the coordinator's token passes. A call with another token fails
// when the token was configured or required, since only a generated token can
// differ between coordinators that do not share a data directory. A call
// without a token fails only when the token is required; otherwise it passes
// and the coordinator logs a warning. Administration calls, such as
// GetJoinReadiness, always require the token. Enroll needs none, since its
// invite admits it.
type PeerTokenAuth struct {
	token      string
	configured bool
	require    bool
	// workerCA issues the worker certificates that count as the peer
	// token, or nil when none do.
	workerCA *x509.Certificate

	warnMu   sync.Mutex
	lastWarn time.Time
}

// NewPeerTokenAuth returns a check for token. configured reports whether the
// token came from peer.token, and require whether calls must carry it.
func NewPeerTokenAuth(token string, configured, require bool) *PeerTokenAuth {
	return &PeerTokenAuth{token: token, configured: configured, require: require}
}

// AcceptWorkerCertificates makes a call over TLS with a client certificate
// that ca issued count as a call with the peer token, also when the token is
// required. Administration calls refuse such a call with PermissionDenied.
// It must be called before the server serves.
func (a *PeerTokenAuth) AcceptWorkerCertificates(ca *PeerCA) {
	a.workerCA = ca.cert
}

// UnaryInterceptor checks the peer token on unary calls.
func (a *PeerTokenAuth) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := a.authorize(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor checks the peer token on streaming calls.
func (a *PeerTokenAuth) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := a.authorize(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func (a *PeerTokenAuth) authorize(ctx context.Context, method string) error {
	if strings.HasPrefix(method, grpcHealthMethodPrefix) || method == coordinatorv1.CoordinatorService_Enroll_FullMethodName {
		return nil
	}
	if workerCertificate(ctx, a.workerCA) != nil {
		if peerTokenOnlyMethods[method] {
			return status.Error(codes.PermissionDenied, "this call needs the peer token; a worker certificate cannot make it")
		}
		return nil
	}
	var got string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(peerTokenMetadataKey); len(values) > 0 {
			got = values[0]
		}
	}
	if got != "" && a.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1 {
		return nil
	}
	require := a.require || peerTokenOnlyMethods[method]
	switch {
	case got != "" && (a.configured || require):
		return status.Error(codes.Unauthenticated, "the peer token does not match the coordinator's peer token")
	case got == "" && require:
		return status.Error(codes.Unauthenticated,
			"the coordinator requires a peer token: set peer.token, or let this process read the coordinator's data directory")
	}
	a.warn(ctx, method, got != "")
	return nil
}

// workerCertificate returns the client certificate of a call over TLS when
// it chains to ca, or nil.
func workerCertificate(ctx context.Context, ca *x509.Certificate) *x509.Certificate {
	if ca == nil {
		return nil
	}
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil
	}
	for _, chain := range info.State.VerifiedChains {
		if len(chain) > 1 && chain[len(chain)-1].Equal(ca) {
			return chain[0]
		}
	}
	return nil
}

func (a *PeerTokenAuth) warn(ctx context.Context, method string, mismatched bool) {
	a.warnMu.Lock()
	if !a.lastWarn.IsZero() && time.Since(a.lastWarn) < peerTokenWarnInterval {
		a.warnMu.Unlock()
		return
	}
	a.lastWarn = time.Now()
	a.warnMu.Unlock()

	remote := "unknown"
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		remote = p.Addr.String()
	}
	message := "Coordinator accepted a call without a peer token"
	if mismatched {
		message = "Coordinator accepted a call with another coordinator's peer token"
	}
	logger.Warn(ctx, message,
		slog.String("method", method),
		slog.String("peer", remote),
		slog.String("hint", "set coordinator.require_token: true once every worker sends the peer token"),
	)
}
