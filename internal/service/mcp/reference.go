// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = `Dagu exposes a compact MCP surface for DAG workflow operations.

Use dagu_read for current state, Wiki pages, and trusted reference resources.
Use dagu_change with mode=preview before mode=apply when editing DAG YAML, DAG default profiles, or Wiki pages.
Use dagu_execute for start, enqueue, retry, and stop. retry and stop are actions inside dagu_execute.
MCP Apps hosts can render run-related dagu_read and dagu_execute results in Dagu's interactive run inspector.
To follow a run to completion, pass wait=true to dagu_execute, or read the returned dagu://runs/... resource, or subscribe to it to receive a resource update notification when the run reaches a terminal state.`

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
- DAG YAML does not contain the server-side default runtime profile. A schedule profile is an activation filter: the schedule runs only when it matches the DAG's effective default profile. Read it with dagu_read target=dag_profile. Set or clear it with dagu_change type=set_dag_profile or clear_dag_profile.
- A DAG profile change is visible to the scheduler on its next minute tick. Time spent under a non-matching profile is not scheduler downtime, so catch-up does not replay those schedule slots.
- Declared value outputs use a step-level outputs field and write records to DAGU_OUTPUT_FILE. Later steps read them as ${steps.step_id.outputs.name}.
- type: build is for local workflows that transform stable regular-file inputs into reusable file outputs.
- A build path step declares named inputs entries with path and at most one outputs entry with path. Only host command or shell steps without containers may declare build paths. Matching canonical producer-output and consumer-input paths infer dependencies, and each output path must have one producer.
- Inside the owning step, ${inputs.name} is the final input path and ${outputs.name} is a fresh per-attempt staging path. Write the result to ${outputs.name}; after commit or reuse, dependent steps read the final path as ${steps.step_id.outputs.name}. stdout, stderr, and artifact stream destinations must not target declared build paths.
- Potentially reusable producers expose downstream data through ${steps.step_id.outputs.name}; attempt-only stdout, stderr, exit-code, output, and outputs references are invalid because reuse does not recreate them. Path-output steps cannot use continue_on.mark_success.
- Build workflows are local-only. Distributed execution requests are rejected because materialization fencing is not shared across workers.
- human.task defines a processless root-DAG operator step with an explicit id, required with.prompt, and optional flat scalar with.form JSON Schema. Omit form for acknowledgement-only tasks.
- Human task form properties that are required or have defaults become ${steps.step_id.outputs.name} values after completion. Do not declare outputs on a human.task step; additionalProperties defaults to false.
- Human tasks cannot run in sub-DAGs, lifecycle handlers, or foreach.steps, and they do not support reject or rewind. Root DAGs containing human tasks may run locally or on distributed workers selected by DAG-level worker_selector. The MCP tool surface does not expose human-task completion; completion uses the local dagu human-task complete command.
- type: agent lets the configured llm pick which step runs next instead of a dependency graph. It requires llm and a non-empty tasks list, where each task has a name and a description stating when it is finished. depends and router steps are rejected, and the reserved names __agent__ and ask_user cannot be used for steps.
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

- dagu_read: read DAGs, DAG specs, DAG default profiles, Wiki pages, DAG-runs, logs, list views, and reference resources.
- dagu_change: preview or apply DAG YAML, DAG default profile, and workspace-aware Wiki changes.
- dagu_execute: start, enqueue, retry, or stop a DAG-run.

Detailed tool references:

- dagu://reference/read-tool: dagu_read inputs, targets, URI mode, query parameters, outputs, and errors.
- dagu://reference/change-tool: dagu_change preview and apply contract for DAG YAML, DAG profiles, and Wiki changes.
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

- target: required in target mode. Values are references, reference, dags, dag, dag_spec, dag_profile, dag_search, wiki, wiki_page, wiki_search, runs, run, run_logs, and step_log.
- name: DAG name or reference topic name. Required for dag, dag_spec, dag_profile, run, run_logs, and step_log. Optional for reference; defaults to authoring. Forbidden for references, dags, and runs.
- dagRunId: required for run, run_logs, and step_log. Forbidden for other targets.
- subRunId: optional child DAG-run ID for run and step_log. The name and dagRunId fields identify its root run.
- stepName: required for step_log. Forbidden for other targets.
- query: URL query string without a leading question mark. Allowed for dags, wiki, runs, run_logs, and step_log.
- workspace: all, default, or a workspace name. Optional for wiki, wiki_search, and dag_search; omitted means all accessible workspaces. Required for wiki_page, where all is not allowed.
- path: Wiki page path without .md. Required for wiki_page.
- search: search text. Required for wiki_search and dag_search.
- prefix: Wiki page path prefix without .md. Optional for wiki and wiki_search.
- cursor: opaque cursor returned by the same search target. Optional for wiki_search and dag_search.
- limit: maximum number of results from 1 to 50. Optional for wiki_search and dag_search; defaults to 20.
- uri: dagu:// resource URI for URI mode.

Targets:

- references lists built-in reference topics.
- reference reads one Markdown reference topic.
- dags lists DAGs.
- dag reads DAG details.
- dag_spec reads the current DAG YAML.
- dag_profile reads the DAG's configured profile, effective profile, and source. The source is dag, workspace, or none.
- dag_search searches DAG definition content and returns matching DAGs with line-level snippets. Continue with nextCursor while keeping search and workspace unchanged.
- wiki lists the Wiki tree or a flat page list. In tree mode, page and perPage select direct children of the workspace or prefix, and each returned directory includes its descendants. In flat mode, they select individual pages.
- wiki_page reads one Markdown Wiki page.
- wiki_search searches accessible Wiki pages in stable path order. Continue with nextCursor while keeping search, workspace, and prefix unchanged.
- runs lists DAG-runs.
- run reads one DAG-run. With subRunId, it reads the child run under the identified root run.
- run_logs reads scheduler and step log metadata.
- step_log reads stdout and stderr for one DAG-run step. With subRunId, it reads a child-run step. Use the stream, tail, head, offset, and limit query parameters to bound the returned lines; without positioning parameters the last 1000 lines are returned.

Query parameters:

- dags: page, perPage, name, labels, active, sort, order.
- wiki: page, perPage, flat, sort, order, prefix. perPage accepts 1 to 200.
- runs: name, dagRunId, status, fromDate, toDate, limit, cursor, labels. status may repeat.
- run_logs: tail. Values from 1 to 10000 are honored.
- step_log: tail, head, offset, limit, stream. tail, head, and limit accept 1 to 10000. Use at most one of tail, head, and offset; limit may be used alone or with offset. limit alone reads from the beginning of the log. stream is stdout or stderr; omitted means both.

Output:

- Successful result text is Dagu read completed.
- For dags, the result text also includes the returned data as indented JSON after a blank line.
- Structured output has target, data, references, and uri when the read has a canonical resource URI.
- Child run and step-log URIs use dagu://runs/{name}/{dagRunId}/sub/{subRunId} under the root run address.
- Reference URIs in references point to built-in guidance resources.
- Wiki list and search entries include canonical dagu://wiki/{workspace}/{path} URIs. Nested page paths are encoded as one URI segment.
- Wiki search output includes result snippets, modification times, hasMore, and nextCursor when another page is available.

Errors:

- invalid_tool_input for malformed target-mode input.
- invalid_resource_uri for malformed URI-mode input.
- unsupported_read_target for unknown target.
- unsupported_resource for unknown dagu:// family.
- resource_not_found, resource_unavailable, or internal_error for runtime failures.
- A resource_not_found error for a misspelled DAG name carries close existing names under details.didYouMean.`,
		},
		{
			topic:       "change-tool",
			uri:         "dagu://reference/change-tool",
			name:        "dagu_mcp_change_tool_reference",
			title:       "Dagu MCP change tool",
			description: "Detailed dagu_change input, output, and error reference.",
			text: `# dagu_change reference

Purpose: validate or apply DAG definition, DAG default profile, and Markdown Wiki changes.

Fields:

- mode: preview or apply. Defaults to preview.
- type: upsert_dag, rename_dag, delete_dag, set_dag_profile, clear_dag_profile, upsert_wiki_page, rename_wiki_page, or delete_wiki_page. Defaults to upsert_dag.
- name: DAG name. Required for DAG changes.
- spec: complete DAG YAML. Required for upsert_dag.
- newName: destination DAG name. Required for rename_dag.
- profile: selectable runtime profile name. Required for set_dag_profile and forbidden for clear_dag_profile.
- workspace: default or a named workspace. Required for Wiki changes; all is not allowed.
- path: Wiki page or directory path without .md. Required for Wiki changes.
- content: full Markdown content. Required for upsert_wiki_page; empty content is allowed.
- newPath: destination Wiki page or directory path. Required for rename_wiki_page.

Mode behavior:

- preview validates the requested operation and reads the required current state without writing.
- apply repeats validation and performs the requested operation through Dagu's API; Wiki operations remain workspace-aware.
- set_dag_profile and clear_dag_profile change server-side DAG settings, not YAML. The scheduler reads the change on its next minute tick.
- rename_dag changes the stored DAG identifier without rewriting the YAML name or historical runs.
- delete_dag removes the DAG definition using Dagu's existing deletion behavior.
- upsert_wiki_page creates a missing page or updates an existing page.
- rename_wiki_page and delete_wiki_page support pages and directories.

Output:

- Successful result text describes the previewed or applied change.
- DAG output has dagName, valid, and applied. Definition changes include dagUri while the target exists. Profile changes include profile, which is null when clearing.
- A valid upsert with profile-scoped schedules includes warning code profile_scoped_schedule_requires_default and the sorted profile names. Set a matching effective default with set_dag_profile when the schedule should be active.
- Wiki output has workspace, path, valid, applied, and wikiPageUri when the target is a page. docUri remains as a compatibility alias.

Errors:

- invalid_tool_input for missing or incompatible fields, invalid paths, unknown mode, unknown type, or malformed input.
- unauthorized when the caller cannot perform the requested write.
- resource_not_found when a rename or delete source does not exist, or when set_dag_profile names a missing profile. A misspelled DAG name carries close existing names under details.didYouMean.
- conflict when a DAG rename destination exists or a Wiki path conflicts with another file or directory.
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
- name: DAG name. Required for every action, including inline runs.
- spec: inline DAG YAML for start or enqueue with targetType=inline_spec; otherwise invalid.
- dagRunId: DAG-run identifier. Required for retry and stop. Optional override for start and enqueue.
- params: run parameters for start and enqueue, as a JSON object or a JSON-encoded string.
- queue: queue name for enqueue.
- singleton: singleton run flag for start and enqueue.
- noReuse: when true for start or enqueue, execute eligible build steps instead of reusing prior materializations.
- labels: labels for start and enqueue.
- stepName: optional step name for retry.
- includeDownstream: when true, retry the selected step and every reachable descendant. Requires stepName.
- wait: when true, wait for the identified run to reach a terminal state before returning. Requires a name that identifies the run.
- waitTimeoutSeconds: maximum seconds to wait, from 1 to 300. Defaults to 60.

Action behavior:

- start runs a stored DAG when targetType=dag and runs an inline spec when targetType=inline_spec.
- enqueue enqueues a stored DAG or inline spec.
- retry retries an existing DAG-run and may target a step with stepName, optionally including downstream steps.
- stop stops an existing DAG-run.
- Fields unsupported by an action are invalid. params, singleton, noReuse, and labels apply only to start and enqueue; queue only to enqueue; stepName and includeDownstream only to retry.
- With wait=true, the call returns once the run reaches a terminal state or the timeout elapses. On timeout the run keeps executing and the output has completed=false.

Output:

- Successful result text begins with Dagu execute action completed.
- Structured output has action, targetType, dagName, dagRunId, and references.
- When a run is identified, output includes runUri and logsUri. Without wait, it also includes subscribe guidance.
- With wait=true, output has completed plus the last observed status and statusLabel, and a completed run includes the run detail summary under run with per-step statuses and errors.

Errors:

- invalid_tool_input for missing fields, unknown action, unsupported targetType, or malformed input.
- unauthorized when the caller cannot perform the requested execution operation.
- resource_not_found when the named DAG or DAG-run does not exist. A misspelled stored-DAG name carries close existing names under details.didYouMean.
- conflict when a singleton run is already running or queued.
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

Clients without resource subscription support have two options: pass wait=true to dagu_execute to wait for the result inside the tool call, or poll dagu_read target=run with the same name and dagRunId.`,
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

func promptCreateWikiPage(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	workspace := strings.TrimSpace(req.Params.Arguments["workspace"])
	path := strings.TrimSpace(req.Params.Arguments["path"])
	goal := strings.TrimSpace(req.Params.Arguments["goal"])
	if goal == "" {
		goal = "Create the Wiki page requested by the user."
	}
	return promptResult(
		"Create a Dagu Wiki page",
		"Draft Markdown for this goal: "+goal+"\n\nCall dagu_change with mode=preview, type=upsert_wiki_page, workspace="+workspace+", path="+path+", and content set to the complete drafted Markdown. Apply only when the user wants the page written.",
	), nil
}

func promptEditWikiPage(_ context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
	workspace := strings.TrimSpace(req.Params.Arguments["workspace"])
	path := strings.TrimSpace(req.Params.Arguments["path"])
	change := strings.TrimSpace(req.Params.Arguments["change"])
	if change == "" {
		change = "Apply the requested Wiki page edit."
	}
	return promptResult(
		"Edit a Dagu Wiki page",
		"Read "+wikiPageURI(workspace, path)+", preserve unrelated content, and make only this change: "+change+"\n\nCall dagu_change with mode=preview, type=upsert_wiki_page, workspace="+workspace+", path="+path+", and content set to the complete edited Markdown. Apply only when the user wants the edit written.",
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
