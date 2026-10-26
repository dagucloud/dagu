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
When a run waits for a person, dagu_execute action=approve, reject, or push_back decides the waiting step that dagu_read target=run lists.
MCP Apps hosts can render run-related dagu_read and dagu_execute results in Dagu's interactive run inspector.
To follow a run to completion, pass wait=true to dagu_execute, or read the returned dagu://runs/... resource, or subscribe to it to receive a resource update notification when the run reaches a terminal state or stops at a waiting checkpoint.`

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
- For data transformation between steps (parsing, filtering, reshaping, regex extraction, computing values), use js.run instead of python -c, Python or Node heredocs, or awk, sed, and jq pipelines in run. It needs no interpreter on the host or worker, its script is passed as written so ${...} and $ need no escaping, and dagu_change preview reports script syntax errors with the line. Keep run for CLIs and for work that needs the network, files, environment variables, or a library. A single jq path is fine in jq.filter.
- DAG YAML does not contain the server-side default runtime profile. A schedule profile is an activation filter: the schedule runs only when it matches the DAG's effective default profile. Read it with dagu_read target=dag_profile. Set or clear it with dagu_change type=set_dag_profile or clear_dag_profile.
- A DAG profile change is visible to the scheduler on its next minute tick. Time spent under a non-matching profile is not scheduler downtime, so catch-up does not replay those schedule slots.
- Declared value outputs use a step-level outputs field and write records to DAGU_OUTPUT_FILE. Later steps read them as ${steps.step_id.outputs.name}. Dagu does not read stdout for them, so the command can keep logging to stdout.
- String-form output: NAME captures the whole trimmed stdout, log lines included, into the env variable NAME.
- type: build is for local workflows that transform stable regular-file inputs into reusable file outputs.
- A build path step declares named inputs entries with path and at most one outputs entry with path. Only host command or shell steps without containers may declare build paths. Matching canonical producer-output and consumer-input paths infer dependencies, and each output path must have one producer.
- Inside the owning step, ${inputs.name} is the final input path and ${outputs.name} is a fresh per-attempt staging path. Write the result to ${outputs.name}; after commit or reuse, dependent steps read the final path as ${steps.step_id.outputs.name}. stdout, stderr, and artifact stream destinations must not target declared build paths.
- Potentially reusable producers expose downstream data through ${steps.step_id.outputs.name}; attempt-only stdout, stderr, exit-code, output, and outputs references are invalid because reuse does not recreate them. Path-output steps cannot use continue_on.mark_success.
- Build workflows are local-only. Distributed execution requests are rejected because materialization fencing is not shared across workers.
- human.task defines a processless root-DAG operator step with an explicit id, required with.prompt, and optional flat scalar with.form JSON Schema. Omit form for acknowledgement-only tasks.
- Human task form properties that are required or have defaults become ${steps.step_id.outputs.name} values after completion. Do not declare outputs on a human.task step; additionalProperties defaults to false.
- Optional with.push_back lets the operator send a human task back instead of completing it: rewind_to names a step the task depends on, and optional form is a flat feedback schema whose additionalProperties must stay false. A push-back re-runs that step and every step that depends on it, directly or transitively, with the feedback as push-back context (DAG_PUSHBACK, DAG_PUSHBACK_ITERATION, and one environment variable per feedback property), then opens the task again. Feedback is limited to 16 KiB as JSON. Human tasks have no reject operation.
- Human tasks cannot run in sub-DAGs, lifecycle handlers, or foreach.steps. Root DAGs containing human tasks may run locally or on distributed workers selected by DAG-level worker_selector. Over MCP, dagu_execute action=approve completes a waiting human task with inputs as its form values and action=push_back pushes it back with inputs as its feedback; locally, dagu human-task complete and dagu human-task push-back do the same.
- type: agent lets the configured llm pick which step runs next instead of a dependency graph. It requires llm and a non-empty tasks list, where each task has a name and a description stating when it is finished. depends and router steps are rejected, and the reserved names __agent__ and ask_user cannot be used for steps.
- Use context references for run metadata, such as ${context.dag.name}, ${context.run.id}, and ${context.paths.artifacts_dir}.
- git.worktree.add creates or reuses a linked worktree in the local repository containing the step working_dir. Omit branch for a Dagu-generated branch, or set branch with create_branch: true and optional base to create a named branch.
- git.worktree.add publishes path, branch, commit, worktree_created, and branch_created. Read them as ${steps.step_id.outputs.field}; do not declare outputs on the step.
- git.worktree.remove accepts branch, path, or both. Set force only to discard worktree changes. Set delete_branch to remove a merged branch, and add force_delete_branch to remove an unmerged branch.
- harness.run can use root-level container or step-level container. A step-level container takes precedence for that step.
- Containerized harness runs support Dagu CLI providers and custom providers that pass the prompt as an argument or flag. They do not support provider=builtin, with.stdin, or custom prompt_mode=stdin.
- Docker or Podman is selected by the Dagu service process through DAGU_CONTAINER_RUNTIME and optional DAGU_PODMAN_HOST, not by a DAG YAML runtime field.
- chat.completion with a step-level output_schema answers through a forced respond tool whose parameters are the schema. The schema needs type: object and at least one property; unlisted properties are dropped, the rest is validated, and each listed property becomes ${steps.step_id.outputs.name}, so do not declare outputs on the step. web_search cannot be combined with it. Leave a field out of required when the input may lack it; a required field makes the model invent a value.
- browser.extract and browser.run drive a local Chrome using the DAG-level llm block or with.llm, which replaces it. browser.run takes with.do, a list where each item sets one of goto, act, upload, extract, expect, wait, screenshot, or ask, plus optional when and timeout.
- Browser steps publish the top-level properties of each extract schema as ${steps.step_id.outputs.name}; do not declare outputs on the step. A field for the address a link points to needs format: uri and a place in required; without the format it gets the link's element id, such as 0-11, and without required some models leave it out. Pass secrets through with.variables and reference them as %name% in act instructions; never write a secret into an instruction. An instruction containing a declared secret value of four or more characters fails the step, and only such values are masked, so shorter secrets are neither caught nor masked. A %name% that is not a variable or an earlier ask.as fails validation.
- Browser expect and when take a statement the model judges or a fixed check object with one of text, selector, or url; prefer fixed checks for repeatable results. A fixed when reads the page once unless within is set. allowed_domains matches exact hosts and *.example.com matches subdomains only; the browser runtime applies it to HTTP(S) requests, so list CDN hosts too, and Dagu fails the step when the page URL leaves it. A headless browser sends the user agent of the same Chrome with a window, so showing the window does not change how a site sees it; a step that fails on a page the site refused, with a bot check or a 403 or 429, ends its error with the refusal.
- For a site that requires a TLS client certificate, set with.browser.client_certificates: entries with hosts (a host, *.example.com, or *) and pfx_file with passphrase from a secret such as ${env.CERT_PASSPHRASE}, or cert_file and key_file in PEM. The host config's browser.client_certificates applies to every browser on that host, after the step's. While certificates are presented, other HTTPS requests go through Dagu too, so a site that asks for a certificate no entry names fails at once saying so instead of hanging on a system prompt; browser.guard_client_certificates in the host config turns that off, or on without certificates.
- with.browser.write_gate: rehearse runs a browser step without saving anything: every write the page sends (any request but GET, HEAD, or OPTIONS) is blocked and reported, and the step stops at the first one and ends partially succeeded, leaving write-gate.json in its artifacts with what it would have sent. The host config's browser.write_gate is a floor a step cannot loosen.
- An upload operation gives local files to a file input: upload: {files, into}. files is a path or glob pattern relative to the step's working directory, a list of them, or a value an earlier step published as a JSON list of paths, of objects with a path, or of mail.search emails, whose attachments saved with save_attachments: true it uploads; each file is at most 50 MB. into names the file input, or the button or drop area that opens the file picker, in plain words; the model finds it once and later runs replay it. Leave into out when the page has only one file input; then no model is asked. A single-file input given several files fails, so upload them one at a time. The model never sees the files.
- Browser acts, and uploads with into, of a step that succeeded are replayed from a per-host recording without the model (with.ai: on_miss, the default, asks the model when a replay fails and repairs the recording); every_run asks the model every run and still records; never replays only, fails the step with the reason when there is no recording or a replay fails, and needs no llm when nothing in the step calls the model; a step-level never refuses extract and a statement expect or when, which need the model. ai on an act or an upload replaces with.ai; cache: false means every_run; set ai or cache, not both.
- When you can run the dagu CLI on the Dagu host, build a browser step for a page you have not seen in a browser session instead of guessing its fields or addresses: dagu browser session open, then one do per operation with the outline it reports, then export, which writes the step with its acts recorded for its first run.
- computer.extract and computer.run operate the desktop of a macOS or Windows worker's logged-in user session, using the DAG-level llm block or with.llm. computer.run takes with.do, a list where each item sets one of launch, act, extract, expect, wait, screenshot, or ask, plus optional when and timeout. Route computer DAGs to such workers with DAG-level worker_selector, or for one run with workerSelector on the start and enqueue API requests; other systems fail the step.
- Computer steps use the provider's native computer-use tool for anthropic, openai, and gemini, and plain function tools with any vision model for other providers or with mode: generic. Pass secrets through with.variables as %name%; the model sees only the placeholder, which is typed as the value. Typed values can still appear in later screenshots. Extract schema properties become ${steps.step_id.outputs.name}. Successful acts are replayed from a per-host cache while the screens match (with.ai: on_miss, the default; every_run asks the model every run; never replays only, fails on a miss with the reason, and needs no llm when nothing in the step calls the model; a statement expect or when needs the model, so a step-level never refuses statements, while exact checks are allowed; a successful extract binds each scalar field (string, number, integer, boolean) to the element its value was read from, and a table field (an array of objects of scalars) to a grid, table, or list read by column header up to with.max_rows (default 10000; extract.max_rows for one extract), so under never it reads the bound elements with no model and fails only when a field's element or a column header is missing, and an extract under never whose schema lists a plain object or a free array is refused); a learned step travels across devices by pinning it into the step keyed by system (windows or macos): an act takes recorded (a list of click/double_click/right_click selectors, check/uncheck selectors that leave a checkbox in that state and click it only when it is not, type {into,text}, key chord, scroll {on,dy}, or wait) run ahead of the device's own recording, and an extract takes fields (each output a selector, or {container,columns} for a table) read with no model; expect and when also take exact checks that read the window's elements without a model on macOS and 64-bit Windows: {text} for text a visible element shows, {element: {role, name | id | near, in, nth, enabled}} for a selector that must match exactly one element, where enabled: false holds only while the element is grayed out, or {window} for the title in front, each with within; an exact expect keeps looking until within or the operation timeout, an exact when keeps looking for two seconds; a check on the step's data, {value, equals} or {value, not_equals}, compares a parameter or %name% variable as written, decided at once with no screen and no model, so a part only some runs need is an operation of its own with when on its value, such as when: {value: "${params.SECOND_CONTACT}", not_equals: ""} to fill a second contact only when one is given, or an act holding check and another holding uncheck, each with when: {value: "%same%", equals: "yes"} or "no", since pinned steps inside one act take no condition; a replay acts on the elements the model's actions landed on and matches the window by its application, not its title, so a moved window, another display, or a title that changes with the document's contents still replays, and with.find_within (default 10s; act.find_within for one act) bounds the wait for an element; with.input: direct (or act.input) has a replayed element act itself through its automation pattern instead of the pointer and keys, invoking a button, toggling a checkbox, selecting a list item, opening a combo box, or setting a field's value at once when the recording showed the typed text became the value, with the screen as the fallback for an element without the pattern; screen stays the default since an application that reacts to each key or the pointer alone sees nothing; with.handle lists screens that may come up at any point, such as a confirmation an application shows only sometimes: each {name, when: an exact check without within, recorded: pinned steps keyed by system} runs whenever the step looks at the screen (an element, a check, a table, a replayed screen, or the screenshot a model sees) or acts without an element, when its check holds in the front window, so a run that never meets the screen waits nothing for it, and a screen still there after 20 runs of its handler fails the step; a dialog that holds the window a replayed element is in (a macOS sheet or modal alert, or a Windows dialog that disables its owner) and that no with.handle handler clears stops the replay there: the step never raises its window over the dialog, so the element is a miss whose reason quotes the dialog's text, while a dialog of another application holds nothing and the window is raised as usual; a check that fails while a dialog is in front names it too; dagu computer check verifies the desktop and permissions. Steps wait until nobody has touched the desktop for with.idle (default 5s; 0 turns it off) before sending input. A replay by pixels compares only the application window the turn was recorded on and clicks relative to its corner, so a moved window or a change elsewhere on the screen still replays, while a window of another size is a miss. with.maximize (default true; act.maximize or extract.maximize for one operation) maximizes the window in front before an operation looks at the screen, filling the screen between the menu bar and the Dock on macOS; dialogs, windows of fixed size, and the windows of apps named in DAGU_DESKTOP_PROTECTED_APPS are left as they are, and a step never operates a protected app.
- mail.search and mail.organize read and organize a mailbox over IMAP, or through the Gmail API for a Google account signed in with OAuth. The mailbox field names an entry of the DAG-level or base-config mail_accounts map, keyed by email address; provider google or microsoft fills in the servers, and credentials are password or oauth (google_refresh or microsoft_refresh with a refresh token; microsoft_refresh accepts optional scopes). A provider google account with oauth uses the Gmail API instead, takes no imap, smtp, or username, and treats folders as Gmail labels; its grant needs https://www.googleapis.com/auth/gmail.modify or https://mail.google.com/, and the Google Cloud project of its client_id must have the Gmail API enabled. mail.send with mailbox sends through that account; add in_reply_to (a found email's id) to reply: to defaults to the email's reply address, subject to Re: and its subject, and the reply is threaded under the email.
- mail.search publishes messages (oldest first; each has id, message_id, folder, from_name, from_address, to, cc, subject, date, unread, flagged, text, attachments), count, and truncated; do not declare outputs on the step. Searching never marks email read.
- mail.organize takes emails as an ID, an email from mail.search, an {id, move_to} object, or a list, with mark (read, unread, flagged, unflagged) and/or move (folder, archive, trash). To process each email once, loop over messages with foreach and mark each email read inside the loop after its work, so a failed email stays unread for the next run.
- xlsx.read reads an .xlsx sheet without a spreadsheet application: with.path, optional sheet, range (A2:F, Sheet!A2:F, a named range, or a table), header (true, false, a row number, or [3, 4] for two header rows), columns (names or {name: alias}), types ({amount: number, due: date}; a pinned column also reads Japanese forms: ￥123,000, 12万3,500円, ▲1,000, (1,000), 10%, 税込1,000, kanji numerals, 2026年10月3日, 令和8年10月3日, R8.10.3, 2026年10月 as its first day, 10月3日 in the current year, 午後2時30分 after a date, and はい/いいえ, 有/無, 済/未, ○/× under boolean), where ({Status: ""} or {Status: {ne: Done}}), max_rows, and skip_hidden (true leaves out rows hidden by a filter or by hand). It publishes rows (objects keyed by header, each with _row), count, headers, sheet, range, warnings, and truncated; do not declare outputs on the step. Numbers stay numbers, dates become ISO text, and text keeps leading zeros. Read a workbook first with dagu_read target=workbook to learn its sheets, headers, and types.
- xlsx.write and xlsx.append write rows (usually ${steps.<id>.outputs.rows}; pass columns such as ${steps.<id>.outputs.headers} to keep column order) or an input json, jsonl, or csv file to a sheet; xlsx.info and xlsx.list_sheets describe a workbook. Writers publish path, sheet, changes ({rows_updated, rows_appended, columns_added, cells_changed}), dry_run, and warnings; dry_run: true reports without saving, wait_for_unlock: 5m retries a workbook another program holds open, and artifact: true keeps a copy with the run. xlsx.append takes output to write the result to another workbook and leave path untouched; an existing output is replaced, so a fixed output inside a foreach keeps only the last iteration's rows.
- xlsx.update_rows writes results back to the rows they came from: key names the column that identifies a row (or _row), rows carry the key and the fields to write, and set maps sheet columns to row fields ({Status: status}) or literals ({Reviewed: {value: yes}}); without set every field other than the key and _row goes to the column of the same name. missing is fail, skip, or append. Before any cell is written the step checks that the key column is still in the header row and that a row addressed by _row still holds its key, and refuses when either changed; with key: _row there is no key to compare, so only the row number's range is checked and a sheet whose rows moved is not detected. A set column the sheet lacks is added at the right of the header. For "do this for each row and mark it done", read with where: {Status: ""}, foreach over the rows with a collect that carries the key and the result fields, and pass that collected output to update_rows. output writes the updated workbook to another file and leaves path untouched.
- xlsx.validate checks a sheet against rules before a workflow acts on it: required (columns the header must have), not_blank, unique, types ({Amount: number}), allowed ({Status: [Open, Done]}); at least one. It publishes ok, problems (each with code missing_column, blank, type, duplicate, or not_allowed, plus sheet, cell, row, column, message), count, rows, headers, sheet, range, warnings, truncated. on_problem: warn (default) succeeds with the problems published; fail fails the step after listing them and publishes nothing. To stop and ask someone, add a human.task step with a precondition on ${steps.<id>.outputs.count} (expected "num:>0") and continue_on: {skipped: true}.
- xlsx.write_cells fills a template: cells maps addresses (B2, Sheet1!B2, or a defined name for one cell) to a value, {value: v, type: t}, {formula: "=SUM(B2:B9)"}, or null to clear; styles are kept. merge lists ranges to merge first ([A1:D1]); a range over a filled cell or another merged region fails the step, and changes.merged counts the ranges merged. output writes the result to a new workbook and leaves path untouched; the workbook at path must exist. xlsx.sheet takes operation add, copy, rename, or delete with sheet, to, if_exists (fail, skip, replace), missing (fail, skip), position, and publishes sheets; use if_exists: skip for reruns. xlsx.convert exports a sheet to the csv, json, or jsonl file named by output (format from the extension or format), with every row and for csv encoding (utf-8, utf-8-bom, shift_jis) and delimiter; the reverse is xlsx.write with input, whose csv takes the same encoding and delimiter. dagu dry warns when an xlsx step's workbook, sheet, or column does not exist.
- xlsx.extract reads fields out of a sheet laid out as a form rather than a table, such as a quote whose layout differs by supplier: with.instruction says what to find and with.schema (type: object) names the fields as properties, each optionally typed (string, number, integer, boolean, or string with format date or date-time) and described. A model is shown the sheet's cells (address, kind, bold or fill, text cut to 200 characters; send_values: false hides non-text values) and answers only the address of each field's cell or null; the engine then reads the typed value from that cell, so no value is invented. It needs the DAG-level llm block or with.llm. Outputs are each property, cells (property to the Sheet!B7 it was read from), sheet, warnings, and source (model or cache); do not declare outputs on the step. The model also names the cells that are labels, and the answered addresses are cached by the sheet's shape with a digest of each label, so a layout seen before makes no model request as long as the instruction, the schema, and the labels are unchanged; a form of the same template with a box left blank is read from the cache too, while a label renamed in place asks again. cache: false asks every run, and dagu xlsx cache clear <dag> drops the cache. A pinned type reads Japanese text as xlsx.read does: yen amounts, kanji numerals, ▲ negatives, percent, price tags, kanji and era dates with 午前/午後 times, and yes/no marks under boolean.
- js.run runs with.script as a JavaScript function body in an embedded sandbox; return a value to publish it (a string as-is, other values as JSON, undefined nothing). The script text is used as written, so ${...} inside it is not resolved: pass workflow values through with.input (any YAML value, bound as input) or with.input_file; a string input that is a JSON object or array is parsed by default (format: text keeps it a string). The sandbox is ES2022 (classes, destructuring, generators, optional chaining, BigInt, regex lookbehind) with console (to stderr), URL, URLSearchParams, and await for promises that resolve synchronously; no ES modules, require, fetch, filesystem, process, timers, Intl, structuredClone, or TextEncoder. with.timeout takes seconds or a duration and defaults to the step timeout, or 60s without one. Use node-script@v1 when real Node.js is required.`,
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
- dagu_execute: start, enqueue, retry, or stop a DAG-run, or approve, reject, or push back a step that waits for a person.

Detailed tool references:

- dagu://reference/read-tool: dagu_read inputs, targets, URI mode, query parameters, outputs, and errors.
- dagu://reference/change-tool: dagu_change preview and apply contract for DAG YAML, DAG profiles, and Wiki changes.
- dagu://reference/execute-tool: dagu_execute start, enqueue, retry, stop, approve, reject, and push_back contract.
- dagu://reference/apps: interactive run inspector behavior for MCP Apps hosts.

Use dagu_execute action=retry with name and dagRunId for retry. Use action=stop with name and dagRunId for stop. Use action=start or action=enqueue with targetType=dag for a stored DAG, or targetType=inline_spec with spec for an ad hoc run. Use action=approve, reject, or push_back with name, dagRunId, stepName, and the attemptId read from the run to decide a step waiting for a person.`,
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

- target: required in target mode. Values are references, reference, dags, dag, dag_spec, dag_profile, dag_search, wiki, wiki_page, wiki_search, workbook, runs, run, run_logs, step_log, foreach_items, and foreach_item.
- name: DAG name or reference topic name. Required for dag, dag_spec, dag_profile, run, run_logs, step_log, foreach_items, and foreach_item. Optional for reference; defaults to authoring. Forbidden for references, dags, and runs.
- dagRunId: required for run, run_logs, step_log, foreach_items, and foreach_item. Forbidden for other targets.
- subRunId: optional child DAG-run ID for run, step_log, foreach_items, and foreach_item. The name and dagRunId fields identify its root run.
- stepName: required for step_log, foreach_items, and foreach_item. Forbidden for other targets.
- item: foreach item path. Required for foreach_item; optional for step_log together with bodyStepName. A top-level item is its index; an item of a nested foreach is the foreachParent its body step reports, a dot, and the index, such as 2.inner.0.
- bodyStepName: body step inside a foreach item for step_log. Requires item. Forbidden for other targets.
- query: URL query string without a leading question mark. Allowed for dags, wiki, runs, run_logs, step_log, and foreach_items.
- workspace: all, default, or a workspace name. Optional for wiki, wiki_search, and dag_search; omitted means all accessible workspaces. Required for wiki_page, where all is not allowed.
- path: Wiki page path without .md, required for wiki_page; or a workbook file path on the server, required for workbook.
- password: optional for workbook, the password of a protected workbook. Forbidden for other targets. It is not recorded in the audit log.
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
- workbook inspects an .xlsx file on the server: for each sheet its used range, detected data block, header row, headers, whether it is hidden, how many of its rows are hidden by a filter or by hand, column types, a profile of each column over up to 5000 rows (filled, blank, distinct, the values when a few repeat, min and max, and odd cells that do not read as the type, such as 未定 in a number column), row count, tables, and five typed sample rows; plus named ranges and the date system. The path is any file the server process can read and is recorded in the audit log. An optional password opens a protected workbook and is not recorded. Use the result to write xlsx.read with the right sheet, range, columns, and types.
- runs lists DAG-runs.
- run reads one DAG-run. With subRunId, it reads the child run under the identified root run.
- run_logs reads scheduler and step log metadata.
- step_log reads stdout and stderr for one DAG-run step. With subRunId, it reads a child-run step. With item and bodyStepName, it reads one body step of a foreach item. Use the stream, tail, head, offset, and limit query parameters to bound the returned lines; without positioning parameters the last 1000 lines are returned.
- foreach_items lists the items a foreach step expanded to, with total, counts per status, and one page of items sorted failed first. A run's step entry links here through foreachUri. Use status to keep one status, page and perPage to page, and parent to list the items of a nested foreach body step.
- foreach_item reads one foreach item: its status, error, and each body step with status, timestamps, error, hasStdout, hasStderr, and for a nested foreach body step the foreachParent to pass to foreach_items.

Query parameters:

- dags: page, perPage, name, labels, active, sort, order. perPage accepts 1 to 200.
- wiki: page, perPage, flat, sort, order, prefix. perPage accepts 1 to 200.
- runs: name, dagRunId, status, fromDate, toDate, limit, cursor, labels. status may repeat.
- run_logs: tail. Values from 1 to 10000 are honored.
- step_log: tail, head, offset, limit, stream. tail, head, and limit accept 1 to 10000. Use at most one of tail, head, and offset; limit may be used alone or with offset. limit alone reads from the beginning of the log. stream is stdout or stderr; omitted means both.
- foreach_items: parent, status, page, perPage. status is one of not_started, running, succeeded, failed, or aborted. perPage accepts 1 to 500 and defaults to 50.

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

Purpose: control DAG execution through start, enqueue, retry, and stop actions, and decide steps that wait for a person through approve, reject, and push_back.

Fields:

- action: required. Values are start, enqueue, retry, stop, approve, reject, and push_back.
- targetType: dag, inline_spec, or run. Defaults to run for retry, stop, approve, reject, and push_back, inline_spec when spec is present, otherwise dag.
- name: DAG name. Required for every action, including inline runs.
- spec: inline DAG YAML for start or enqueue with targetType=inline_spec; otherwise invalid.
- dagRunId: DAG-run identifier. Required for retry, stop, approve, reject, and push_back. Optional override for start and enqueue.
- params: run parameters for start and enqueue, as a JSON object or a JSON-encoded string.
- queue: queue name for enqueue.
- singleton: singleton run flag for start and enqueue.
- noReuse: when true for start or enqueue, execute eligible build steps instead of reusing prior materializations.
- labels: labels for start and enqueue.
- stepName: optional step name for retry. Required for approve, reject, and push_back: the waiting step to decide.
- includeDownstream: when true, retry the selected step and every reachable descendant. Requires stepName.
- subDAGRunId: for approve, reject, and push_back, the sub-DAG run that owns the waiting step when it runs inside a sub-DAG run of dagRunId.
- inputs: object for approve and push_back. An approval gate takes string values for the names its input list declares; a human task takes its form values on approve and its push-back feedback on push_back.
- reason: optional reason for reject.
- attemptId: for approve, reject, and push_back, the attemptId from the run details the decision is based on. When the run has moved to another attempt, the decision is refused with conflict.
- wait: when true, wait for the identified run to reach a terminal state or a waiting checkpoint before returning. Requires a name that identifies the run.
- waitTimeoutSeconds: maximum seconds to wait, from 1 to 300. Defaults to 60.

Action behavior:

- start runs a stored DAG when targetType=dag and runs an inline spec when targetType=inline_spec.
- enqueue enqueues a stored DAG or inline spec.
- retry retries an existing DAG-run and may target a step with stepName, optionally including downstream steps.
- stop stops an existing DAG-run.
- approve, reject, and push_back decide a step that dagu_read target=run reports as waiting with approval or humanTask. For an approval gate, approve lets the step continue with inputs, reject rejects it with reason, and push_back sends it back for re-execution with inputs as feedback. For a human task, approve completes it and push_back pushes it back; human tasks cannot be rejected.
- Fields unsupported by an action are invalid. params, singleton, noReuse, and labels apply only to start and enqueue; queue only to enqueue; includeDownstream only to retry; stepName only to retry, approve, reject, and push_back; subDAGRunId and attemptId only to approve, reject, and push_back; inputs only to approve and push_back; reason only to reject.
- With wait=true, the call returns once the run reaches a terminal state, stops at a waiting checkpoint, or the timeout elapses. A run stopped at a checkpoint has completed=false and reports each waiting step under run.steps as humanTask or approval with its prompt; resolve them to resume the run. On timeout the run keeps executing and the output has completed=false.

Output:

- Successful result text begins with Dagu execute action completed.
- Structured output has action, targetType, dagName, dagRunId, and references.
- When a run is identified, output includes runUri and logsUri. Without wait, it also includes subscribe guidance.
- approve, reject, and push_back output also has stepName, subDAGRunId when given, and, without wait, the run status and statusLabel observed after the decision.
- With wait=true, output has completed plus the last observed status and statusLabel, and a completed run includes the run detail summary under run with per-step statuses and errors.

Errors:

- invalid_tool_input for missing fields, unknown action, unsupported targetType, or malformed input, when reject targets a human task, or when inputs do not fit the waiting step.
- unauthorized when the caller cannot perform the requested execution operation.
- resource_not_found when the named DAG, DAG-run, or step does not exist. A misspelled stored-DAG name carries close existing names under details.didYouMean.
- conflict when a singleton run is already running or queued, when the step to decide is not waiting for a person, or when attemptId differs from the run's current attempt, which details.attemptId reports.
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

Clients that support MCP resource subscriptions can subscribe to the dagu://runs/{name}/{dagRunId} resource. Dagu sends a resource update notification when the run reaches a terminal state: success, failed, aborted, partial success, or rejected. Dagu also notifies when the run stops at a waiting checkpoint, and keeps watching so the later terminal state is notified too.

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
