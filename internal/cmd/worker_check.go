// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/spf13/cobra"
)

// errNoWorkerCoordinators is the failure of a worker without
// worker.coordinators.
var errNoWorkerCoordinators = errors.New("worker.coordinators is required")

// isWorkerCheck reports whether cmd is dagu worker --check.
func isWorkerCheck(cmd *cobra.Command) bool {
	if commandFamilyName(cmd) != "worker" {
		return false
	}
	check, _ := cmd.Flags().GetBool(workerCheckFlag.name)
	return check
}

// runWorkerCheck prints the worker's transport, then what each coordinator
// it would use showed. It fails when a coordinator does not pass. Without
// worker.coordinators it checks the coordinators in the service registry,
// and fails since the worker does not start without them.
func runWorkerCheck(ctx *Context) error {
	clientConfig := coordinator.ConfigFromPeer(ctx.Config.Core.Peer)
	if err := clientConfig.Validate(); err != nil {
		return fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	members, err := workerCheckMembers(ctx)
	if err != nil {
		return err
	}

	out := ctx.Command.OutOrStdout()
	line, ignored := peerTransport(ctx.Config)
	for _, setting := range ignored {
		_, _ = fmt.Fprintln(out, ignoredSettingWarning(setting))
	}
	_, _ = fmt.Fprintln(out, line)

	failed := 0
	for _, member := range members {
		result := coordinator.Check(ctx, clientConfig, member)
		printWorkerCheck(out, result)
		if !result.Passed() {
			failed++
		}
	}
	switch {
	case len(ctx.Config.Worker.Coordinators) == 0:
		return fmt.Errorf("%w: set it to the address of a coordinator above that passed", errNoWorkerCoordinators)
	case failed > 0:
		return fmt.Errorf("%d of %d coordinators did not pass the check", failed, len(members))
	}
	_, _ = fmt.Fprintln(out, "Every coordinator passed the check")
	return nil
}

// workerCheckMembers returns the coordinators in worker.coordinators, or
// the live ones in the service registry without it.
func workerCheckMembers(ctx *Context) ([]serviceregistry.HostInfo, error) {
	var registry serviceregistry.ServiceRegistry
	if len(ctx.Config.Worker.Coordinators) > 0 {
		static, err := coordinator.NewStaticRegistry(ctx.Config.Worker.Coordinators)
		if err != nil {
			return nil, fmt.Errorf("failed to create static registry: %w", err)
		}
		registry = static
	} else {
		registry = ctx.serverStores.ServiceRegistry
	}
	members, err := registry.GetServiceMembers(ctx, serviceregistry.ServiceNameCoordinator)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, errNoWorkerCoordinators
	}
	return members, nil
}

// printWorkerCheck prints what one coordinator showed, with the messages of
// Spec 108.
func printWorkerCheck(w io.Writer, result coordinator.CheckResult) {
	_, _ = fmt.Fprintf(w, "Coordinator %s\n", result.Address)
	if result.DialErr != nil {
		_, port, _ := net.SplitHostPort(result.Address)
		_, _ = fmt.Fprintf(w, "  Reachable: no (%v)\n", result.DialErr)
		_, _ = fmt.Fprintf(w, "  Cannot reach coordinator at %s\n", result.Address)
		_, _ = fmt.Fprintf(w, "  Check that the coordinator accepts workers from other hosts, that port %s is open on the way, and that the address is right.\n", port)
		return
	}
	_, _ = fmt.Fprintln(w, "  Reachable: yes")

	switch {
	case result.TLS == nil:
		_, _ = fmt.Fprintln(w, "  Handshake: none, the worker uses h2c")
	case result.TLS.Err != nil:
		_, _ = fmt.Fprintf(w, "  Handshake: failed, %v\n", result.TLS.Err)
		_, _ = fmt.Fprintf(w, "    Address dialed: %s\n", result.Address)
		printServerCertificate(w, result.TLS.Certificate)
		return
	default:
		_, _ = fmt.Fprintln(w, "  Handshake: TLS")
		printServerCertificate(w, result.TLS.Certificate)
	}

	_, _ = fmt.Fprintf(w, "  Credential: %s\n", credentialText(result.Credential))
	if result.AnswerErr != nil {
		_, _ = fmt.Fprintf(w, "  Coordinator answered: %v\n", result.AnswerErr)
		return
	}
	_, _ = fmt.Fprintln(w, "  Coordinator answered: serving, and it accepted the call")
}

func printServerCertificate(w io.Writer, cert *coordinator.ServerCertificate) {
	if cert == nil {
		return
	}
	names := "no names"
	if len(cert.Names) > 0 {
		names = strings.Join(cert.Names, ", ")
	}
	_, _ = fmt.Fprintf(w, "    Certificate covers: %s\n", names)
	ca := cert.CA
	if cert.SelfSigned {
		ca += " (the certificate signs itself)"
	}
	_, _ = fmt.Fprintf(w, "    Certificate chains to: %s\n", ca)
}

// credentialText says what the worker presents to the coordinator.
func credentialText(credential coordinator.Credential) string {
	var token string
	switch credential.PeerToken {
	case coordinator.PeerTokenConfigured:
		token = "peer token from peer.token"
	case coordinator.PeerTokenDataDir:
		token = "peer token from the coordinator's data directory"
	default:
		token = "no peer token"
	}
	if credential.CertFile == "" {
		return token
	}
	return fmt.Sprintf("client certificate %s and %s", credential.CertFile, token)
}
