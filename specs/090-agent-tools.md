# Spec 090: Agent Tools

## Status

Partially implemented.

Implemented: skills, the key `dags` and the tool map of `chat.completion` and
of `agent.run`. The tool map has the category `mcp`, and `agent.run` has the
category `builtin` too. The key `params`, the limits of the catalog and the
record of the catalog are implemented too. The named form of an `mcp` entry is implemented. It
names a MCP server entry of the AI page. See [Spec 095](095-mcp-servers.md).

Not implemented: tool steps (`tool_for`) and the approval of a tool call by a
person. The sections that define these stay as target behavior.

## Scope

This spec defines:

- the `skills` key of a step and the search of the `skills/` directory
- the use of skills by a step: the system message and the tool `load_skill`
  (the skill folders and the format are in [Spec 096](096-skills.md))
- the key `dags` of `chat.completion` and `agent.run`, and the keys `name` and
  `params` of an entry
- the tool map in `with.tools` of `chat.completion` and `agent.run`, and its
  category `mcp`
- the limits of the catalog
- tool steps, which a step declares with `tool_for`
- the effective catalog of a step and its record
- the category `builtin` in `with.tools` of `agent.run`

This spec does not define:

- the loop of tool calls. See [Spec 059: Chat Completion Action](059-chat-completion.md).
- the format of a skill, the skill folder, the API of the skills and the tool
  `read_skill_file`. See [Spec 096: Skills](096-skills.md).
- the tool bridge of a CLI agent, the mount of the skills of a CLI agent and
  the other keys of `agent.run`. See [Spec 091: Agent Node](091-agent-node.md).
- the action catalog of an agent DAG. See [Spec 032: Agent DAGs](032-agent-dag.md).
- the approval of a tool call by a person
- MCP servers that Dagu starts as a local process
- the MCP server entries of the AI page, their credentials and the MCP relay. See
  [Spec 095](095-mcp-servers.md).
- remote action packages as tools
- REST, Web UI, MCP server, authentication or authorization behavior

## Goal

A workflow author gives tools to a model in two places. The key `dags` lists the
DAGs that the model can call. The key `tools` is a map for the other sources, with
one category for each source. The author writes each tool one time. A CLI agent
also has its own tools. The author selects them in the category `builtin`. The
author gives instructions to the model in skills.

## Related Specs

- [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- [Spec 009: Step Reference](009-step-reference.md)
- [Spec 023: Preconditions](023-preconditions.md)
- [Spec 032: Agent DAGs](032-agent-dag.md)
- [Spec 059: Chat Completion Action](059-chat-completion.md)
- [Spec 091: Agent Node](091-agent-node.md)
- [Spec 096: Skills](096-skills.md)

## Terms

A tool is a function that the model can request. It has a name, a description
and a JSON Schema for its arguments.

The key `dags` is a list of entries. Each entry gives one DAG as a tool. The tool
map is the value of `with.tools`. A category is one key of the tool map. An entry
is one item of `dags`, or one item of the list of a category.

A skill is a folder that holds the instructions of one task. The file `SKILL.md`
of the folder defines the skill. See [Spec 096](096-skills.md).

A tool step is a step that has `tool_for`. It runs only when the model calls it.

The catalog is the list of tools that Dagu offers to the model in one step.

The effective catalog is the catalog after Dagu resolves the parameters and
reads the tools of each MCP server.

## Skills

A skill gives instructions to the model. The model reads the instructions only
when it needs them. This keeps the system message short.

[Spec 096: Skills](096-skills.md) defines the skill folders, the format of
`SKILL.md`, the rules that refuse a skill, the hash and the API. It also defines
the tools `load_skill` and `read_skill_file`, and the mount of a skill for a CLI
agent.
This section defines the `skills` key of a step and the part of the use that
belongs to the tool map.

### Skill files

The skills directory is named `skills`. Dagu looks for it in the directory of
the DAG file, then in each parent directory, at most 8 levels up. The search
stops at the DAGs directory of the Dagu configuration when the file is inside
it. The `skills`
directory of the DAGs directory is the last choice. It serves a DAG that has no
file. The `skills` directory at the root of the DAGs directory does not hold
workflows. Dagu does not list its files as DAGs.

The folder `skills/<name>/` defines the skill `<name>`. A skill that does not
follow the format of Spec 096 is refused: Dagu never loads it.

A skill is local. Dagu does not fetch a skill from a remote source. Dagu does
not run a script of a skill. Dagu does not follow a reference to another skill.

### Skill list

The `skills` key is a list of skill names. Each item is a string. An object
item is a validation error. A value that is not a list gives the same error on
both actions: `skills must be a list of skill names`. An empty list is valid.
The key is valid in the `with` of a `chat.completion` step, in the `with` of an
`agent.run` step, and in the `llm` block. A step names at most 20 skills. Dagu
counts the names before it reads a folder. A name that occurs twice counts
once. The estimated catalog of the skills is at most 2000 tokens. See
[Spec 096](096-skills.md).

Dagu reads the skill folders when it loads the DAG. The DAG that Dagu saves for
the run holds a snapshot of each skill: the body, the hash and the files. A DAG
with no `skills` key does not read the skills directory.

### Use at run time

Each skill of the list is loadable. Dagu adds the tool `load_skill` to
the catalog when the list is not empty. This applies to `chat.completion`.
[Spec 091](091-agent-node.md) defines the skills of a CLI agent: Dagu mounts
them as folders, and the step has no tool `load_skill`.

The tool `load_skill` has one input, `name`. The schema of `name` lists the
names of the skills. The tool returns the body of the skill. Dagu limits the
result with the limit `tool_result_max_bytes`. A name outside the schema gives
an error result to the model. When a skill has a text file in `references/` or
`scripts/`, Dagu adds the tool `read_skill_file` too. See
[Spec 096](096-skills.md).

Dagu adds the section `Skills` to the first system message of each request. It
puts a system message first when the request has none. The section lists each
skill as `- <name>: <description>`. It tells the model to call `load_skill`
with a name. The session record of the step does not hold this section.

A step with no skill gets no section and no `load_skill` tool.

The tools `load_skill` and `read_skill_file` take places in the catalog. They
count against `max_tools`. On a `chat.completion` step with a skill, a DAG tool
with the name `load_skill` or `read_skill_file` is a validation error.

Dagu does not evaluate value references in the body of a skill.

### Skill record

The record of the catalog shows `load_skill` as a tool with the source `skills`.

Dagu also saves the skills of the step with the session record. For each skill,
the record has the name, the description and the hash. The record has no body.

### Skill errors

`dagu validate` rejects these cases. It exits with a nonzero code. The error
names the skill, the file, the rule and the directories that Dagu searched.

- a skill that does not exist
- a skill that breaks a rule of the format. See [Spec 096](096-skills.md).
- more than 20 skills in one step, or a catalog above 2000 tokens
- an item of `skills` that is not a string

## The key dags

`with.dags` of a `chat.completion` step or of an `agent.run` step is a list. The
key is also valid in the `llm` block of a DAG. Each entry gives one DAG as a
tool. An entry has these keys.

| Key | Type | Rule |
| --- | --- | --- |
| `name` | string | Required. The name of a DAG. |
| `params` | map | Optional. See [params](#params). |

An unknown key in an entry, or a value that is not a list, is a validation error.
The error gives the path of the key. An empty list is valid.

To offer an action to a model, write a DAG with one step that runs the action.
Then list the DAG in `dags`. The inputs of the action are the params of the DAG.

The DAG is a DAG of the DAGs directory, or a DAG of the same file. A DAG that
Dagu cannot find fails the step when it starts. The error names the DAG. The
tool takes its schema from the declared
parameters of the DAG, as in Spec 059. The tool description is the
description of the DAG. For a DAG with no description, the tool description is
`Run the DAG <name>.`

A call runs the DAG as a child run. The arguments of the model and the fixed
inputs become its params, as one JSON object of strings. A value cannot add or
change another param. A call with an argument that the schema of the tool does
not show gets the error result `tool "<tool>" has no input "<name>"`, and no
child run starts. The result is the JSON text of the declared outputs of the
child run.

On an `agent.run` step, the tool bridge refuses some tool DAGs. Such a DAG has a
command text that refers to an input that the model sets, as
`${params.<name>}` or `${env.<name>}`. Dagu would put the value of the model in the command. The
error tells the author to write `"$<name>"`, which the shell reads as data. A
`chat.completion` step does not make this check.

### params

`params` is a map from an input name to a value. It is valid on an entry of
`dags`.

A value is fixed, unless it is an object whose only key is `from_ai`.
Dagu resolves a fixed value when it builds the catalog. The schema that the
model sees does not contain a fixed input. A model argument that names a
fixed input gives an error result, as for each input that the schema does not
show.

`from_ai: true` leaves the input to the model. `from_ai` can instead be an
object with the key `description`. That text replaces the description of the
input in the schema that the model sees.

An input that `params` does not name stays open for the model. A `params` key
that the schema does not declare fails the step when it starts.

## The tool map

`with.tools` of a `chat.completion` step or of an `agent.run` step is a map. It
has a closed schema. Each key is a category.

| Category | Type | Entries |
| --- | --- | --- |
| `mcp` | list | Each entry names a remote MCP server. See [mcp](#mcp). |
| `builtin` | object | `agent.run` only. The built-in tools of the CLI agent. See [The builtin category](#the-builtin-category). |

An unknown category, an unknown key in an entry, or a duplicate key is a
validation error. The error gives the path of the key. A string in place of
the map is a validation error. A list is valid only in the deprecated list form:
see [The list form of tools](#the-list-form-of-tools-deprecated).

`chat.completion` accepts the category `mcp`. A `builtin` category
on a `chat.completion` step is a validation error. `agent.run` accepts the two
categories. Its CLI agent gets the tools of `dags` through the tool bridge of
[Spec 091](091-agent-node.md#tool-bridge). It gets each `mcp` entry as an MCP
server of its config.

### The list form of tools (deprecated)

The list form of `tools` is deprecated. Use the key `dags`.

`tools` can be a list of DAG names. This is the form of `tools` of
[Spec 059](059-chat-completion.md). It stays valid in `with.tools` and in the
`llm` block of the DAG. It is not valid on an `agent.run` step: there, a list
is a validation error that names the key `dags`.

- Each name is an entry of `dags` with no `params`. The list
  `[lookup, refund]` is the same as `dags: [{name: lookup}, {name: refund}]`.
- The tool name is the name of the DAG that Dagu loads. If this DAG has no name,
  the tool name is the name in the list. The key `dags` gives the tool name from
  the key `name`.
- An item that is not the name of a DAG is a validation error. The error gives
  the path of the item.
- `tools` has one form. Do not put a map in a list.
- The list form and the key `dags` in the same step are a validation error. Use
  one of them.
- `dagu validate` accepts the list form and exits with code 0. It prints a
  warning on its output: "Deprecated DAG syntax: <path>.tools: The list form of
  `tools` is deprecated. Use the key dags." The text `<path>` is the path of the
  block, such as `steps[0].with`.
- The UI shows a note on a step with the list form. The editor writes the key
  `dags` when the author edits the step for the first time.

### mcp

An entry of `mcp` has these keys.

| Key | Type | Rule |
| --- | --- | --- |
| `name` | string | Required. Matches `^[a-z][a-z0-9-]*$`. The label of the server, or the name of a MCP server entry. The name of a named entry matches `^[a-z][a-z0-9-]{0,63}$`, the rule of a MCP server entry. |
| `url` | string | Required for an inline entry. The scheme MUST be `http` or `https`. A named entry has no `url`. |
| `headers` | map | Optional. Strings only. Valid with `url` only. |
| `allow` | list | Required. Not empty. Names of server tools. |

Two entries with the same `name` are a validation error. The name `dagu`
belongs to the tool bridge of Spec 091 and is a validation error.

An entry with no `url` is a named entry. It names a MCP server entry of the MCP
tab of the AI page. The URL and the credentials of the server stay in the
coordinator. The MCP traffic of the step goes through the relay of the gateway.
The key `allow` stays required. The coordinator refuses a `tools/call` for a tool
outside `allow`. A named entry with `headers` is a validation error.

An entry with `url` is an inline entry. It needs no gateway. A header value of an inline entry is visible to the agent in the MCP
config of the attempt. Use a named entry for a credential. See [Spec
095](095-mcp-servers.md#mcp-servers-with-credentials).

Only a `chat.completion` step and an `agent.run` step can have a named entry. The
`llm` block of a DAG cannot have one. A named entry also needs a worker that
announces the capability `ai_relay.v1`.

On an `agent.run` step, the CLI agent connects to the server, lists its tools
and calls them. Dagu writes the server in the MCP config of the attempt. The
rules below about the connection and the calls are for `chat.completion`. See
[Spec 091](091-agent-node.md#tool-bridge).

Dagu resolves `url` and the `headers` values when the step starts. Dagu does not
write a header value in a log or in a record.

Dagu connects with the MCP streamable HTTP transport and calls `tools/list`.
Each name in `allow` MUST be in the list of the server. A name that the server
does not list fails the step. The error names the tool and the URL. A failed
connection at this point fails the step.

For a named entry, Dagu connects to the relay of the step on `127.0.0.1`, with the
secret of the relay. The coordinator connects to the server with the credentials
of the entry. Dagu reads `tools/list` through the relay when the step starts.

Only the tools in `allow` join the catalog. Each one keeps the description and
the input schema that the server gives. Its tool name is
`<name>_<server tool name>`. Dagu replaces each character of the server tool
name outside `[A-Za-z0-9_-]` with `_`. The allow rule of a CLI agent keeps the
server tool name as the server gives it.

A call runs `tools/call` with the arguments of the model and the server tool
name. The result is the text content of the reply, joined with a line end. A
reply with `isError` set to true or a failed call gives an error result to the
model. The step does not fail.

Dagu limits the connection, the call `tools/list` and each call to 30 seconds.
The limit is fixed. An expired limit before the end of `tools/list` fails the
step. An expired limit
during a call gives an error result to the model.

## Tool names

Dagu computes the tool name of an entry of `dags` from its `name`. It replaces
each character outside `[A-Za-z0-9_-]` with `_`. The DAG `ops.notify` gives the
tool `ops_notify`. An author cannot give another tool name.

A tool name MUST NOT be longer than 64 characters. A longer name is a
validation error.

Two entries of `dags` that give the same tool name are a validation error. The
error names the two entries by their paths, such as `llm.dags[0]` and
`llm.dags[1]`. Two entries with the same `name` are the same error.

The tool names of an `mcp` entry are known when the step starts. A name that
another tool of the catalog has, or a name longer than 64 characters, fails
the step. The error names the two sources.

The name `load_skill` is a validation error when the step has a skill.

The name `respond` is a validation error when the step has `output_schema`.

## Tool steps

`tool_for` is a list of step references. See [Spec 009](009-step-reference.md).
It is valid only in a DAG of `type: graph`. Each target MUST be a
`chat.completion` step. A tool step serves each of its targets.

The plan never schedules a tool step. It runs only when a model calls it.

`depends` of a tool step lists the steps whose outputs the tool step reads.
Each target step also waits for these steps. A dependency that depends on
a target is a cycle and a validation error.

A step MUST NOT depend on a tool step. A tool step MUST NOT have `router`.

Dagu evaluates the `preconditions` of a tool step when it builds the catalog.
Dagu does not offer a tool step that has an unmet precondition.

The tool name follows the rules of [Spec 032](032-agent-dag.md): it comes from
the `id`, or the `name` when there is no `id`. The tool description is the
`description` of the step. When the step has none, it is the description of
its custom action. Otherwise it is a generated sentence that names the step.

The schema is as follows.

- For a step that runs a custom action, it is the `input_schema` of the
  action. Inputs that the `with` of the step supplies are not in it.
- For a step that runs `dag.run`, it is derived as in
  [Spec 032](032-agent-dag.md).
- For any other step, the tool has no arguments.

A call resets the step and runs it. One tool step runs at most 5 times in one
DAG run. A later call is refused as a tool error.

The result for the model has the status, the declared outputs, and a tail of
stdout and stderr. Dagu limits the result to `tool_result_max_bytes` bytes. A
truncated result stays valid UTF-8.

A failed tool step is an observation for the model. It does not fail the DAG
run by itself. When the target steps succeed and a tool step stays failed, the
DAG run ends `partially succeeded`.

A tool step that the model never called, or that Dagu did not offer, ends
`skipped`.

## Limits

| Limit | Default | Rule |
| --- | --- | --- |
| `max_tools` | 64 | At least 1. Limits the effective catalog. |
| `tool_result_max_bytes` | 524288 | At least 0. Limits one tool result sent to the model. 0 means no limit. |

The keys `agent_tools.max_tools` and `agent_tools.tool_result_max_bytes` of the Dagu
configuration set these limits for each step of the process. A
`chat.completion` step can also set `max_tools` and `tool_result_max_bytes` in
its `with` or in the `llm` block. A value on the step wins for that step. An
`agent.run` step has no key for these limits: the keys of the configuration
apply to the catalog of its tool bridge. A value outside the range is a validation error, or an error at the start of Dagu for a
key of the configuration.

The block `agent_tools` is file only. If the variable `DAGU_AGENT_TOOLS` exists in the environment,
Dagu stops at start. The error names the variable and the key, and never shows the value:

```text
config: the environment variable DAGU_AGENT_TOOLS is not read: set the block agent_tools in the configuration file
```

The limit of an MCP connection, of `tools/list` and of a call is 30 seconds. It
is fixed.

## The effective catalog

Dagu builds the effective catalog when the step starts. It does these actions
in order:

1. Take the entries of `dags`, then the entries of `mcp`, each in the order that
   the author wrote them.
2. Resolve `params`, `url` and `headers`.
3. Read the tools of each `mcp` entry that stays.
4. Compute the tool names and check them.
5. Check the number of tools.

The catalog has at most `max_tools` tools. A larger catalog fails the step. The
error gives the number and the limit.

Dagu offers the same catalog on each request of the step. It does not build
the catalog again during the step.

A call to a name outside the catalog gets the result `tool "<name>" not found`.
The loop continues, as in [Spec 059](059-chat-completion.md).

### Record

Dagu saves the effective catalog with the session record of the step. For
each tool, the record has the name, the description, the schema and the
source. The source is one of `dag:<name>`, `mcp:<name>` or `skills`.

The record of an `agent.run` step holds the tools of its tool bridge: the
entries of `dags`. The CLI lists the tools of an `mcp` entry, so
the record does not hold them.

The record has no header values and no fixed input values.

## The builtin category

The category `builtin` of `with.tools` of an `agent.run` step selects the
built-in tools of the CLI agent. It is valid on `agent.run` only. The CLI agent
runs these tools itself. The key `dags` reaches the CLI agent through the tool
bridge. The category `mcp` of the same map reaches it as MCP servers of its
config.

The keys of `builtin` are `read`, `write`, `edit`, `search` and `shell`. Any
other key is a validation error. The value of the first four is `true`. The
value of `shell` is an object with `allow` (a non-empty list of command
patterns) and `deny` (a list of command patterns).

A command pattern is a command prefix. A final ` *` means any arguments. The
pattern `git diff *` matches `git diff --stat`. A pattern that is `*` or empty
is a validation error. The value `shell: true` is a validation error.

Dagu passes each tool and each pattern to the CLI. The CLI decides if a command
matches a pattern. Write a pattern with no shell operator. A `deny` pattern wins
over an `allow` pattern.

[Spec 091](091-agent-node.md) defines the flags of the provider `claude` that
Dagu builds from this category.

## Custom actions that wrap a chat step

A custom action template MAY be a `chat.completion` step with `dags` or `tools`.
Dagu evaluates its tools in the same way as for a direct step.

## UI contract

The editor shows three kinds of tool: a DAG tool, an MCP server and a built-in
tool. Each kind has one icon, one short label and one color. The label is `DAG`,
`MCP` or `Built-in`. Each place of the editor uses the same icon, label and color
for a kind. The places are the canvas, the Tools section of the step form, the
list of choices and the tool panel. The
tooltip of a tool names its kind and its source: the DAG name or the host of the
MCP URL.

The Tools card of a step on the canvas lists the tools in groups, in the order
DAG, MCP and built-in. A group has a heading when the step has more than
one kind of tool. A built-in row shows the number of shell patterns that it
allows.

- A click on a tool row selects the tool entry. The panel of the tool opens at
  the right. The selection of a tool is the selection of its step.
- The add row of the card, and the add button of the Tools section, open the
  list of choices at the right. The list has one group for each kind that the
  step takes. A chat step takes DAG tools and MCP servers. An agent step takes
  DAG tools, MCP servers and built-in tools.
- When the user picks a choice, the editor writes the new entry in the YAML. The
  panel of the new tool then replaces the list of choices. The focus is on the
  first field of the panel.
- The Tools section of the step form is a compact list. A row has the icon and
  the label of the kind, the name and a delete button. A click on a row opens
  the panel of the tool.
- The panel of a tool has the same parts as the step form. It has a title bar
  with the icon, the name and a way back to the step. It has sections with their
  issue counts, the same fields and a YAML section for the entry. A DAG tool has the sections
  General and Parameters. An MCP server has the sections
  General, Connection and Allowed tools. The built-in tools of a step are one
  entry with one section.
- The delete button of the panel removes the entry and closes the panel. Escape,
  the close button and the selection of another step close the panel.
- The run view shows the same panel, read-only. The calls of each tool in the
  run stay on the rows of the card.

### Skills in the editor

The Skills slot of a step on the canvas follows the same rules as the Tools
slot. It has one of two forms. A step with no skill shows a small round "+"
node. A step with one skill or more shows a Skills card with one row for each
skill and an add row.

- The "+" node and the add row open the list of skills at the right. The list
  shows the skills of the `skills/` directory. A pick writes the name in
  `with.skills`.
- A click on a skill row opens the Skills panel of the step at the right. The
  panel lists the names of `with.skills`, with the description of each skill
  and a link to its page. A skill with an error shows the reason of the server.
- The tab Skills of the AI page lists the skills of the server. It creates,
  changes, downloads and deletes a skill with its files. [Spec 096](096-skills.md)
  defines the contract. [Spec 093](093-ai-endpoints.md) defines the AI page.
- A skill with an error is shown as not usable, with the reason.

The Tools slot has the same two forms. It is a small round "+" node when the
step has no tool, and a card when it has one tool or more. The Model slot always
shows its own node.

Top to bottom, the slots sit beside the step: the Model and the Skills on its
left, the Tools on its right. Left to right, the slots sit under the step. The
link of a slot leaves the side of the step that faces its node. The box of
the step grows with its nodes. It is wider top to bottom and taller left to
right. No node touches the previous step, the next step or their links.

The user can drag each node of a slot (Model, Prompt, Skills, Tools or a "+"
node) alone. The step and the other nodes stay in place. The link follows the
node. When the node goes to the other side of the step, the link leaves the side
that faces it. When the user drags the step, its nodes move with it and keep
their offsets. A new layout drops the moves: a change of direction, a change of
the shape of the graph or the align button. The editor does not save the moves
in the YAML or in the browser. A frozen canvas moves no node. A click that ends
a drag does not open a node.

## Errors

`dagu validate` rejects these cases. It exits with a nonzero code. The error
gives the path of the key.

- an unknown category, an unknown key or a duplicate key in the tool map or in
  an entry. The error names the key and the valid keys.
- a value of `tools` that is neither a map nor a list of DAG names
- a value of `dags`, or of a category, that is not a list
- an item of the deprecated list form of `tools` that is not the name of a DAG
- an entry with no `name`, or an `mcp` entry with no `allow`
- an `mcp` entry with `headers` and no `url`, or with a `url` that is empty
- an `mcp` entry with no `url` in the `llm` block of a DAG
- two entries that give the same tool name, a tool name above 64 characters,
  or two `mcp` entries with the same `name`
- a `builtin` category on a `chat.completion` step
- an `mcp` entry named `dagu`
- a skill that does not exist, or a skill that breaks the format. See [Skill errors](#skill-errors).
- the list form of `tools` on an `agent.run` step
- the list form of `tools` and the key `dags` in the same step
- a `shell` entry of `builtin` with no `allow`, or a pattern that matches all
  commands
- a `builtin` category that selects no tool, or a key of `builtin` with a value
  other than `true`
- a `max_tools` below 1, or a negative `tool_result_max_bytes`
- `tool_for` outside a graph DAG, or with a target that is not `chat.completion`
- a step that depends on a tool step
- a cycle that `tool_for` and `depends` make

These cases fail the step at run time.

- a DAG of an entry of `dags` that Dagu cannot find
- a `params` key that the schema does not declare
- an `mcp` tool in `allow` that the server does not list
- a failed connection to an MCP server during the build of the catalog
- an MCP tool name that another tool has, or that is above 64 characters
- a named `mcp` entry whose server entry does not exist, or that the DAG may not
  use. The reasons are `mcp_server_unknown` and `mcp_server_forbidden`.
- a catalog of more than `max_tools` tools

## Examples

A skill, `skills/refund-policy/SKILL.md`:

```markdown
---
name: refund-policy
description: Rules for a refund
---
# Refund policy

Refund an order in 30 days. Ask a manager for a larger amount.
```

A step that uses the skill:

```yaml
steps:
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: local-model
      base_url: http://localhost:8080
      prompt: Refund 12 euros for c-42
      skills:
        - refund-policy
      dags:
        - name: refund
```

The model sees the DAG tool `refund` and the tool `load_skill`.

A DAG with the key `dags`, the category `mcp` and fixed inputs:

```yaml
type: graph
params:
  - CUSTOMER_ID
  - MCP_URL
steps:
  - id: triage
    action: chat.completion
    with:
      provider: local
      model: local-model
      base_url: http://localhost:8080
      prompt: Handle the new ticket.
      dags:
        - name: refund
          params:
            CUSTOMER_ID: ${CUSTOMER_ID}
            AMOUNT:
              from_ai:
                description: Amount to refund, in euros
        - name: ops_notify
          params:
            text: { from_ai: { description: Message to post } }
      tools:
        mcp:
          - name: docs
            url: ${MCP_URL}
            allow: [search]
---
name: ops_notify
description: Post a message on the ops channel
params:
  - text
steps:
  - action: log.write
    with:
      message: ${text}
```

The model sees the tools `refund`, `ops_notify` and `docs_search`. The DAG
`ops_notify` wraps the action `log.write`.

A tool entry with an unknown key fails validation. An entry has a closed schema:

```yaml
dags:
  - name: refund
    retries: 3
```

```text
field 'llm.dags[0].retries': unknown key "retries" in an entry of dags; valid keys are name, params (value: 3)
```

An unknown key of the tool map, of an entry, of `builtin`, of `shell` or of
`from_ai` gives the same error. The places are `the tool map`, `an entry of
dags`, `an entry of mcp`, `builtin`, `shell`, `an input with from_ai` and
`from_ai`.

A duplicate tool name fails validation:

```yaml
dags:
  - name: ops_notify
  - name: ops.notify
```

```text
field 'llm.dags[1]': tool name "ops_notify" is also the tool name of llm.dags[0] (value: ops.notify)
```

The path of a key starts with `llm` on a `chat.completion` step, and with `with`
on an `agent.run` step. The paths in these examples are the paths of a
`chat.completion` step.

A tool step:

```yaml
type: graph
steps:
  - id: discover
    run: ./discover.sh
    output: BASE_URL

  - id: lookup
    depends: discover
    tool_for: [triage]
    description: Search the knowledge base
    action: http.request
    with:
      url: ${BASE_URL}/search

  - id: triage
    depends: discover
    action: chat.completion
    with:
      provider: local
      model: local-model
      base_url: http://localhost:8080
      prompt: Answer the question with the knowledge base.
```

## Conformance

Black-box tests are in `conformance/spec090_agent_tools/`. They use a local
OpenAI-compatible endpoint and a local MCP server. They need no external
service. The tests of [Spec 091](091-agent-node.md) cover the keys `dags` and
`tools` of `agent.run` and its tool bridge.

| Section | Tests |
| --- | --- |
| Skill files, Skill list | `TestSkillsSectionAndLoadSkillTool`, `TestSkillFoldersAreNotWorkflows` and the skill rows of `TestValidateRejectsBadSkills` in `skills_test.go`. |
| Use at run time | `TestSkillsSectionAndLoadSkillTool`, `TestLoadSkillReturnsTheBody`, and Spec 096, `TestChatCompletionUsesLoadSkillAndReadSkillFile`. |
| Skill errors | `TestValidateRejectsBadSkills`: a missing skill, a key of the front matter, a name that differs from the folder, an object item. The limits of 20 skills and 2000 tokens: Spec 096, `TestCatalogCaps`. |
| The key dags, The tool map | `TestValidateRejectsBadToolMaps`: an unknown category, the key `dags` inside `tools`, the key `dags` with the list form of `tools`, an unknown key on an entry, an entry with no name, a `builtin` category on `chat.completion`, an `mcp` entry with no `allow`, named `dagu`, or twice. |
| The list form of tools | `TestValidateAcceptsTheDeprecatedToolsList`, and Spec 059, `TestToolIterationLimit` with the list form. |
| The key dags, at run time | `TestDAGWithoutDescriptionGetsADefault`, `TestFixedInputIsHiddenAndApplied`, `TestHiddenInputArgumentIsAnErrorResult`. The refusal of a tool DAG of `agent.run` that puts an input of the model in its command: Go tests of `agenttools`. |
| mcp | `TestMCPToolsAreOfferedAndCalled`, `TestMCPToolNameClashFailsTheStep`. The named form: Spec 095, `TestChatNamedMCPLocalMode`. |
| params | `TestFixedInputIsHiddenAndApplied`, `TestHiddenInputArgumentIsAnErrorResult`. A `params` key that the schema does not declare: not covered. |
| Tool names | `TestValidateRejectsADuplicateToolName`, the long name and the name `load_skill` of `TestValidateRejectsBadToolMaps`, `TestMCPToolNameClashFailsTheStep`. |
| Limits | `TestMaxToolsFailsTheStep`, `TestToolResultLimit`, and `limits_test.go` for the block `agent_tools`. The fixed limit of 30 seconds: not covered, because a test would wait 30 seconds. |
| The effective catalog, Record | `TestFixedInputIsHiddenAndApplied` and the record checks of Spec 091, `TestBridgeOffersTheCatalogAndRunsTheCalls`. |
| The builtin category | Spec 091, `TestClaudeFlags` and the `builtin` rows of its `TestValidateRejectsWhatTheProviderCannotRun`. |
| Tool steps | Not covered: not implemented. |
| UI contract | Not covered here. The tests of the UI cover it. |
