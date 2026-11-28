// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// workerJoinCommandFormat is the command that joins a worker host with
	// an invite, from the coordinator address and the invite.
	workerJoinCommandFormat = "dagu worker --worker.coordinators=%s --worker.invite=%s"
	// allInterfacesHost is the coordinator.host that accepts workers from
	// other hosts.
	allInterfacesHost = "0.0.0.0"
)

var (
	inviteAddressFlag = commandLineFlag{
		name:     "address",
		usage:    "Address workers use to reach the coordinator (format: host:port)",
		required: true,
	}
	inviteReusableFlag = commandLineFlag{
		name:   "reusable",
		usage:  "Create an invite that enrolls any number of workers (default: single use)",
		isBool: true,
	}
	inviteExpiresFlag = commandLineFlag{
		name:  "expires",
		usage: "How long the invite lasts, such as 24h (default: 24h for single use, none for reusable)",
	}
)

var inviteCommandFlags = []commandLineFlag{inviteAddressFlag, inviteReusableFlag, inviteExpiresFlag}

// inviteCoordinator is what dagu worker invite calls on the coordinators.
type inviteCoordinator interface {
	coordinator.JoinReadinessClient
	coordinator.InviteClient
}

// inviteOptions are the flags of dagu worker invite.
type inviteOptions struct {
	address  string
	reusable bool
	// expires is zero for the default expiry of the invite's kind.
	expires time.Duration
}

func workerInviteCommand() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "invite --address <host:port> [--reusable] [--expires <duration>]",
			Short: "Create an invite and print the command that joins a worker host",
			Long: `Create a worker invite and print it with the command to run on the worker
host. The worker enrolls with the invite and connects over built-in TLS.

--address is the host:port the worker dials to reach the coordinator. It goes
into the printed command. A single-use invite enrolls one worker and expires
after 24 hours unless --expires is given. A reusable invite enrolls any number
of workers and lasts until deleted unless --expires is given.

The command calls the coordinator with the peer token, so it runs on any host
whose processes have the peer token. The invite appears only in this output.

While a coordinator accepts connections from its own machine only, or cannot
create invites, the command prints the change to make, creates no invite, and
exits with code 1.`,
			Example: `  dagu worker invite --address 192.168.1.20:50055
  dagu worker invite --address 192.168.1.20:50055 --reusable --expires 168h`,
			Args: cobra.NoArgs,
		}, inviteCommandFlags, runWorkerInvite,
	)
}

func runWorkerInvite(ctx *Context, _ []string) error {
	opts, err := parseInviteOptions(ctx.Command)
	if err != nil {
		return err
	}
	client, err := ctx.coordinatorClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Cleanup(ctx) }()
	coordinators, ok := client.(inviteCoordinator)
	if !ok {
		return errors.New("the coordinator client cannot create invites")
	}

	out := ctx.Command.OutOrStdout()
	reports, err := coordinators.GetJoinReadiness(ctx)
	if err != nil {
		return fmt.Errorf("failed to read join readiness: %w", err)
	}
	notReady := 0
	for _, report := range reports {
		if !printJoinReadiness(out, report) {
			notReady++
		}
	}
	if notReady > 0 {
		return fmt.Errorf("no invite created: %d of %d coordinators cannot accept workers from other hosts", notReady, len(reports))
	}

	resp, err := coordinators.CreateInvite(ctx, &coordinatorv1.CreateInviteRequest{
		Reusable:         opts.reusable,
		ExpiresInSeconds: expirySeconds(opts.expires),
		Address:          opts.address,
	})
	if err != nil {
		return fmt.Errorf("failed to create the invite: %s", statusMessage(err))
	}
	printInvite(out, opts.address, resp)
	return nil
}

func parseInviteOptions(cmd *cobra.Command) (inviteOptions, error) {
	var opts inviteOptions
	var err error
	if opts.address, err = cmd.Flags().GetString(inviteAddressFlag.name); err != nil {
		return opts, err
	}
	if !isHostPort(opts.address) {
		return opts, fmt.Errorf("--%s must be host:port, such as 192.168.1.20:50055", inviteAddressFlag.name)
	}
	if opts.reusable, err = cmd.Flags().GetBool(inviteReusableFlag.name); err != nil {
		return opts, err
	}
	expires, err := cmd.Flags().GetString(inviteExpiresFlag.name)
	if err != nil || expires == "" {
		return opts, err
	}
	if opts.expires, err = time.ParseDuration(expires); err != nil || opts.expires <= 0 {
		return opts, fmt.Errorf("--%s must be a positive duration, such as 24h", inviteExpiresFlag.name)
	}
	return opts, nil
}

// isHostPort reports whether addr is a host and a TCP port, the address the
// coordinator accepts for an invite.
func isHostPort(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= math.MaxUint16
}

// expirySeconds returns d in whole seconds, rounded up so the invite lasts at
// least d, or 0 for the default expiry.
func expirySeconds(d time.Duration) int64 {
	return int64((d + time.Second - 1) / time.Second)
}

// printJoinReadiness reports whether the coordinator of report accepts
// workers from other hosts. When it does not, it prints why and the change
// to make, as the Add worker dialog does.
func printJoinReadiness(w io.Writer, report coordinator.JoinReadiness) bool {
	addr := net.JoinHostPort(report.Coordinator.Host, strconv.Itoa(report.Coordinator.Port))
	if report.Err != nil {
		if status.Code(report.Err) == codes.Unimplemented {
			_, _ = fmt.Fprintf(w, "Coordinator %s predates worker invites: upgrade it to create invites\n", addr)
		} else {
			_, _ = fmt.Fprintf(w, "Coordinator %s did not report whether it accepts workers: %s\n", addr, statusMessage(report.Err))
		}
		return false
	}
	r := report.Report
	if r.Ready {
		return true
	}
	_, _ = fmt.Fprintf(w, "Coordinator %s cannot accept workers from other hosts:\n", addr)
	if r.LoopbackOnly {
		_, _ = fmt.Fprintf(w, "  It listens on %s, so only its own machine can connect. %s\n", r.ListenHost, listenChange(r))
	}
	if !r.BuiltinTls {
		_, _ = fmt.Fprintf(w, "  Invites need the built-in CA: %s.\n", builtinTLSChange(r.BuiltinTlsReason))
	}
	_, _ = fmt.Fprintln(w, "  Each change needs a restart of the coordinator.")
	return false
}

// listenChange is the change that makes a coordinator listening on loopback
// only accept workers from other hosts, for how it was started.
func listenChange(r *coordinatorv1.GetJoinReadinessResponse) string {
	switch {
	case r.Environment == coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES:
		return fmt.Sprintf("Set coordinator.host to %s through the chart's values.", allInterfacesHost)
	case r.Environment == coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER:
		return fmt.Sprintf("Recreate its container with -p %d:%d -e DAGU_COORDINATOR_HOST=%s.", r.Port, r.Port, allInterfacesHost)
	case r.HostSource.GetKind() == coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV:
		return fmt.Sprintf("Set %s=%s.", r.HostSource.GetName(), allInterfacesHost)
	case r.HostSource.GetKind() == coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG:
		return fmt.Sprintf("Start it with %s=%s.", r.HostSource.GetName(), allInterfacesHost)
	default:
		return fmt.Sprintf("Add this to %s:\n    coordinator:\n      host: %s", r.ConfigFile, allInterfacesHost)
	}
}

// builtinTLSChange says why a coordinator has no built-in CA, in the words
// of its refusal of an invite call.
func builtinTLSChange(reason coordinatorv1.BuiltinTLSReason) string {
	if reason == coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES {
		return "this coordinator uses peer.cert_file and peer.key_file"
	}
	return "this coordinator could not create or read its CA; see its start warning"
}

// printInvite prints the invite of resp and the command that joins a worker
// host with it through address.
func printInvite(w io.Writer, address string, resp *coordinatorv1.CreateInviteResponse) {
	kind := "single use"
	if resp.Record.GetKind() == coordinatorv1.InviteKind_INVITE_KIND_REUSABLE {
		kind = "reusable"
	}
	expiry := "no expiry"
	if expiresAt := resp.Record.GetExpiresAt(); expiresAt > 0 {
		expiry = "expires " + time.Unix(expiresAt, 0).UTC().Format(time.RFC3339)
	}
	_, _ = fmt.Fprintf(w, "Invite (%s, %s):\n  %s\n", kind, expiry, resp.Invite)
	_, _ = fmt.Fprintf(w, "Run on the worker host:\n  "+workerJoinCommandFormat+"\n", address, resp.Invite)
}

// statusMessage returns the message of the gRPC status that err carries, or
// err itself when it carries none.
func statusMessage(err error) string {
	var carrier interface{ GRPCStatus() *status.Status }
	if errors.As(err, &carrier) {
		return carrier.GRPCStatus().Message()
	}
	return err.Error()
}
