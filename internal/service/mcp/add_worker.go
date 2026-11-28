// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	daguapi "github.com/dagucloud/dagu/v2/api/v1"
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
	// workerImage is the image the Docker and Kubernetes commands run.
	workerImage = "ghcr.io/dagucloud/dagu:latest"
	// workerInviteEnv carries the invite into a worker container.
	workerInviteEnv = "DAGU_WORKER_INVITE"
	// workerDataDir is the data directory of the image, where a worker keeps
	// its certificate.
	workerDataDir = "/var/lib/dagu"
)

// addWorkerFields lists the fields add_worker takes besides action.
var addWorkerFields = []string{executeFieldAddress, executeFieldReusable, executeFieldExpiresIn, executeFieldLabels}

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

// addWorker returns the join readiness and, once every coordinator is ready
// and an address is given, creates an invite. Only that result holds it.
func (svc *Service) addWorker(ctx context.Context, input executeInput) (*mcpsdk.CallToolResult, map[string]any, error) {
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
		"action":       executeActionAddWorker,
		"ready":        notReady == 0,
		"coordinators": coordinators,
		"address":      addressStep(readiness),
		"references":   defaultReferenceURIs(),
	}
	in := input.AddWorker
	switch {
	case notReady > 0:
		return resultWithLinks(fmt.Sprintf("No invite created: %d of %d coordinators cannot accept workers from other hosts. "+
			"Show the user coordinators[].changes and coordinators[].error; each change needs a restart of the coordinator. Then call add_worker again.", notReady, len(coordinators))), output, nil
	case in.Address == "":
		return resultWithLinks("Every coordinator accepts workers from other hosts. No invite created: " +
			"ask for the address workers dial, starting from address.initial, and call add_worker again with it."), output, nil
	}

	body := &daguapi.CreateWorkerInviteJSONRequestBody{Address: &in.Address, Reusable: &in.Reusable}
	if in.ExpiresIn != "" {
		body.ExpiresIn = &in.ExpiresIn
	}
	created, err := svc.api.CreateWorkerInvite(ctx, daguapi.CreateWorkerInviteRequestObject{Body: body})
	if err != nil {
		return nil, nil, err
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
	output["commands"] = workerCommands(in.Address, input.Labels, invite.Invite, in.Reusable)
	message := "Invite " + invite.Id + " created. Show the user one of commands; no other result contains the invite. " +
		"A worker that joins with it lists inviteId " + invite.Id + "."
	if !in.Reusable {
		message += " Kubernetes needs reusable=true, since a new pod has no certificate."
	}
	return resultWithLinks(message), output, nil
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
		reason := "this coordinator could not create or read its CA; see its start warning"
		if report.BuiltinTLSReason != nil && *report.BuiltinTLSReason == daguapi.BuiltinTLSReasonOwnCertificates {
			reason = "this coordinator uses peer.cert_file and peer.key_file"
		}
		changes = append(changes, "Invites need the built-in CA: "+reason+".")
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

// addressStep returns step 2 of Add worker: the address to start from and
// the addresses to suggest. It never names a host the coordinators did not
// report.
func addressStep(readiness daguapi.GetWorkerJoinReadiness200JSONResponse) map[string]any {
	step := map[string]any{}
	suggestions := []string{}
	for _, entry := range readiness.Coordinators {
		report := entry.Report
		if report == nil {
			continue
		}
		port := strconv.Itoa(report.Port)
		if report.Advertise != nil && step["initial"] == nil {
			step["initial"] = net.JoinHostPort(*report.Advertise, port)
		}
		for _, addr := range report.Addresses {
			if suggestion := net.JoinHostPort(addr, port); !slices.Contains(suggestions, suggestion) {
				suggestions = append(suggestions, suggestion)
			}
		}
		if environmentIs(report, daguapi.HostEnvironmentContainer) {
			step["note"] = "The coordinator runs in a container: ask for the address of the machine that runs it."
		}
	}
	if readiness.LastInviteAddress != nil {
		step["initial"] = *readiness.LastInviteAddress
	}
	step["suggestions"] = suggestions
	return step
}

// workerCommands returns the command that joins a worker with invite for
// each way to run one. Kubernetes takes only a reusable invite.
func workerCommands(address string, labels []string, invite string, reusable bool) map[string]string {
	args := []string{"dagu", "worker", "--worker.coordinators=" + address}
	if len(labels) > 0 {
		args = append(args, "--worker.labels="+strings.Join(labels, ","))
	}
	commands := map[string]string{
		"linux": strings.Join(append(args, "--worker.invite="+invite), " "),
		"docker": "docker run -d --restart unless-stopped -v dagu-worker:" + workerDataDir + " -e " + workerInviteEnv + "=" + invite +
			" " + workerImage + " " + strings.Join(args, " "),
	}
	if reusable {
		commands["kubernetes"] = kubernetesManifest(args, invite)
	}
	return commands
}

// kubernetesManifest returns a Secret with invite and a Deployment that runs
// args with it. The data directory is an emptyDir, so a new pod enrolls
// again with the reusable invite.
func kubernetesManifest(args []string, invite string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, strconv.Quote(arg))
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
          image: ` + workerImage + `
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
