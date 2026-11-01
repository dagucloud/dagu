// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package extauthz

// Action is an AuthZEN action name sent to the PDP.
type Action string

// Action names sent to the PDP. The list is part of the public contract of
// the hook: a PDP policy is written against these names.
const (
	// ActionWorkflowsRead reads workflow definitions, lists and search results.
	ActionWorkflowsRead Action = "workflows.read"
	// ActionWorkflowsWrite creates, edits, renames, deletes or configures a
	// workflow, or runs an inline workflow spec. Dagu still applies the
	// permissions.write_dags setting: the PDP cannot allow what it denies.
	ActionWorkflowsWrite Action = "workflows.write"
	// ActionRunsRead reads runs, run history, outputs and artifacts.
	ActionRunsRead Action = "runs.read"
	// ActionRunsStart starts or enqueues a run.
	ActionRunsStart Action = "runs.start"
	// ActionRunsStop stops or dequeues a run.
	ActionRunsStop Action = "runs.stop"
	// ActionRunsRetry retries, reschedules or resumes a run.
	ActionRunsRetry Action = "runs.retry"
	// ActionRunsUpdate approves, rejects or changes steps of a running run.
	ActionRunsUpdate Action = "runs.update"
	// ActionRunsDelete deletes a run record.
	ActionRunsDelete Action = "runs.delete"
	// ActionLogsRead reads run and step logs.
	ActionLogsRead Action = "logs.read"
	// ActionQueuesRead reads queues and queue items.
	ActionQueuesRead Action = "queues.read"
	// ActionMetricsRead reads metrics, workers and service status.
	ActionMetricsRead Action = "metrics.read"
	// ActionSettingsRead reads every other resource of the server.
	ActionSettingsRead Action = "settings.read"
	// ActionSettingsWrite changes every other resource of the server.
	ActionSettingsWrite Action = "settings.write"
	// ActionEventsRead opens the server-sent event stream used by the UI.
	ActionEventsRead Action = "events.read"
	// ActionMCPUse uses the MCP endpoint.
	ActionMCPUse Action = "mcp.use"
	// ActionRemoteNodesUse forwards a request to a remote node.
	ActionRemoteNodesUse Action = "remote_nodes.use"
)

// Resource types sent to the PDP.
const (
	// ResourceTypeWorkflow identifies one workflow by name.
	ResourceTypeWorkflow = "workflow"
	// ResourceTypeServer identifies the Dagu server itself.
	ResourceTypeServer = "server"
	// ResourceTypeRemoteNode identifies a configured remote node by name.
	ResourceTypeRemoteNode = "remote_node"

	// ServerResourceID is the resource id of ResourceTypeServer.
	ServerResourceID = "dagu"
)

// SubjectTypeUser is the AuthZEN subject type sent for token subjects.
const SubjectTypeUser = "user"

// WorkflowResource returns the resource of one workflow. A non-empty run ID
// is sent as the "run_id" resource property.
func WorkflowResource(name, runID string) Resource {
	r := Resource{Type: ResourceTypeWorkflow, ID: name}
	if runID != "" {
		r.Properties = map[string]any{"run_id": runID}
	}
	return r
}

// ServerResource returns the resource of the Dagu server.
func ServerResource() Resource {
	return Resource{Type: ResourceTypeServer, ID: ServerResourceID}
}

// RemoteNodeResource returns the resource of a remote node.
func RemoteNodeResource(name string) Resource {
	return Resource{Type: ResourceTypeRemoteNode, ID: name}
}
