// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/spf13/cobra"
)

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
	clientConfig, err := worker.CoordinatorClientConfig(ctx.Config, ctx.serverStores.ServiceRegistry)
	if err != nil {
		return err
	}
	out := ctx.Command.OutOrStdout()
	line, ignored := peerTransport(ctx.Config, clientConfig.StartTransport)
	for _, setting := range ignored {
		_, _ = fmt.Fprintln(out, ignoredSettingWarning(setting))
	}
	_, _ = fmt.Fprintln(out, line)

	members, err := workerCheckMembers(ctx)
	if err != nil {
		return err
	}

	failed := 0
	for _, member := range members {
		result := coordinator.Check(ctx, clientConfig, member)
		printWorkerCheck(out, result, ctx.Config.Worker.Invite != "")
		if !result.Passed() {
			failed++
		}
	}
	switch {
	case len(ctx.Config.Worker.Coordinators) == 0 && failed == len(members):
		return fmt.Errorf("%w, and no coordinator in the service registry passed the check", worker.ErrNoCoordinators)
	case len(ctx.Config.Worker.Coordinators) == 0:
		return fmt.Errorf("%w: set it to the address of a coordinator above that passed", worker.ErrNoCoordinators)
	case failed > 0:
		return fmt.Errorf("%d of %d coordinators did not pass the check", failed, len(members))
	}
	_, _ = fmt.Fprintln(out, "Every coordinator passed the check")
	return nil
}

// workerCheckMembers returns the coordinators the worker calls, or the live
// ones in the service registry without worker.coordinators.
func workerCheckMembers(ctx *Context) ([]serviceregistry.HostInfo, error) {
	var registry serviceregistry.ServiceRegistry
	static, err := worker.CoordinatorRegistry(ctx.Config)
	switch {
	case errors.Is(err, worker.ErrNoCoordinators):
		registry = ctx.serverStores.ServiceRegistry
	case err != nil:
		return nil, err
	default:
		registry = static
	}
	members, err := registry.GetServiceMembers(ctx, serviceregistry.ServiceNameCoordinator)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, worker.ErrNoCoordinators
	}
	return members, nil
}

// printWorkerCheck prints what one coordinator showed, with the messages of
// Spec 108. invite reports a worker with worker.invite.
func printWorkerCheck(w io.Writer, result coordinator.CheckResult, invite bool) {
	_, _ = fmt.Fprintf(w, "Coordinator %s\n", result.Address)
	if result.TransportErr != nil {
		_, _ = fmt.Fprintf(w, "  Transport: none (%v)\n", result.TransportErr)
		return
	}
	_, _ = fmt.Fprintf(w, "  Transport: %s\n", result.Transport)
	if fallback := result.Fallback; fallback != nil {
		_, _ = fmt.Fprintf(w, "    h2c until %s, after built-in TLS failed: %s\n",
			fallback.Until.Format(time.RFC3339), fallback.Err)
	}
	through := ""
	if result.Proxy != "" {
		through = ", through proxy " + result.Proxy
	}
	if result.DialErr != nil {
		_, port, _ := net.SplitHostPort(result.Address)
		_, _ = fmt.Fprintf(w, "  Reachable: no%s (%v)\n", through, result.DialErr)
		_, _ = fmt.Fprintf(w, "  Cannot reach coordinator at %s\n", result.Address)
		_, _ = fmt.Fprintf(w, "  Check that the coordinator accepts workers from other hosts, that port %s is open on the way, and that the address is right.\n", port)
		return
	}
	_, _ = fmt.Fprintf(w, "  Reachable: yes%s\n", through)

	switch {
	case result.TLS == nil:
		_, _ = fmt.Fprintln(w, "  Handshake: none, the worker uses h2c")
	case result.TLS.Err != nil:
		_, _ = fmt.Fprintf(w, "  Handshake: failed, %v\n", result.TLS.Err)
		_, _ = fmt.Fprintf(w, "    Address dialed: %s\n", result.Address)
		printServerCertificate(w, result.TLS.Certificate)
		if result.TLS.Fallback == "" {
			return
		}
		_, _ = fmt.Fprintf(w, "    The worker falls back to h2c (from %s) for 10 minutes\n", result.TLS.Fallback)
	case !result.TLS.Verified:
		_, _ = fmt.Fprintln(w, "  Handshake: TLS, certificate not verified (peer.skip_tls_verify: true)")
		printServerCertificate(w, result.TLS.Certificate)
	default:
		_, _ = fmt.Fprintln(w, "  Handshake: TLS")
		printServerCertificate(w, result.TLS.Certificate)
	}

	// A built-in TLS handshake of an invite worker passes only with the CA
	// the invite pins: the invite's pin, or the CA of the certificate kept
	// from it.
	if invite && result.TLS != nil && result.TLS.Err == nil {
		_, _ = fmt.Fprintln(w, "  Invite pin: matches the coordinator's CA")
	}
	credential := result.Credential
	_, _ = fmt.Fprintf(w, "  Credential: %s\n", credentialText(credential))
	switch {
	case coordinator.IsWorkerRemoved(result.AnswerErr):
		_, _ = fmt.Fprintln(w, "  Coordinator answered: This worker was removed from the coordinator")
		_, _ = fmt.Fprintln(w, `  Get a new command from "Add worker" to add it again.`)
	case result.AnswerErr != nil:
		_, _ = fmt.Fprintf(w, "  Coordinator answered: %v\n", result.AnswerErr)
	case credential.Invite:
		_, _ = fmt.Fprintln(w, "  Coordinator answered: serving; the worker enrolls with its invite when it starts")
	case credential.CertificateID != "":
		_, _ = fmt.Fprintln(w, "  Coordinator answered: serving, and it accepted the worker certificate")
	default:
		_, _ = fmt.Fprintln(w, "  Coordinator answered: serving, and it accepted the call")
	}
}

// printServerCertificate prints the names a certificate covers, marked as
// DNS names or IP addresses, and the CA it chains to.
func printServerCertificate(w io.Writer, cert *coordinator.ServerCertificate) {
	if cert == nil {
		return
	}
	names := make([]string, 0, len(cert.DNSNames)+len(cert.IPAddresses))
	for _, name := range cert.DNSNames {
		names = append(names, "DNS:"+name)
	}
	for _, ip := range cert.IPAddresses {
		names = append(names, "IP:"+ip)
	}
	covers := "no names"
	if len(names) > 0 {
		covers = strings.Join(names, ", ")
	}
	_, _ = fmt.Fprintf(w, "    Certificate covers: %s\n", covers)
	ca := cert.CA
	if cert.SelfSigned {
		ca += " (the certificate signs itself)"
	}
	_, _ = fmt.Fprintf(w, "    Certificate chains to: %s\n", ca)
}

// credentialText says what the worker presents to the coordinator.
func credentialText(credential coordinator.Credential) string {
	switch {
	case credential.CertificateID != "":
		return fmt.Sprintf("worker certificate %s, from invite %s; no peer token", credential.CertificateID, credential.InviteID)
	case credential.Invite:
		return "the invite of worker.invite, which only enrollment uses; no peer token"
	}
	var token string
	switch credential.PeerToken {
	case coordinator.PeerTokenConfigured:
		token = "peer token from peer.token"
	case coordinator.PeerTokenDataDir:
		token = "peer token from the coordinator's data directory"
	case coordinator.PeerTokenNone:
		token = "no peer token"
	}
	switch {
	case credential.CertFile == "":
		return token
	case !credential.CertSent:
		return fmt.Sprintf("%s; client certificate %s not sent, as the coordinator did not ask for one from its CA", token, credential.CertFile)
	default:
		return fmt.Sprintf("client certificate %s and %s", credential.CertFile, token)
	}
}
