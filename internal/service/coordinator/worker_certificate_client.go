// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// removedWorkerCheckAnswer and removedWorkerCheckAnswerKubernetes are what
// dagu worker --check prints for a worker certificate refused as revoked, in
// the words of Spec 108 §Messages.
const (
	removedWorkerCheckAnswer           = "This worker was removed from the coordinator\n  Get a new command from \"Add worker\" to add it again."
	removedWorkerCheckAnswerKubernetes = "This worker was removed from the coordinator\n  Scale its Deployment down to stop it, or get a new command from \"Add worker\" to add it again."
)

// removedWorkerError is the check's answer for a worker certificate refused
// as revoked, with the next step for where the worker runs.
type removedWorkerError struct{}

func (removedWorkerError) Error() string {
	return removedWorkerAnswer(ProcessEnvironment())
}

// removedWorkerAnswer is the check's answer for a worker certificate
// refused as revoked, for a worker in environment.
func removedWorkerAnswer(environment coordinatorv1.HostEnvironment) string {
	if environment == coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES {
		return removedWorkerCheckAnswerKubernetes
	}
	return removedWorkerCheckAnswer
}

// isWorkerRemoved reports whether err is a coordinator's refusal of a worker
// certificate as revoked, also when an error wraps it.
func isWorkerRemoved(err error) bool {
	// The status itself, not one that FromError builds around the message
	// of an error that wraps it.
	var withStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &withStatus) {
		return false
	}
	st := withStatus.GRPCStatus()
	return st.Code() == codes.Unauthenticated && st.Message() == removedWorkerMessage
}

// NewWorkerClient returns the client of the coordinators at addresses for a
// worker that holds the worker certificate of config, a client from
// WithWorkerCertificate. It first asks the coordinators, in order, whether
// they accept the certificate, until one answers. onRemoved is called each
// time a coordinator refuses the certificate as revoked, that confirmation
// included, on any call of the client.
//
// It fails with the refusal when a coordinator refuses the certificate as
// revoked, and with ErrInviteCAMismatch when every coordinator that answers
// belongs to another Dagu, with otherDagu naming the first of them. While no
// coordinator answers, it returns the client, whose calls retry.
func NewWorkerClient(ctx context.Context, addresses []string, config *Config, onRemoved func()) (client Client, otherDagu string, err error) {
	return newWorkerClient(ctx, addresses, config, onRemoved, confirmWorkerCertificate)
}

// newWorkerClient is NewWorkerClient with confirm in place of the
// confirmation of the certificate with one coordinator.
func newWorkerClient(ctx context.Context, addresses []string, config *Config, onRemoved func(),
	confirm func(context.Context, *Config, string) (bool, error)) (Client, string, error) {
	if config.workerCert == nil {
		return nil, "", errors.New("a worker client needs a worker certificate")
	}
	registry, err := NewStaticRegistry(addresses)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create static registry: %w", err)
	}
	clientConfig := *config
	clientConfig.onRemoved = onRemoved
	var otherDagu string
	// The registry's addresses, as the client dials them, in the order of
	// addresses.
	for _, member := range registry.coordinators {
		address := coordinatorAddress(member)
		authenticated, err := confirm(ctx, &clientConfig, address)
		switch {
		case err == nil:
			return New(registry, &clientConfig), "", nil
		case isWorkerRemoved(err):
			return nil, "", err
		case ctx.Err() != nil:
			return nil, "", ctx.Err()
		case authenticated:
			// The coordinator completed the handshake with the certificate
			// and did not refuse it, so it is of this Dagu.
			logger.Debug(ctx, "Worker certificate confirmed with an error", tag.Addr(address), tag.Error(err))
			return New(registry, &clientConfig), "", nil
		case errors.Is(err, ErrInviteCAMismatch):
			if otherDagu == "" {
				otherDagu = address
			}
		}
		logger.Debug(ctx, "Worker certificate not confirmed", tag.Addr(address), tag.Error(err))
	}
	if otherDagu != "" {
		return nil, otherDagu, ErrInviteCAMismatch
	}
	return New(registry, &clientConfig), "", nil
}

// confirmWorkerCertificate calls the coordinator at address once with the
// worker certificate of config. It reports whether the TLS handshake
// completed, and returns nil when the coordinator accepts the certificate,
// ErrInviteCAMismatch when its CA did not sign it, and otherwise the error of
// the call.
func confirmWorkerCertificate(ctx context.Context, config *Config, address string) (bool, error) {
	cli := New(nil, config).(*clientImpl)
	c, err := cli.createClient(address)
	if err != nil {
		return false, err
	}
	defer func() { _ = c.conn.Close() }()

	callCtx, cancel := context.WithTimeout(ctx, config.DialTimeout)
	defer cancel()
	// The worker list answers any worker certificate the coordinator
	// accepts, and changes nothing.
	var p peer.Peer
	_, err = c.client.GetWorkers(callCtx, &coordinatorv1.GetWorkersRequest{}, grpc.Peer(&p))
	_, authenticated := p.AuthInfo.(credentials.TLSInfo)
	if err != nil && !authenticated && foreignCoordinatorFormat(c.link.lastHandshakeError()) == otherDaguFormat {
		return false, ErrInviteCAMismatch
	}
	return authenticated, err
}
