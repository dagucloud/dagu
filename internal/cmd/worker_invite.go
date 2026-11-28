// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// workerJoinCommandFormat is the command that joins a worker host with
	// an invite, from the coordinator address and the invite.
	workerJoinCommandFormat = "dagu worker --worker.coordinators=%s --worker.invite=%s"
	// workerInviteCommandName is the command family name of dagu worker
	// invite.
	workerInviteCommandName = "worker invite"
	// workerCoordinatorsKey is the setting that names the coordinators to
	// call.
	workerCoordinatorsKey = "worker.coordinators"
	// coordinatorHostVariable is the variable that sets coordinator.host.
	coordinatorHostVariable = "DAGU_COORDINATOR_HOST"
	// allInterfacesHost is the coordinator.host that accepts workers from
	// other hosts.
	allInterfacesHost = "0.0.0.0"
	// maxExpirySeconds is the longest expiry a coordinator accepts.
	maxExpirySeconds = math.MaxInt64 / int64(time.Second)
	// maxHostnameLength and maxLabelLength bound a hostname and each of its
	// labels.
	maxHostnameLength = 253
	maxLabelLength    = 63
	// hostnameChars are the characters of a hostname label.
	hostnameChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	// shellSafeChars are the characters a POSIX shell reads as they are.
	shellSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:-/=@%+,"
)

var (
	errInvitePeerSettings  = errors.New("this host's peer settings cannot be used: run dagu worker --check here to see why")
	errInvalidCoordinators = errors.New("worker.coordinators holds no valid host:port: fix it, or unset it to use the coordinators in the service registry")
	errNoInviteCoordinator = errors.New("no coordinator found: set worker.coordinators to the coordinators' addresses, or run this on a host that shares their data directory")
)

// connectionCodes are the codes of a call that did not reach a coordinator
// or got no answer from it.
var connectionCodes = map[codes.Code]bool{
	codes.Unavailable:      true,
	codes.DeadlineExceeded: true,
	codes.Canceled:         true,
}

// coordinatorFailures are the messages a coordinator answers with for a
// failure on its side of an invite call.
var coordinatorFailures = map[string]bool{
	"failed to generate the invite": true,
	"failed to store the invite":    true,
	"failed to delete the invite":   true,
}

var (
	// containerID matches the short and the full ID of a container.
	containerID = regexp.MustCompile(`^[0-9a-f]{12}([0-9a-f]{52})?$`)
	// invitePattern matches text shaped like an invite: dagu1.<pin>.<secret>.
	invitePattern = regexp.MustCompile(`dagu1\.[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}`)
)

// refusalCodes are the codes of a coordinator's refusal, whose message
// names the cause and the change without any file location.
var refusalCodes = map[codes.Code]bool{
	codes.InvalidArgument:    true,
	codes.FailedPrecondition: true,
	codes.PermissionDenied:   true,
	// A refusal for TLS, or one for the peer token without a
	// peerTokenRefusals prefix.
	codes.Unauthenticated: true,
}

// peerTokenRefusals start the messages a coordinator refuses a missing or
// wrong peer token with, in this release and the next. Other refusals with
// the same code, such as the one for coordinator.require_tls, keep their
// own remedy.
var peerTokenRefusals = []string{
	"the coordinator requires a peer token",
	"the peer token does not match the coordinator's peer token",
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

// warningsOnly is a logger that drops debug and info messages.
type warningsOnly struct {
	logger.Logger
}

func (warningsOnly) Debug(string, ...slog.Attr) {}
func (warningsOnly) Info(string, ...slog.Attr)  {}
func (warningsOnly) Debugf(string, ...any)      {}
func (warningsOnly) Infof(string, ...any)       {}

func (l warningsOnly) With(attrs ...slog.Attr) logger.Logger {
	return warningsOnly{l.Logger.With(attrs...)}
}

func (l warningsOnly) WithGroup(name string) logger.Logger {
	return warningsOnly{l.Logger.WithGroup(name)}
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
whose processes have the peer token. It calls the coordinators of
worker.coordinators when set, else those in the service registry of its data
directory. The invite appears only in this output.

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
	// The command reports each outcome itself. The client's info lines,
	// such as the transport to each coordinator, would only sit next to
	// the invite the operator copies.
	ctx = ctx.WithContext(logger.WithLogger(ctx, warningsOnly{logger.FromContext(ctx)}))
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
		return fmt.Errorf("failed to create the invite: %s", callFailure("the coordinator", err))
	}
	if err := printInvite(ctx.Command.OutOrStdout(), opts.address, resp); err != nil {
		return discardUnprinted(ctx, coordinators, resp.Record.GetId())
	}
	return nil
}

// discardUnprinted deletes the invite id that could not be printed, since
// no one may have its secret and no command lists it. It fails either way,
// naming the invite only by its ID.
func discardUnprinted(ctx *Context, invites coordinator.InviteClient, id string) error {
	if err := invites.DeleteInvite(ctx, id); err != nil {
		return fmt.Errorf("failed to print invite %s, and failed to delete it: %s; delete it before it is used", id, callFailure("the coordinator", err))
	}
	return fmt.Errorf("failed to print invite %s, so it was deleted: run the command again", id)
}

// inviteRegistry returns the registry of the coordinators to call: those of
// worker.coordinators when set, else the service registry of the data
// directory. Tests replace it to change the coordinators during a call.
var inviteRegistry = func(ctx *Context) (serviceregistry.ServiceRegistry, error) {
	if len(ctx.Config.Worker.Coordinators) > 0 {
		return worker.CoordinatorRegistry(ctx.Config)
	}
	// The loader drops an address it cannot use, so a worker.coordinators
	// that is set and empty held none. The registry is not used in its
	// place, since the operator named the coordinators to call.
	if source := ctx.Config.Sources[workerCoordinatorsKey]; source.Kind != config.SourceKindDefault {
		return nil, errInvalidCoordinators
	}
	return ctx.serverStores.ServiceRegistry, nil
}

// newInviteClient returns a client for the coordinators of inviteRegistry,
// as listed when it is called.
func newInviteClient(ctx *Context) (coordinator.Client, error) {
	clientConfig, err := worker.CoordinatorClientConfig(ctx.Config, ctx.serverStores.ServiceRegistry)
	if err != nil {
		// The error may name a credential file, so it is not repeated.
		return nil, errInvitePeerSettings
	}
	registry, err := inviteRegistry(ctx)
	if err != nil {
		return nil, err
	}
	// One read of the registry, so join readiness and the create reach the
	// same coordinators.
	members, err := registry.GetServiceMembers(ctx, serviceregistry.ServiceNameCoordinator)
	if err != nil || len(members) == 0 {
		return nil, errNoInviteCoordinator
	}
	return coordinator.New(&snapshotRegistry{ServiceRegistry: registry, members: members}, clientConfig), nil
}

// snapshotRegistry lists the coordinators read once, so the create reaches
// only coordinators whose join readiness was read.
type snapshotRegistry struct {
	serviceregistry.ServiceRegistry
	members []serviceregistry.HostInfo
}

func (r *snapshotRegistry) GetServiceMembers(context.Context, serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	return slices.Clone(r.members), nil
}

func parseInviteOptions(cmd *cobra.Command) (inviteOptions, error) {
	var opts inviteOptions
	address, err := cmd.Flags().GetString(inviteAddressFlag.name)
	if err != nil {
		return opts, err
	}
	// The request carries the address as a protobuf string, which must be
	// valid UTF-8.
	if !utf8.ValidString(address) {
		return opts, fmt.Errorf("--%s must be valid UTF-8 text", inviteAddressFlag.name)
	}
	// Surrounding spaces are dropped, as the Add worker dialog drops them.
	// A comma would split the printed --worker.coordinators value.
	address = strings.TrimSpace(address)
	if strings.Contains(address, ",") {
		return opts, fmt.Errorf("--%s must be one host:port, without commas", inviteAddressFlag.name)
	}
	if invitePattern.MatchString(address) {
		return opts, fmt.Errorf("--%s holds an invite; the invite goes only into the printed command", inviteAddressFlag.name)
	}
	if !validInviteAddress(address) {
		return opts, fmt.Errorf("--%s must be host:port, such as 192.168.1.20:50055: a hostname, a dotted IPv4 address, "+
			"or an IPv6 address in brackets, and a port from 1 to 65535 without a sign or leading zero", inviteAddressFlag.name)
	}
	if problem := dialAddressProblem(address); problem != "" {
		return opts, fmt.Errorf("--%s %s", inviteAddressFlag.name, problem)
	}
	opts.address = address
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

// dialAddressProblem says why a worker on another host cannot dial addr, an
// address validInviteAddress accepts, or returns "" when it can. It is the
// rest of the address rule of Spec 108 §Add Worker, which the Add worker
// dialog and the MCP action apply too.
func dialAddressProblem(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsUnspecified():
			return "must name one address, not " + host
		case ip.IsLoopback():
			return "must not be a loopback address, which reaches only the worker's own machine"
		case ip.IsLinkLocalUnicast():
			return "must not be a link-local address, which another network does not route"
		case ip.IsMulticast(), ip.Equal(net.IPv4bcast):
			return "must be one machine's address, not a multicast or broadcast address"
		}
		return ""
	}
	switch {
	case strings.EqualFold(host, "localhost"):
		return "must not be localhost, which reaches only the worker's own machine"
	case containerID.MatchString(host):
		return "must not be a container ID; give the address of the machine that runs the container"
	}
	return ""
}

// validInviteAddress reports whether addr can be an invite's address, by the
// rule coordinators apply from the next release: host:port, where host is a
// hostname of dot-separated labels of 1 to 63 ASCII letters, digits,
// hyphens, and underscores, neither starting nor ending with a hyphen, at
// most 253 characters in all, with no trailing dot; an IPv4 address in
// dotted decimal; or an IPv6 address in brackets without a zone. The port
// is a decimal number from 1 to 65535 without a sign or leading zero.
func validInviteAddress(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !validInvitePort(port) {
		return false
	}
	if strings.HasPrefix(addr, "[") {
		ip, err := netip.ParseAddr(host)
		return err == nil && ip.Is6() && ip.Zone() == ""
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Is4()
	}
	return validHostname(host)
}

// validInvitePort reports whether port is a decimal TCP port from 1 to
// 65535, written without a sign or leading zero.
func validInvitePort(port string) bool {
	if port == "" || port[0] == '0' || strings.Trim(port, "0123456789") != "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n <= math.MaxUint16
}

// validHostname reports whether host is a hostname as validInviteAddress
// states.
func validHostname(host string) bool {
	if host == "" || len(host) > maxHostnameLength {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > maxLabelLength || label[0] == '-' || label[len(label)-1] == '-' ||
			strings.Trim(label, hostnameChars) != "" {
			return false
		}
	}
	return true
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

// callFailure returns the cause of a failed call to subject and the change
// to make. It repeats a coordinator's refusal and its own words for a
// failure on its side, and otherwise only the kind of failure, since an
// error raised on this host may name a credential file.
func callFailure(subject string, err error) string {
	var carrier interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &carrier) {
		return "the call failed on this host: run dagu worker --check here to see why"
	}
	st := carrier.GRPCStatus()
	code := st.Code()
	switch {
	case code == codes.Unauthenticated && slices.ContainsFunc(peerTokenRefusals, func(prefix string) bool {
		return strings.HasPrefix(st.Message(), prefix)
	}):
		// The coordinator's own words also cover processes on its host.
		return "the coordinator refused this host's peer token, or its lack of one: set the same peer.token on this host"
	case refusalCodes[code]:
		return st.Message()
	case code == codes.Unimplemented:
		return subject + " predates worker invites: upgrade it to create invites"
	case connectionCodes[code]:
		return subject + " could not be reached or did not answer: run dagu worker --check on this host to see why"
	case code == codes.Internal && coordinatorFailures[st.Message()]:
		return fmt.Sprintf("%s %s: see its log for the cause", subject, st.Message())
	default:
		// gRPC raises some codes, such as Internal, on this host too, so the
		// failure cannot be placed.
		return fmt.Sprintf("the call failed with code %s: run dagu worker --check on this host to see why", code)
	}
}

func coordinatorAddr(report coordinator.JoinReadiness) string {
	return net.JoinHostPort(report.Coordinator.Host, strconv.Itoa(report.Coordinator.Port))
}

// printJoinReadiness reports whether the coordinator of report accepts
// workers from other hosts. When it does not, it prints why and the change
// to make, as the Add worker dialog does.
func printJoinReadiness(w io.Writer, report coordinator.JoinReadiness) bool {
	addr := coordinatorAddr(report)
	if report.Err != nil {
		_, _ = fmt.Fprintf(w, "Coordinator %s did not report whether it accepts workers: %s\n", addr, callFailure("it", report.Err))
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
// only accept workers from other hosts, for how it was started, as the Add
// worker dialog gives it. coordinator.host comes from a flag, else a
// variable, else a config file, so the change goes where the value came
// from: a container's -e wins over a file but not over a flag.
func listenChange(r *coordinatorv1.GetJoinReadinessResponse) string {
	kind := r.HostSource.GetKind()
	pinned := r.HostSource.GetName() != "" &&
		(kind == coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG || kind == coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV)
	switch r.Environment {
	case coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES:
		if pinned {
			return sourceChange(r)
		}
		return fmt.Sprintf("Set %s=%s on the coordinator's workload, then restart it. With the Helm chart, deploymentMode: distributed sets it.",
			coordinatorHostVariable, allInterfacesHost)
	case coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER:
		if kind == coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG && pinned {
			return fmt.Sprintf("%s Recreate its container with -p %d:%d.", sourceChange(r), r.Port, r.Port)
		}
		variable := coordinatorHostVariable
		if kind == coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV && pinned {
			variable = r.HostSource.GetName()
		}
		return fmt.Sprintf("Recreate its container with -p %d:%d -e %s=%s.", r.Port, r.Port, variable, allInterfacesHost)
	case coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_UNSPECIFIED, coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST:
	}
	return sourceChange(r)
}

// sourceChange is the change to coordinator.host where its value came from:
// the flag or variable that set it, the config file that set it, such as
// admin.yaml over config.yaml, or else the config file Dagu reads.
func sourceChange(r *coordinatorv1.GetJoinReadinessResponse) string {
	name := r.HostSource.GetName()
	switch r.HostSource.GetKind() {
	case coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV:
		if name != "" {
			return fmt.Sprintf("Set %s=%s.", name, allInterfacesHost)
		}
	case coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG:
		if name != "" {
			return fmt.Sprintf("Start it with %s=%s.", name, allInterfacesHost)
		}
	case coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FILE:
		if name != "" {
			return fileHostChange(name)
		}
	case coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_UNSPECIFIED, coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT:
	}
	return fileHostChange(r.ConfigFile)
}

// fileHostChange is the change to coordinator.host in the config file
// file. The file may hold a coordinator block already, such as one that
// sets the port, so the change goes inside it rather than after it.
func fileHostChange(file string) string {
	return fmt.Sprintf("In %s, set host to %s inside the coordinator block, adding the block if the file has none.", file, allInterfacesHost)
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
// host with it through address, in one write. A closed standard output
// fails the write instead of ending the process, so the caller can delete
// the invite.
func printInvite(w io.Writer, address string, resp *coordinatorv1.CreateInviteResponse) error {
	signal.Ignore(syscall.SIGPIPE)
	defer signal.Reset(syscall.SIGPIPE)
	kind := "single use"
	if resp.Record.GetKind() == coordinatorv1.InviteKind_INVITE_KIND_REUSABLE {
		kind = "reusable"
	}
	expiry := "no expiry"
	if expiresAt := resp.Record.GetExpiresAt(); expiresAt > 0 {
		expiry = "expires " + time.Unix(expiresAt, 0).UTC().Format(time.RFC3339)
	}
	_, err := fmt.Fprintf(w, "Invite (%s, %s):\n  %s\nRun on the worker host:\n  "+workerJoinCommandFormat+"\n",
		kind, expiry, resp.Invite, shellQuote(address), resp.Invite)
	return err
}

// shellQuote returns s as one word of a POSIX shell command: as it is when
// the shell reads it unchanged, else in single quotes.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, shellSafeChars) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
