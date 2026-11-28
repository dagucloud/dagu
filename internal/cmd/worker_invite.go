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
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// workerJoinCommandFormat is the command that joins a worker host with
	// an invite, from the coordinator address and the invite.
	workerJoinCommandFormat = "dagu worker --worker.coordinators=%s --worker.invite=%s"
	// allInterfacesHost is the coordinator.host that accepts workers from
	// other hosts.
	allInterfacesHost = "0.0.0.0"
	// maxExpirySeconds is the longest expiry a coordinator accepts.
	maxExpirySeconds = math.MaxInt64 / int64(time.Second)
	// shellSafeChars are the characters a POSIX shell reads as they are.
	shellSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:-/=@%+,"
)

var (
	errInvitePeerSettings  = errors.New("this host's peer settings cannot be used: run dagu worker --check here to see why")
	errNoInviteCoordinator = errors.New("no coordinator found: set worker.coordinators to the coordinators' addresses, or run this on a host that shares their data directory")
)

// refusalCodes are the codes of a coordinator's refusal, whose message
// names the cause and the change without any file location.
var refusalCodes = map[codes.Code]bool{
	codes.InvalidArgument:    true,
	codes.FailedPrecondition: true,
	codes.Unauthenticated:    true,
	codes.PermissionDenied:   true,
}

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
	// expiresInSeconds is zero for the default expiry of the invite's kind.
	expiresInSeconds int64
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

The command calls the coordinators with the peer token, so it runs on any host
whose processes have the peer token. It calls the coordinators a worker on
this host would: those of worker.coordinators when set, else those in the
service registry of its data directory. The invite appears only in this
output.

While a coordinator accepts connections from its own machine only, or cannot
create invites, the command prints the change to make to standard error,
creates no invite, and exits with code 1.`,
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
	client, err := newInviteClient(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Cleanup(ctx) }()
	coordinators, ok := client.(inviteCoordinator)
	if !ok {
		return errors.New("the coordinator client cannot create invites")
	}

	reports, err := coordinators.GetJoinReadiness(ctx)
	if err != nil {
		return errNoInviteCoordinator
	}
	notReady := 0
	for _, report := range reports {
		if !printJoinReadiness(ctx.Command.ErrOrStderr(), report) {
			notReady++
		}
	}
	if notReady > 0 {
		return fmt.Errorf("no invite created: %d of %d coordinators cannot accept workers from other hosts", notReady, len(reports))
	}

	resp, err := coordinators.CreateInvite(ctx, &coordinatorv1.CreateInviteRequest{
		Reusable:         opts.reusable,
		ExpiresInSeconds: opts.expiresInSeconds,
		Address:          opts.address,
	})
	if err != nil {
		return fmt.Errorf("failed to create the invite: %s", callFailure(err))
	}
	printInvite(ctx.Command.OutOrStdout(), opts.address, resp)
	return nil
}

// newInviteClient returns a client for the coordinators a worker on this
// host calls: those of worker.coordinators when set, else those in the
// service registry of its data directory.
func newInviteClient(ctx *Context) (coordinator.Client, error) {
	clientConfig, err := worker.CoordinatorClientConfig(ctx.Config, ctx.serverStores.ServiceRegistry)
	if err != nil {
		// The error may name a credential file, so it is not repeated.
		return nil, errInvitePeerSettings
	}
	registry := ctx.serverStores.ServiceRegistry
	if len(ctx.Config.Worker.Coordinators) > 0 {
		if registry, err = worker.CoordinatorRegistry(ctx.Config); err != nil {
			return nil, err
		}
	}
	return coordinator.New(registry, clientConfig), nil
}

func parseInviteOptions(cmd *cobra.Command) (inviteOptions, error) {
	var opts inviteOptions
	address, err := cmd.Flags().GetString(inviteAddressFlag.name)
	if err != nil {
		return opts, err
	}
	if opts.address, err = normalizeHostPort(address); err != nil {
		return opts, fmt.Errorf("--%s must be host:port, such as 192.168.1.20:50055 or [2001:db8::1]:50055", inviteAddressFlag.name)
	}
	if opts.reusable, err = cmd.Flags().GetBool(inviteReusableFlag.name); err != nil {
		return opts, err
	}
	if !cmd.Flags().Changed(inviteExpiresFlag.name) {
		return opts, nil
	}
	expires, err := cmd.Flags().GetString(inviteExpiresFlag.name)
	if err != nil {
		return opts, err
	}
	d, err := time.ParseDuration(expires)
	if err != nil || d <= 0 {
		return opts, fmt.Errorf("--%s must be a positive duration, such as 24h", inviteExpiresFlag.name)
	}
	if opts.expiresInSeconds = expirySeconds(d); opts.expiresInSeconds > maxExpirySeconds {
		return opts, fmt.Errorf("--%s must be at most %s", inviteExpiresFlag.name, time.Duration(maxExpirySeconds)*time.Second)
	}
	return opts, nil
}

// normalizeHostPort returns addr as host:port, with an IPv6 host in
// brackets, or an error when addr is not a host and a TCP port.
func normalizeHostPort(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if n, err := strconv.Atoi(port); host == "" || err != nil || n <= 0 || n > math.MaxUint16 {
		return "", errors.New("not host:port")
	}
	return net.JoinHostPort(host, port), nil
}

// expirySeconds returns d in whole seconds, rounded up so the invite lasts at
// least d.
func expirySeconds(d time.Duration) int64 {
	seconds := int64(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	return seconds
}

// callFailure returns the cause of a failed coordinator call and the change
// to make. It repeats a coordinator's refusal, and otherwise only the kind
// of failure, since an error raised on this host may name a credential
// file.
func callFailure(err error) string {
	var carrier interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &carrier) {
		return "the call failed on this host: run dagu worker --check here to see why"
	}
	st := carrier.GRPCStatus()
	if refusalCodes[st.Code()] {
		return st.Message()
	}
	if st.Code() == codes.Unimplemented {
		return "it predates worker invites: upgrade it to create invites"
	}
	return "it could not be reached or did not answer: run dagu worker --check on this host to see why"
}

// printJoinReadiness reports whether the coordinator of report accepts
// workers from other hosts. When it does not, it prints why and the change
// to make, as the Add worker dialog does.
func printJoinReadiness(w io.Writer, report coordinator.JoinReadiness) bool {
	addr := net.JoinHostPort(report.Coordinator.Host, strconv.Itoa(report.Coordinator.Port))
	if report.Err != nil {
		_, _ = fmt.Fprintf(w, "Coordinator %s did not report whether it accepts workers: %s\n", addr, callFailure(report.Err))
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
	_, _ = fmt.Fprintf(w, "Run on the worker host:\n  "+workerJoinCommandFormat+"\n", shellQuote(address), resp.Invite)
}

// shellQuote returns s as one word of a POSIX shell command: as it is when
// the shell reads it unchanged, else in single quotes.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, shellSafeChars) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
