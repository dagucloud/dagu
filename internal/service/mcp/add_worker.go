// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	executeActionAddWorker = "add_worker"

	executeFieldAddress   = "address"
	executeFieldReusable  = "reusable"
	executeFieldExpiresIn = "expiresIn"

	// allInterfacesHost is the coordinator.host that accepts workers from
	// other hosts.
	allInterfacesHost = "0.0.0.0"
	// workerImageRepository is the repository of the published images. Each
	// release is tagged with its version.
	workerImageRepository = "ghcr.io/dagucloud/dagu"
	// workerInviteEnv carries the invite into a worker container.
	workerInviteEnv = "DAGU_WORKER_INVITE"
	// workerDataDir is the data directory of the image, where a worker keeps
	// its certificate.
	workerDataDir = "/var/lib/dagu"
	// maxExpiresIn is the longest expiresIn the invite API accepts: its whole
	// seconds, rounded up, fit the coordinator's expiry.
	maxExpiresIn = time.Duration(math.MaxInt64/int64(time.Second)) * time.Second
	// maxImageTag is the longest tag an image registry accepts.
	maxImageTag = 128
	// publishedVersionPattern matches a version, without its leading v, of
	// a vX.Y.Z or vX.Y.Z-* tag, which the image workflow publishes as that
	// image tag, in the characters an image tag may hold.
	publishedVersionPattern = `^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9_.-]+)?$`
	// devBuildPattern matches the version of a development build, which no
	// release is tagged with: the default 0.0.0, git describe's commit count
	// and hash after the last tag, or a snapshot's -next.
	devBuildPattern = `^0\.0\.0$|-[0-9]+-g[0-9a-f]+$|-next$`
	// workerLabelRule is the rule a worker applies to its labels at start.
	workerLabelRule = "a label is key or key=value; a key holds only letters, digits, -, _, and ., " +
		"at most 63, and a value only those and /, at most 255, each starting with a letter or digit"
)

// builtinLabelKeys are the labels a worker sets from its own host.
var builtinLabelKeys = []string{"os", "arch"}

// addWorkerFields lists the fields add_worker takes besides action.
var addWorkerFields = []string{executeFieldAddress, executeFieldReusable, executeFieldExpiresIn, executeFieldLabels}

var (
	publishedVersion = regexp.MustCompile(publishedVersionPattern)
	devBuild         = regexp.MustCompile(devBuildPattern)
	// containerID matches a short or full container ID used as a host name.
	containerID = regexp.MustCompile(`^[0-9a-f]{12}([0-9a-f]{52})?$`)
	// dnsName matches one DNS name, so a list, a zone, or shell syntax is
	// not taken for a host.
	dnsName = regexp.MustCompile(`^([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.?$`)
)

// addWorkerInput holds the fields of the add_worker action. The worker
// labels are executeInput.Labels.
type addWorkerInput struct {
	Address   string
	Reusable  bool
	ExpiresIn string
}

// isAddWorkerField reports whether a field belongs to add_worker only.
func isAddWorkerField(field string) bool {
	return field == executeFieldAddress || field == executeFieldReusable || field == executeFieldExpiresIn
}

// parseAddWorkerField reads a field of add_worker into input.
func parseAddWorkerField(input *addWorkerInput, field string, value json.RawMessage) *executeToolError {
	var err *executeToolError
	switch field {
	case executeFieldAddress:
		input.Address, err = executeStringField(field, value)
	case executeFieldReusable:
		input.Reusable, err = executeBoolField(field, value)
	case executeFieldExpiresIn:
		input.ExpiresIn, err = executeStringField(field, value)
	}
	return err
}

// validateAddWorkerInput refuses the fields add_worker does not take.
func validateAddWorkerInput(provided map[string]bool) *executeToolError {
	for field := range provided {
		if field != executeFieldAction && !slices.Contains(addWorkerFields, field) {
			return executeInputError(fmt.Sprintf("The %s field is not valid for %s.", field, executeActionAddWorker), field)
		}
	}
	return nil
}

// checkAddWorkerInput refuses an address a worker on another host cannot
// dial, an expiresIn the invite API would refuse, a label the worker would
// refuse at start, and invite text in an address or a label, which would
// reach the invite record or the worker's log. A refusal never repeats the
// text it refuses.
func checkAddWorkerInput(input executeInput) *executeToolError {
	if address := input.AddWorker.Address; address != "" {
		if holdsInvite(address) {
			return executeInputError("The address field holds an invite; the invite goes only into the commands.", executeFieldAddress)
		}
		if problem := dialAddressProblem(address); problem != "" {
			return executeInputError("The address field "+problem+".", executeFieldAddress)
		}
	}
	if expiresIn := input.AddWorker.ExpiresIn; expiresIn != "" {
		if d, err := time.ParseDuration(expiresIn); err != nil || d <= 0 || d > maxExpiresIn {
			return executeInputError("The expiresIn field must be a positive duration of at most "+maxExpiresIn.String()+", such as 24h.", executeFieldExpiresIn)
		}
	}
	return workerLabelsProblem(input.Labels)
}

// workerLabelsProblem refuses labels the worker would refuse at start, by
// the worker's own parsing and rules. A refusal names a label by its
// position, never by its text, which may be a pasted invite.
//
// The worker parses the joined labels at commas, so with no comma in any
// label, parsing each label alone and tracking its key gives the worker's
// result.
func workerLabelsProblem(labels []string) *executeToolError {
	keys := make(map[string]bool, len(labels))
	for i, label := range labels {
		position := fmt.Sprintf("Worker label %d", i+1)
		if holdsInvite(label) {
			return executeInputError(position+" holds an invite; the invite goes only into the commands.", executeFieldLabels)
		}
		one := config.ParseWorkerLabels(label)
		if strings.Contains(label, ",") || len(one) != 1 {
			return executeInputError(position+" must be one key or key=value, without a comma.", executeFieldLabels)
		}
		accepted, err := ir.WorkerLabels(one)
		if err != nil {
			return executeInputError(position+" breaks the worker label rule: "+workerLabelRule+".", executeFieldLabels)
		}
		for key := range accepted {
			switch {
			case slices.Contains(builtinLabelKeys, key):
				return executeInputError(position+" sets "+key+", which a worker sets from its own host.", executeFieldLabels)
			case keys[key]:
				return executeInputError(position+" sets a key an earlier label sets; keys compare without case.", executeFieldLabels)
			}
			keys[key] = true
		}
	}
	return nil
}

// holdsInvite reports whether s holds text shaped like an invite.
func holdsInvite(s string) bool {
	return coordinator.RedactInvites(s) != s
}

// dialAddressProblem says why a worker on another host cannot dial addr, or
// returns "" when it can. Spec 108 §Add Worker states the rule.
func dialAddressProblem(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return "must be host:port, such as 192.168.1.20:50055"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "must have a port from 1 to 65535"
	}
	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsUnspecified():
			return "must name one address, not " + host
		case ip.IsLoopback():
			return "must not be a loopback address, which reaches only the worker's own machine"
		case ip.IsLinkLocalUnicast():
			return "must not be a link-local address, which another network does not route"
		}
		return ""
	}
	switch {
	case !dnsName.MatchString(host):
		return "must name one host: an IP address without a zone, or a DNS name"
	case strings.EqualFold(strings.TrimSuffix(host, "."), "localhost"):
		return "must not be localhost, which reaches only the worker's own machine"
	case containerID.MatchString(host):
		return "must not be a container ID; give the address of the machine that runs the container"
	}
	return ""
}

// workerImage returns the published image of the server version, and false
// for a build that has no published image.
func workerImage(version string) (string, bool) {
	tag := strings.TrimPrefix(version, "v")
	if !publishedVersion.MatchString(tag) || devBuild.MatchString(tag) || len(tag) > maxImageTag {
		return "", false
	}
	return workerImageRepository + ":" + tag, true
}

// addWorker returns the join readiness and, once every coordinator is ready
// and an address is given, creates an invite. Only that result holds it.
func (svc *Service) addWorker(ctx context.Context, input executeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
	if err := svc.api.RequireWorkerAccess(ctx); err != nil {
		return nil, nil, err
	}
	if err := checkAddWorkerInput(input); err != nil {
		return nil, nil, err
	}
	resp, err := svc.api.GetWorkerJoinReadiness(ctx, daguapi.GetWorkerJoinReadinessRequestObject{})
	if err != nil {
		return nil, nil, err
	}
	readiness, ok := resp.(daguapi.GetWorkerJoinReadiness200JSONResponse)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected join readiness response %T", resp)
	}
	coordinators, notReady := joinSteps(readiness.Coordinators)
	output := map[string]any{
		"action":         executeActionAddWorker,
		"ready":          notReady == 0,
		"coordinators":   coordinators,
		"addressOptions": addressOptions(readiness),
		"references":     defaultReferenceURIs(),
	}
	in := input.AddWorker
	switch {
	case notReady > 0:
		return resultWithLinks(fmt.Sprintf("No invite created: %d of %d coordinators cannot accept workers from other hosts. "+
			"Show the user coordinators[].changes and coordinators[].error; each change needs a restart of the coordinator. Then call add_worker again.", notReady, len(coordinators))), output, nil
	case in.Address == "":
		return resultWithLinks("Every coordinator accepts workers from other hosts. No invite created: " +
			"ask for the address workers dial, starting from addressOptions.initial, and call add_worker again with it."), output, nil
	}

	body := &daguapi.CreateWorkerInviteJSONRequestBody{Address: &in.Address, Reusable: &in.Reusable}
	if in.ExpiresIn != "" {
		body.ExpiresIn = &in.ExpiresIn
	}
	created, err := svc.api.CreateWorkerInvite(ctx, daguapi.CreateWorkerInviteRequestObject{Body: body})
	if err != nil {
		return nil, nil, inviteInputError(err)
	}
	invite, ok := created.(daguapi.CreateWorkerInvite201JSONResponse)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected worker invite response %T", created)
	}
	kind := daguapi.WorkerInviteKindSingleUse
	if in.Reusable {
		kind = daguapi.WorkerInviteKindReusable
	}
	output["inviteId"] = invite.Id
	output["invite"] = invite.Invite
	output["kind"] = kind
	if invite.ExpiresAt != nil {
		output["expiresAt"] = *invite.ExpiresAt
	}
	image, published := workerImage(config.Version)
	output["commands"] = workerCommands(in.Address, input.Labels, invite.Invite, image, in.Reusable)
	message := "Invite " + invite.Id + " created. Show the user one of commands; no other result contains the invite. " +
		"A worker that joins with it lists inviteId " + invite.Id + "."
	switch {
	case !published:
		output["imageUnavailable"] = "This server runs build " + config.Version + ", which has no published image, " +
			"so commands has no container command. A worker container needs an image built from the same source."
		message += " " + output["imageUnavailable"].(string)
	case !in.Reusable:
		message += " The kubernetes command needs reusable=true, since a new pod has no certificate."
	}
	return resultWithLinks(message), output, nil
}

// inviteInputError names the field of a create refusal for a bad request:
// the API and the coordinator start its message with the field. Any other
// error returns as it is.
func inviteInputError(err error) error {
	apiErr, ok := errors.AsType[*frontendapi.Error](err)
	if !ok || apiErr.HTTPStatus != http.StatusBadRequest {
		return err
	}
	field := ""
	switch {
	case strings.HasPrefix(apiErr.Message, executeFieldExpiresIn), strings.HasPrefix(apiErr.Message, "expires_in"):
		field = executeFieldExpiresIn
	case strings.HasPrefix(apiErr.Message, executeFieldAddress):
		field = executeFieldAddress
	}
	return executeInputError(apiErr.Message, field)
}

// joinSteps returns step 1 of Add worker for each coordinator, and how many
// of them are not ready.
func joinSteps(entries []daguapi.CoordinatorJoinReadiness) ([]map[string]any, int) {
	steps := make([]map[string]any, 0, len(entries))
	notReady := 0
	for _, entry := range entries {
		step := map[string]any{"instanceId": entry.InstanceId, "host": entry.Host, "port": entry.Port}
		report := entry.Report
		ready := report != nil && report.Ready
		if !ready {
			notReady++
		}
		step["ready"] = ready
		if report == nil {
			if entry.Error != nil {
				step["error"] = *entry.Error
			}
			if entry.ErrorReason != nil {
				step["errorReason"] = *entry.ErrorReason
			}
			steps = append(steps, step)
			continue
		}
		step["report"] = report
		if changes := joinChanges(report); len(changes) > 0 {
			step["changes"] = changes
		}
		if notes := joinNotes(report); len(notes) > 0 {
			step["notes"] = notes
		}
		steps = append(steps, step)
	}
	return steps, notReady
}

// joinChanges returns what to change before the coordinator of report accepts
// invited workers. Each change needs a restart of the coordinator.
func joinChanges(report *daguapi.JoinReadinessReport) []string {
	var changes []string
	if report.LoopbackOnly {
		changes = append(changes, fmt.Sprintf("It listens on %s, so only its own machine can connect. %s", report.ListenHost, listenChange(report)))
	}
	if !report.BuiltinTLS {
		change := "Built-in TLS is unavailable. Check the coordinator startup log."
		if report.BuiltinTLSReason != nil && *report.BuiltinTLSReason == daguapi.BuiltinTLSReasonOwnCertificates {
			change = "Invites need built-in TLS, and this coordinator uses peer.cert_file and peer.key_file."
		}
		changes = append(changes, change)
	}
	return changes
}

// listenChange is the change that makes a coordinator listening on loopback
// only accept workers from other hosts, for how it was started.
func listenChange(report *daguapi.JoinReadinessReport) string {
	var source daguapi.ConfigSource
	if report.HostSource != nil {
		source = *report.HostSource
	}
	switch {
	case environmentIs(report, daguapi.HostEnvironmentKubernetes):
		return "Set coordinator.host to " + allInterfacesHost + " through the chart's values."
	case environmentIs(report, daguapi.HostEnvironmentContainer):
		return fmt.Sprintf("Recreate its container with -p %d:%d -e DAGU_COORDINATOR_HOST=%s.", report.Port, report.Port, allInterfacesHost)
	case source.Kind == daguapi.ConfigSourceKindEnv && source.Name != nil:
		return "Set " + *source.Name + "=" + allInterfacesHost + "."
	case source.Kind == daguapi.ConfigSourceKindFlag && source.Name != nil:
		return "Start it with " + *source.Name + "=" + allInterfacesHost + "."
	default:
		return "Add this to " + report.ConfigFile + ":\n  coordinator:\n    host: " + allInterfacesHost
	}
}

// joinNotes returns the reminders that hold even for a ready coordinator.
func joinNotes(report *daguapi.JoinReadinessReport) []string {
	switch {
	case environmentIs(report, daguapi.HostEnvironmentContainer):
		return []string{fmt.Sprintf("Publish port %d of its container; Dagu cannot see the published ports.", report.Port)}
	case environmentIs(report, daguapi.HostEnvironmentKubernetes):
		return []string{"Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort."}
	}
	return nil
}

func environmentIs(report *daguapi.JoinReadinessReport, environment daguapi.HostEnvironment) bool {
	return report.Environment != nil && *report.Environment == environment
}

// addressOptions returns step 2 of Add worker: the address to start from and
// the addresses to suggest. It never names a host the coordinators did not
// report, and offers only addresses the address rule accepts.
func addressOptions(readiness daguapi.GetWorkerJoinReadiness200JSONResponse) map[string]any {
	step := map[string]any{}
	usable := func(addr string) bool { return dialAddressProblem(addr) == "" && !holdsInvite(addr) }
	if readiness.LastInviteAddress != nil && usable(*readiness.LastInviteAddress) {
		step["initial"] = *readiness.LastInviteAddress
	}
	suggestions := []string{}
	for _, entry := range readiness.Coordinators {
		report := entry.Report
		if report == nil {
			continue
		}
		port := strconv.Itoa(report.Port)
		if report.Advertise != nil && step["initial"] == nil {
			if advertised := net.JoinHostPort(*report.Advertise, port); usable(advertised) {
				step["initial"] = advertised
			}
		}
		for _, addr := range report.Addresses {
			if suggestion := net.JoinHostPort(addr, port); usable(suggestion) && !slices.Contains(suggestions, suggestion) {
				suggestions = append(suggestions, suggestion)
			}
		}
		if environmentIs(report, daguapi.HostEnvironmentContainer) {
			step["note"] = "The coordinator runs in a container: ask for the address of the machine that runs it."
		}
	}
	step["suggestions"] = suggestions
	return step
}

// workerCommands returns the command that joins a worker with invite for
// each way to run one. Without image, it has no container commands.
// Kubernetes takes only a reusable invite.
func workerCommands(address string, labels []string, invite, image string, reusable bool) map[string]string {
	args := []string{"dagu", "worker", "--worker.coordinators=" + address}
	if len(labels) > 0 {
		args = append(args, "--worker.labels="+strings.Join(labels, ","))
	}
	commands := map[string]string{"linux": shellCommand(append(args, "--worker.invite="+invite)...)}
	if image == "" {
		return commands
	}
	docker := []string{"docker", "run", "-d", "--restart", "unless-stopped", "-v", "dagu-worker:" + workerDataDir,
		"-e", workerInviteEnv + "=" + invite, image}
	commands["docker"] = shellCommand(append(docker, args...)...)
	if reusable {
		commands["kubernetes"] = kubernetesManifest(args, invite, image)
	}
	return commands
}

// shellCommand joins args into a POSIX shell command, quoting each argument
// that holds a character the shell treats specially.
func shellCommand(args ...string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "" && strings.Trim(arg, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
			quoted = append(quoted, arg)
			continue
		}
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return strings.Join(quoted, " ")
}

// kubernetesManifest returns a Secret with invite and a Deployment that runs
// image with args and takes the invite from the Secret. The data directory
// is an emptyDir, so a new pod enrolls again with the reusable invite.
func kubernetesManifest(args []string, invite, image string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		// $$ keeps a $ from expanding a variable reference such as
		// $(DAGU_WORKER_INVITE).
		quoted = append(quoted, strconv.Quote(strings.ReplaceAll(arg, "$", "$$")))
	}
	return `apiVersion: v1
kind: Secret
metadata:
  name: dagu-worker-invite
stringData:
  invite: ` + invite + `
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dagu-worker
spec:
  replicas: 1
  selector:
    matchLabels:
      app: dagu-worker
  template:
    metadata:
      labels:
        app: dagu-worker
    spec:
      containers:
        - name: worker
          image: ` + image + `
          args: [` + strings.Join(quoted, ", ") + `]
          env:
            - name: ` + workerInviteEnv + `
              valueFrom:
                secretKeyRef:
                  name: dagu-worker-invite
                  key: invite
          volumeMounts:
            - name: data
              mountPath: ` + workerDataDir + `
      volumes:
        - name: data
          emptyDir: {}
`
}
