// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// ErrOtherDagu is the error of a coordinator on built-in TLS whose CA did not
// sign the worker's certificate: one of another Dagu than the one the worker
// joined.
var ErrOtherDagu = errors.New("the coordinator's CA did not sign the worker certificate")

// IsWorkerRemoved reports whether err is a coordinator's refusal of a worker
// certificate as revoked, also when an error wraps it.
func IsWorkerRemoved(err error) bool {
	// The status itself, not one that FromError builds around the message
	// of an error that wraps it.
	var withStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &withStatus) {
		return false
	}
	st := withStatus.GRPCStatus()
	return st.Code() == codes.Unauthenticated && st.Message() == removedWorkerMessage
}

// ConfirmWorkerCertificate calls the coordinator at address once with the
// worker certificate of config, a client from WithWorkerCertificate. It
// returns nil when the coordinator accepts the certificate, ErrOtherDagu
// when the coordinator's CA did not sign it, and otherwise the error of the
// call, which IsWorkerRemoved reports when the certificate was revoked.
func ConfirmWorkerCertificate(ctx context.Context, config *Config, address string) error {
	if config.workerCert == nil {
		return errors.New("the confirmation needs a client with a worker certificate")
	}
	cli := New(nil, config).(*clientImpl)
	c, err := cli.createClient(address)
	if err != nil {
		return err
	}
	defer func() { _ = c.conn.Close() }()

	callCtx, cancel := context.WithTimeout(ctx, config.DialTimeout)
	defer cancel()
	// The worker list answers any worker certificate the coordinator
	// accepts, and changes nothing.
	_, err = c.client.GetWorkers(callCtx, &coordinatorv1.GetWorkersRequest{})
	if err != nil && status.Code(err) == codes.Unavailable && foreignCoordinatorFormat(c.link.lastHandshakeError()) == otherDaguFormat {
		return ErrOtherDagu
	}
	return err
}
