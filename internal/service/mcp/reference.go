// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = `Dagu exposes a compact MCP surface for DAG workflow operations.

Use dagu_read for current state, Markdown documents, and trusted reference resources.
Use dagu_change with mode=preview before mode=apply when editing DAG YAML or Markdown documents.
Use dagu_execute for start, enqueue, retry, and stop. retry and stop are actions inside dagu_execute.
MCP Apps hosts can render run-related dagu_read and dagu_execute results in Dagu's interactive run inspector.
After starting or enqueueing a run, read the returned dagu://runs/... resource or subscribe to it to receive a resource update notification when the run reaches a terminal state.`

type referenceResource struct {
	topic       string
	uri         string
	name        string
	title       string
	description string
	text        string
}

func referenceResources() []referenceResource {
	return []referenceResource{
		{
			topic:       "authoring",
			uri:         "dagu://reference/authoring",
			name:        "dagu_authoring_reference",
			title:       "Dagu DAG authoring",
			description: "Guidance for writing and editing Dagu DAG YAML through MCP.",
			text: `# Dagu DAG authoring

DAGs are YAML workflow definitions. Use dagu_change for edits:

1. Call dagu_change with mode=preview, type=upsert_dag, name, and spec.
2. Fix validation errors if valid=false.
3. Call dagu_change again with mode=apply only after the user intends to write.

Keep generated DAGs explicit and small. Prefer clear step names, dependencies, and command bodies over clever shell composition. Preserve existing schedules, labels, parameters, workspace labels, and lifecycle hooks unless the user asked to change them.

Authoring rules:

- Use scoped references for Dagu-managed values: ${consts.NAME}, ${params.NAME}, ${env.NAME}, ${context.run.id}, and ${steps.step_id.outputs.name}.
- Define reusable static values with top-level consts. consts must use list form with one single-entry mapping per item. A const can reference inherited or earlier consts, but runtime references such as params, env, and steps remain unresolved inside const values.
- Use shell $NAME only when the target shell or process should read the variable at execution time.
- Single-line run values are shell commands. Array-form run entries run one by one. Multi-line run values are scripts.
- Dagu does not split shell syntax such as pipes, redirects, &&, or ; into separate Dagu commands.
- Declared step outputs use a step-level outputs field and write records to DAGU_OUTPUT_FILE. Later steps read them as ${steps.step_id.outputs.name}.
- human.task defines a processless root-DAG operator step with an explicit id, required with.prompt, and optional flat scalar with.form JSON Schema. Omit form for acknowledgement-only tasks.
- Human task form properties that are required or have defaults become ${steps.step_id.outputs.name} values after completion. Do not declare outputs on a human.task step; additionalProperties defaults to false.
- Human tasks cannot run in sub-DAGs, lifecycle handlers, or foreach.steps, and they do not support reject or rewind. Root DAGs containing human tasks may run locally or on distributed workers selected by DAG-level worker_selector. The MCP tool surface does not expose human-task completion; completion uses the local dagu human-task complete command.
- type: controller lets the configured llm pick which step runs next instead of a dependency graph. It requires llm and a non-empty tasks list, where each task has a name and a description stating when it is finished. depends and router steps are rejected, and the reserved names __controller__ and ask_user cannot be used for steps.
- Use context references for run metadata, such as ${context.dag.name}, ${context.run.id}, and ${context.paths.artifacts_dir}.
- git.worktree.add creates or reuses a linked worktree in the local repository containing the step working_dir. Omit branch for a Dagu-generated branch, or set branch with create_branch: true and optional base to create a named branch.
- git.worktree.add publishes path, branch, commit, worktree_created, and branch_created. Read them as ${steps.step_id.outputs.field}; do not declare outputs on the step.
- git.worktree.remove accepts branch, path, or both. Set force only to discard worktree changes. Set delete_branch to remove a merged branch, and add force_delete_branch to remove an unmerged branch.
- harness.run can use root-level container or step-level container. A step-level container takes precedence for that step.
- Containerized harness runs support Dagu CLI providers and custom providers that pass the prompt as an argument or flag. They do not support provider=builtin, with.stdin, or custom prompt_mode=stdin.
- Docker or Podman is selected by the Dagu service process through DAGU_CONTAINER_RUNTIME and optional DAGU_PODMAN_HOST, not by a DAG YAML runtime field.`,
		},
		{
			topic:       "tools",
			uri:         "dagu://reference/tools",
			name:        "dagu_mcp_tools_reference",
			title:       "Dagu MCP tools",
			description: "The compact Dagu MCP tool surface.",
			text: `# Dagu MCP tools

The server intentionally exposes three tools.

- dagu_read: read DAGs, DAG specs, Markdown documents, DAG-runs, logs, list views, and reference resources.
- dagu_change: preview or apply DAG YAML and workspace-aware document changes.
- dagu_execute: start, enqueue, retry, or stop a DAG-run.

Detailed tool references:

- dagu://reference/read-tool: dagu_read inputs, targets, URI mode, query parameters, outputs, and errors.
- dagu://reference/change-tool: dagu_change preview and apply contract for DAG YAML and document changes.
- dagu://reference/execute-tool: dagu_execute start, enqueue, retry, and stop contract.
- dagu://reference/apps: interactive run inspector behavior for MCP Apps hosts.

Use dagu_execute action=retry with name and dagRunId for retry. Use action=stop with name and dagRunId for stop. Use action=start or action=enqueue with targetType=dag for a stored DAG, or targetType=inline_spec with spec for an ad hoc run.`,
		},
		{
			topic:       "apps",
			uri:         "dagu://reference/apps",
			name:        "dagu_mcp_apps_reference",
			title:       "Dagu MCP Apps",
			description: "Interactive run inspector behavior for MCP Apps hosts.",
			text: `# Dagu MCP Apps

Dagu supports the io.modelcontextprotocol/ui extension with text/html;profile=mcp-app resources.

Hosts that support MCP Apps can render run-related dagu_read and dagu_execute results in an interactive run inspector. The inspector can display recent runs, run status, step status, scheduler logs, and individual step logs. Refresh, stop, and retry actions use the existing Dagu MCP resources and tools, so authentication, authorization, and audit attribution are unchanged.

The app is a progressive enhancement. Clients without MCP Apps support continue to receive the same text content, structuredContent, and resource links from dagu_read and dagu_execute.`,
		},
		{
			topic:       "read-tool",
			uri:         "dagu://reference/read-tool",
			name:        "dagu_mcp_read_tool_reference",
			title:       "Dagu MCP read tool",
			description: "Detailed dagu_read input, output, and error reference.",
			text: `# dagu_read reference

Purpose: read Dagu state and built-in reference content. The tool is read-only.

Addressing:

- Target mode uses target plus target-specific fields.
- URI mode uses uri and forbids all target-mode fields.

Fields:

- target: required in target mode. Values are references, reference, dags, dag, dag_spec, docs, doc, doc_search, runs, run, run_logs, and step_log.
- name: DAG name or reference topic name. Required for dag, dag_spec, run, run_logs, and step_log. Optional for reference; defaults to authoring. Forbidden for references, dags, and runs.
- dagRunId: required for run, run_logs, and step_log. Forbidden for other targets.
- stepName: required for step_log. Forbidden for other targets.
- query: URL query string without a leading question mark. Allowed for dags, docs, runs, and run_logs.
- workspace: all, default, or a workspace name. Optional for docs and doc_search; omitted means all accessible workspaces. Required for doc, where all is not allowed.
- path: document path without .md. Required for doc.
- search: search text. Required for doc_search.
- uri: dagu:// resource URI for URI mode.

Targets:

- references lists built-in reference topics.
- reference reads one Markdown reference topic.
- dags lists DAGs.
- dag reads DAG details.
- dag_spec reads the current DAG YAML.
- docs lists the document tree or a flat document list.
- doc reads one Markdown document.
- doc_search searches Markdown content in accessible documents.
- runs lists DAG-runs.
- run reads one DAG-run.
- run_logs reads scheduler and step log metadata.
- step_log reads stdout and stderr for one DAG-run step.

Query parameters:

- dags: page, perPage, name, labels, sort, order.
- docs: page, perPage, flat, sort, order.
- runs: name, dagRunId, status, fromDate, toDate, limit, cursor, labels. status may repeat.
- run_logs: tail.

Output:

- Successful result text is Dagu read completed.
- Structured output has target, data, references, and uri when the read has a canonical resource URI.
- Reference URIs in references point to built-in guidance resources.
- Document list and search entries include canonical dagu://docs/{workspace}/{path} URIs. Nested document paths are encoded as one URI segment.

Errors:

- invalid_tool_input for malformed target-mode input.
- invalid_resource_uri for malformed URI-mode input.
- unsupported_read_target for unknown target.
- unsupported_resource for unknown dagu:// family.
- resource_not_found, resource_unavailable, or internal_error for runtime failures.`,
		},
		{
			topic:       "change-tool",
			uri:         "dagu://reference/change-tool",
			name:        "dagu_mcp_change_tool_reference",
			title:       "Dagu MCP change tool",
			description: "Detailed dagu_change input, output, and error reference.",
			text: `# dagu_change reference

Purpose: validate or write DAG YAML and Markdown document changes.

Fields:

- mode: preview or apply. Defaults to preview.
- type: upsert_dag, upsert_doc, rename_doc, or delete_doc. Defaults to upsert_dag.
- name and spec: required for upsert_dag.
- workspace: default or a named workspace. Required for document changes; all is not allowed.
- path: document or directory path without .md. Required for document changes.
- content: full Markdown content. Required for upsert_doc; empty content is allowed.
- newPath: destination document or directory path. Required for rename_doc.

Mode behavior:

- preview validates the requested operation and reads enough current state to report whether it can target a file or directory, without writing.
- apply repeats validation and performs the requested operation through Dagu's existing workspace-aware Documents API.
- upsert_doc creates a missing document or updates an existing document.
- rename_doc and delete_doc support both documents and directories.

Output:

- Successful result text is Dagu change completed.
- DAG output has dagName, valid, errors, applied, and dagUri.
- Document output has workspace, path, valid, applied, and docUri when the target is a document.

Errors:

- invalid_tool_input for missing or incompatible fields, invalid paths, unknown mode, unknown type, or malformed input.
- unauthorized when the caller cannot perform the requested write.
- resource_not_found when a rename or delete source does not exist.
- conflict when a document path conflicts with another file or directory.
- internal_error for unexpected failures.`,
		},
		{
			topic:       "execute-tool",
			uri:         "dagu://reference/execute-tool",
			name:        "dagu_mcp_execute_tool_reference",
			title:       "Dagu MCP execute tool",
			description: "Detailed dagu_execute input, output, and error reference.",
			text: `# dagu_execute reference

Purpose: control DAG execution through start, enqueue, retry, and stop actions.

Fields:

- action: required. Values are start, enqueue, retry, and stop.
- targetType: dag, inline_spec, or run. Defaults to run for retry and stop, inline_spec when spec is present, otherwise dag.
- name: DAG name. Required for stored DAG runs and run actions.
- spec: inline DAG YAML for targetType=inline_spec.
- dagRunId: DAG-run identifier. Required for retry and stop. Optional override for start and enqueue.
- params: run parameters string for start and enqueue.
- queue: queue name for enqueue.
- singleton: singleton run flag for start and enqueue.
- labels: labels for start and enqueue.
- stepName: optional failed step name for retry.

Action behavior:

- start runs a stored DAG when targetType=dag and runs an inline spec when targetType=inline_spec.
- enqueue enqueues a stored DAG or inline spec.
- retry retries an existing DAG-run and may target a step with stepName.
- stop stops an existing DAG-run.

Output:

- Successful result text is Dagu execute completed.
- Structured output has action, targetType, dagName, dagRunId, and references.
- When a run is identified, output includes runUri, logsUri, and subscribe guidance.

Errors:

- invalid_tool_input for missing fields, unknown action, unsupported targetType, or malformed input.
- unauthorized when the caller cannot perform the requested execution operation.
- resource_not_found when the named DAG or DAG-run does not exist.
- resource_unavailable or internal_error for runtime failures.`,
		},
		{
			topic:       "notifications",
			uri:         "dagu://reference/notifications",
			name:        "dagu_notifications_reference",
			title:       "Dagu MCP notifications",
			description: "How completion notification works over MCP resources.",
			text: `# Dagu MCP notifications

dagu_execute returns resource links for the DAG-run and logs when a run can be identified.

Clients that support MCP resource subscriptions can subscribe to the dagu://runs/{name}/{dagRunId} resource. Dagu sends a resource update notification when the run reaches a terminal state: success, failed, aborted, partial success, or rejected.

Clients without resource subscription support should poll dagu_read target=run with the same name and dagRunId.`,
		},
	}
}

func defaultReferenceURIs() []string {
	refs := referenceResources()
	uris := make([]string, 0, 3)
	for _, ref := range refs {
		switch ref.topic {
		case "authoring", "tools", "notifications":
			uris = append(uris, ref.uri)
		}
	}
	return uris
}

func referenceByTopic(topic string) (referenceResource, bool) {
	for _, ref := range referenceResources() {
		if ref.topic == topic {
			return ref, true
		}
	}
	return referenceResource{}, false
}

func promptCreateDAG(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	goal := strings.TrimSpace(req.Params.Arguments["goal"])
	if goal == "" {
		goal = "Create a Dagu DAG from the user's request."
	}
	return promptResult("Create a Dagu DAG", "Use dagu://reference/authoring. Draft a YAML spec for this goal: "+goal+"\n\nCall dagu_change with mode=preview first. Apply only when the user wants the file written."), nil
}

func promptEditDAG(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	name := strings.TrimSpace(req.Params.Arguments["name"])
	change := strings.TrimSpace(req.Params.Arguments["change"])
	if change == "" {
		change = "Apply the requested DAG edit."
	}
	return promptResult("Edit a Dagu DAG", "Read dagu://dags/"+pathEscape(name)+"/spec, make only this change: "+change+"\n\nValidate with dagu_change mode=preview. Apply only when the user wants the edit written."), nil
}

func promptCreateDoc(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	workspace := strings.TrimSpace(req.Params.Arguments["workspace"])
	path := strings.TrimSpace(req.Params.Arguments["path"])
	goal := strings.TrimSpace(req.Params.Arguments["goal"])
	if goal == "" {
		goal = "Create the Markdown document requested by the user."
	}
	return promptResult(
		"Create a Dagu document",
		"Draft Markdown for this goal: "+goal+"\n\nCall dagu_change with mode=preview, type=upsert_doc, workspace="+workspace+", path="+path+", and content set to the complete drafted Markdown. Apply only when the user wants the document written.",
	), nil
}

func promptEditDoc(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	workspace := strings.TrimSpace(req.Params.Arguments["workspace"])
	path := strings.TrimSpace(req.Params.Arguments["path"])
	change := strings.TrimSpace(req.Params.Arguments["change"])
	if change == "" {
		change = "Apply the requested document edit."
	}
	return promptResult(
		"Edit a Dagu document",
		"Read "+docURI(workspace, path)+", preserve unrelated content, and make only this change: "+change+"\n\nCall dagu_change with mode=preview, type=upsert_doc, workspace="+workspace+", path="+path+", and content set to the complete edited Markdown. Apply only when the user wants the edit written.",
	), nil
}

func promptDebugRun(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	name := strings.TrimSpace(req.Params.Arguments["name"])
	dagRunID := strings.TrimSpace(req.Params.Arguments["dagRunId"])
	runURI := runURI(name, dagRunID)
	logsURI := runLogsURI(name, dagRunID)
	return promptResult("Debug a Dagu run", "Read "+runURI+" and "+logsURI+". Identify the failing step, summarize the likely cause, and propose the smallest next action. Use dagu_execute action=retry or action=stop only when the user asks for it."), nil
}

func promptResult(description, text string) *mcpsdk.GetPromptResult {
	return &mcpsdk.GetPromptResult{
		Description: description,
		Messages: []*mcpsdk.PromptMessage{{
			Role:    mcpsdk.Role("user"),
			Content: &mcpsdk.TextContent{Text: text},
		}},
	}
}
