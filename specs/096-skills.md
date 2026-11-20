# Spec 096: Skills

## Status

Implemented.

The API, the loader, `chat.completion`, `agent.run`, the ZIP download and the editor of the UI follow
this spec. Items that are not built are in [Open items](#open-items).

## Scope

This spec defines:

- the format of a skill: the front matter of `SKILL.md`, the limits, and the rules that Dagu enforces
- the folder of a skill: the layout, the files, and the limits of the files
- the files as the only source of truth of the skills, the hash of a skill, and the preconditions of a write
- the skill routes of the REST API, and the download of skills as a ZIP file
- the use of a skill by `chat.completion` (the tools `load_skill` and `read_skill_file`) and by `agent.run`
- the transfer of a skill to a worker, and the record of the version that a run received
- the contract of the UI for skills
- `dagu validate` for skills

This spec does not define:

- the `skills` key of a step, the system message of the catalog and the tool map. See [Spec 090](090-agent-tools.md).
- the other keys of `agent.run`, the CLI flags and the sandbox of the CLI. See [Spec 091](091-agent-node.md).
- the agent worker, the coordinator and the workspace bundle of a run. See [Spec 092](092-agent-workers.md) and [Spec 035](035-file-dependencies.md).
- the upload of a ZIP file, a command that checks a skill, and the other items of [Open items](#open-items)

## Goal

A skill gives instructions to a model. A skill that follows the Agent Skills standard works in Dagu
without a change. A skill that does not follow the standard never reaches a model: Dagu refuses it
and tells the author why. The skill is a folder of files. A person, a git repository or the UI can
change it. Several Dagu instances can share the folder, and a run keeps the exact version that it
received.

## Related Specs

- [Spec 035: File Dependencies](035-file-dependencies.md)
- [Spec 090: Agent Tools](090-agent-tools.md)
- [Spec 091: Agent Node](091-agent-node.md)
- [Spec 092: Agent Workers](092-agent-workers.md)
- [Spec 093: AI Endpoints](093-ai-endpoints.md)

## Terms

A skill is a folder `skills/<name>/` of the skills directory. The file `SKILL.md` of the folder
defines it.

The skills directory is the directory `skills` that Dagu finds next to a DAG file, as
[Spec 090](090-agent-tools.md) defines. The API uses `skills` in the DAGs directory.

A refused skill is a skill that breaks one rule of an error in this spec. Dagu never loads a
refused skill. The API and the UI list it with the state ERROR.

An error is a broken rule that refuses the skill. A warning is a hint. A warning does not refuse
the skill.

The hash of a skill identifies one version of the skill. See [The hash](#the-hash).

The standard is the Agent Skills specification at `https://agentskills.io/specification`. This spec
follows it as it stood on 2026-11-05.

## The format

`SKILL.md` has a front matter block and a Markdown body. The first line of the file MUST be `---`.
The front matter is the YAML text up to the next line that holds only `---`. The body is the text
after that line. Dagu accepts the line ends LF and CRLF.

The front matter has a closed schema. A key that is not in the table is an error, and the message
names the key. This includes the extension keys of Claude Code, such as `argument-hint`,
`when_to_use` and `model`. A key that occurs twice is an error. Custom data goes in `metadata`.

| Key | Type | Rule |
| --- | --- | --- |
| `name` | string | Required. 1 to 64 characters. Unicode lower case letters, digits and `-`. No `-` at the start or at the end. No `--`. MUST be equal to the folder name. |
| `description` | string | Required. 1 to 1024 characters after Dagu replaces each run of white space with one space. MUST NOT contain an XML tag. |
| `license` | string | Optional. 1 to 128 characters. |
| `compatibility` | string | Optional. 1 to 500 characters. |
| `metadata` | map | Optional. A map from string keys to string values. A number or a boolean value is an error: quote it. |
| `allowed-tools` | string | Optional. A string of tool names, separated by spaces. Informational. See [Security](#security). |

The body MUST NOT be empty. The body MUST NOT be larger than 65536 bytes (64 KiB).

### The rules

Each rule has an identifier. The message of an issue gives the identifier, the file and the reason.

| Rule | Severity | Meaning |
| --- | --- | --- |
| `skill-md-missing` | error | The folder has no file `SKILL.md`. |
| `not-utf8` | error | `SKILL.md` or a file of `references/` or `scripts/` is not valid UTF-8. |
| `front-matter-start` | error | The file does not start with a `---` line. |
| `front-matter-end` | error | The front matter has no closing `---` line. |
| `front-matter-yaml` | error | The front matter is not valid YAML. |
| `front-matter-duplicate-key` | error | A key occurs twice. |
| `front-matter-key` | error | A key is not in the table. |
| `name-missing`, `name-type` | error | `name` is absent or is not a string. |
| `name-length` | error | The name has fewer than 1 or more than 64 characters. |
| `name-charset` | error | The name has a character that is not a lower case letter, a digit or `-`. |
| `name-hyphen-edge` | error | The name starts or ends with `-`. |
| `name-hyphen-double` | error | The name holds `--`. |
| `name-folder` | error | The name differs from the folder name. |
| `description-missing`, `description-type` | error | `description` is absent, empty or not a string. |
| `description-length` | error | The description has more than 1024 characters. |
| `description-xml` | error | The description holds an XML tag. |
| `license`, `compatibility`, `metadata`, `allowed-tools` | error | The value breaks the rule of its key. |
| `body-empty` | error | The body is empty. |
| `body-too-large` | error | The body is larger than 64 KiB. |
| `body-lines` | warning | The body has more than 500 lines. |
| `body-tokens` | warning | The body is above about 5000 tokens. The estimate is the size in bytes divided by 3.7. |
| `description-short` | warning | The description has fewer than 20 characters. |
| `reference-depth` | warning | A file of `references/` lies deeper than one folder level. |
| `file-too-large-to-read` | warning | A file of `references/` or `scripts/` is larger than 262144 bytes. The tool `read_skill_file` cannot return it. |

The folder rules are in [The folder](#the-folder).

### Source of the rules

These rules come from the standard:

- the table of keys
- the limits of `name`: 1 to 64 characters, no `--`, equal to the folder
- the limits of `description` (1 to 1024) and of `compatibility` (1 to 500)
- the type of `metadata` and the string form of `allowed-tools`
- the 500 lines and the 5000 tokens

Dagu makes the standard limits errors, except the 500 lines and the 5000 tokens, which stay warnings.

These rules are Dagu rules. The standard does not state them in its specification page:

- The 64 KiB limit of the body.
- The ban on an XML tag in the description. The Anthropic API variant of the standard states it.
- The limit of 128 characters for `license`. The standard asks for a short value.
- The ban on every key that the standard does not list. The standard does not forbid other files or keys, and many clients ignore them.
- The warning for a description of fewer than 20 characters.

## The folder

A skill folder holds `SKILL.md` at its root and three optional directories: `references/`,
`assets/` and `scripts/`. Each directory holds files at any depth. The standard asks that
references stay one level deep. A deeper file is a warning, not an error.

Any other file or directory at the root of the folder is an error. The message names the entry.

| Rule | Severity | Meaning |
| --- | --- | --- |
| `root-entry` | error | The root holds an entry other than `SKILL.md`, `references/`, `assets/` or `scripts/`. |
| `symlink` | error | An entry is a symbolic link. Dagu never follows a link. |
| `hardlink` | error | A regular file, also `SKILL.md`, has more than one hard link. |
| `special-file` | error | An entry is not a regular file or a directory. |
| `hidden-file` | error | A name starts with `.`. Only the file `.gitkeep` below the root is allowed. |
| `path` | error | A name has a control character, a backslash or invalid UTF-8, or a path is longer than 512 bytes. |
| `file-too-large` | error | A file is larger than 4194304 bytes (4 MiB). |
| `total-too-large` | error | The files of the skill are larger than 16777216 bytes (16 MiB) in total. |
| `too-many-files` | error | The skill has more than 256 files, or the folder has more than 1024 entries (files and directories). |
| `folder-link` | error | The folder of the skill is a symbolic link. |
| `changing` | error | The folder changed while Dagu read it, and it did not settle. |
| `unreadable` | error | Dagu cannot read a file or a folder of the skill, for example because of its permissions. |

Dagu stores a file as bytes. `SKILL.md`, the files of `references/` and the files of `scripts/` MUST
be valid UTF-8 text. A file of `assets/` can be text or binary.

A name that ends with `.tmp.` and digits is the temporary file of an atomic write. Dagu ignores
such a file. A skill cannot hold a file with such a name: a write of such a path is refused with the
rule `path`. An empty directory is not part of the
skill: it has no file, so the hash and the ZIP file ignore it.

The limits are constants of this spec. They are not configuration keys. The limits of the
workspace bundle of [Spec 035](035-file-dependencies.md) also apply, to the whole bundle of a run. They
are 64 MiB compressed, 256 MiB extracted and 8192 entries.

## Files and HA

### Source of truth

The folder `skills/<name>/` in the DAGs directory is the only source of truth of a skill. Dagu
keeps no state of a skill in the memory of a process. Every operation reads the folder from the disk
when it needs it: the API, the loader of a DAG and the dispatch of a run.

Dagu supports several instances that share the same DAGs directory (a shared volume, or git-based
sync) and use the scheduler lock for failover. The instances need no coordination of skills beyond
the shared files and the hash precondition of a write. Dagu does not depend on a file watcher.

A read can use a cache in the process. The key of the cache is the path of the folder and the data of
each entry: the path, the kind, the mode, the size and the time of the last change. The cache gives a result only
when every key matches. It does not give a result when the newest change is younger than 2 seconds.
It keeps at most 128 folders. The cache is a function of the disk. It never decides alone.

### Atomic writes

Every write of a file is atomic: Dagu writes a temporary file next to the target, makes it durable
and renames it. A reader sees the old file or the new file, never a part of it.

A write or a delete never goes through a symbolic link. This holds for the skill folder, for each
folder below it and for the file. Dagu checks each part of the path before the write and again
before the rename. It refuses the request with the status 400 and the rule `folder-link` or
`symlink`, and it changes nothing. A delete can remove a link at the end of the path, and never its
target. A client that sends the hash of such a skill gets the same refusal.

A change of several files is several writes. A client writes the files of `references/`,
`assets/` and `scripts/` first and `SKILL.md` last. Each write returns the new hash.

A reader tolerates a skill that a writer changes at the same time. It lists the folder, reads the files and lists the
folder again. When the two lists differ, the reader starts again, at most 4 times. A skill that
does not settle is refused with the rule `changing`. A reader validates what it reads. A skill with
a new file and the old `SKILL.md` is a complete skill. A folder with no `SKILL.md` is refused.

### The hash

The hash of a skill is the SHA-256 of a text, in lower case hexadecimal (64 characters). The text
has one line for each regular file of the folder. The lines are in the byte order of the path, from
the lowest:

```text
<sha256 of the file in hexadecimal><two spaces><path><line feed>
```

This is the output of `sha256sum` for the files. The path is relative to the skill folder. It uses
`/` as separator. It has no `./` prefix. The line feed is `\n`, also on Windows. The text has no
other line.

The hash does not depend on the order of the directory entries, the time stamps or the modes of the
files. A change of one byte, of one name, or the addition or the removal of a file changes it.

A skill with an error has a hash too, so that a client can repair it. An entry that is not a regular
file (a link, a special file) gives the line `<64 zeros><two spaces><path>`. A file larger than 4
MiB is not read: its digest is the SHA-256 of the text `oversize:<size>`. A skill folder that is a
link has one line with an empty path and the SHA-256 of the text `symlink:<target>`. So its hash is
not the hash of an empty skill.

### Preconditions and conflicts

Every write of the API carries the precondition of the client:

- `If-Match: <hash>` applies the write only when the skill still has this hash. The value can be in quotes,
  and a prefix `W/` is ignored. A value that is not 64 hexadecimal characters gives 400.
- `If-None-Match: *` applies the write only when the skill folder does not exist. This creates a skill.
  Another value of `If-None-Match` gives 400. Only `PUT /skills/{name}` accepts this header.

A write with neither header is refused with the HTTP status 428. A write with a stale hash or with
`If-None-Match: *` on an existing skill gives 409 with the code `conflict` and the current hash
(`hash`). The write changes nothing. The check and the write are one step under the lock of the
skill. So two writers that hold the same hash never both succeed, also on two Dagu instances.

The lock is a directory lock (`dirlock`) at `skills/.locks/<name>/`. It is held for the time of one
write. A holder that crashes gives the lock free after 30 seconds. A writer waits at most 15 seconds
and then gets 409. The directory `.locks` is hidden: the listing ignores it. A repository that syncs
the DAGs directory SHOULD ignore `.locks/`.

### The bundle

The coordinator can dispatch a DAG that uses skills. Then it puts every file of each skill in the
workspace bundle of the run, at the same path under `skills/`. The coordinator then reads each skill
again. When the hash differs from the hash that the loader read, the dispatch fails and the error
names the skill. The client retries. The worker never gets a mix of two versions.

The worker reads the skills from the bundle. It never reads the skills directory of the
coordinator. The worker computes the same hash as the coordinator, because the hash depends on the
content only.

### The record of a run

The loader keeps a snapshot of each skill in the DAG that Dagu saves for the run. The snapshot has
the name, the description, the body, the hash and the list of files with their size and SHA-256.
The snapshot holds the content of each text file. It holds no content of a binary file.

The record of the step (`skillDefinitions`) lists the name, the description and the hash of each
skill. A person can reproduce a run with the hash, and the snapshot gives the text that the model
read.

An edit of the folder after the start of a run does not change what the run received. A run that
another instance continues after a failover (a queued run, a retry) uses the snapshot. `read_skill_file`
reads the snapshot. `agent.run` writes the text files from the snapshot. It reads a binary file from
the folder and checks its SHA-256. When the file changed, the step fails with the reason
`skills_mount` and the message names the skill and the file. Dagu never gives other bytes to a run.

### Secrets

A skill holds no secret. Dagu does not evaluate a value reference in a skill. A person who has
read access to the DAGs directory, to the API or to the ZIP file can read every file of a skill.

## The API

The routes are under `/api/v1`. They use camelCase names. All routes accept the `remoteNode` query
parameter. A read needs a signed-in user when authentication is on. A write, a delete and a file
write need the developer role or above, and the permission `write_dags`.

A JSON body of a skill route has a closed schema. An unknown field gives the status 400. A JSON body
above 8 MiB gives the status 413. The error never quotes a value of the body.

| Route | Meaning |
| --- | --- |
| `GET /skills` | List the folders of the skills directory, sorted by name. |
| `GET /skills/{name}` | Read one skill: the raw `SKILL.md`, the hash, the files, the errors and the warnings. |
| `PUT /skills/{name}` | Create or replace `SKILL.md`. Body `{content}`. |
| `DELETE /skills/{name}` | Delete the whole folder with every file. |
| `GET /skills/{name}/files?path=` | Stream one file, exactly as stored. The header `ETag` holds the hash. |
| `PUT /skills/{name}/files?path=` | Write one file of `references/`, `assets/` or `scripts/`. The body holds the bytes. |
| `DELETE /skills/{name}/files?path=` | Delete one file, or an entry that breaks the layout. |
| `GET /skills/{name}/download` | Download one skill as a ZIP file. |
| `POST /skills/download` | Download several skills as one ZIP file. Body `{names}`. |

The listing contains every folder of the skills directory, also a folder with no `SKILL.md` and a
folder that breaks the format. It does not contain a plain file, a link or a hidden folder. A read of a
skill folder that is a link gives the error `folder-link`. A skill with an
error is a skill with the state ERROR.

`SkillSummary` has `name`, `description`, `hash`, `valid`, `errors`, `warnings`, `fileCount`,
`totalBytes`, `bodyLines` and `bodyTokens`. `SkillDetail` has `name`, `content`, `hash`, `errors`,
`warnings`, `files`, the values of `license`, `compatibility`, `allowedTools` and `metadata`, and the
`SkillSummary` of the skill in `summary`. A `SkillIssue` has `severity`, `rule`,
`file` and `message`. A `SkillFile` has `path`, `size`, `sha256` and `text`.

A write returns `SkillWriteResult`: the new `hash`, the `errors`, the `warnings` and the `files`.
The client uses the new hash for its next write. `DELETE /skills/{name}` returns 204 with no body.
`DELETE /skills/{name}/files` returns 200 with `SkillWriteResult`. `PUT /skills/{name}/files` refuses
the path `SKILL.md` with 400, because `SKILL.md` has its own route. `GET /skills/{name}/files` gives 409
when the skill changed while the server read the file.

A write validates the file that it writes. `PUT /skills/{name}` refuses a `SKILL.md` that breaks a
rule of the file. A write of another file refuses a path that breaks the layout, and a file above the
limits. It also refuses a file of `references/` or `scripts/` that is not UTF-8. The refusal is 400 with
the issues in `issues`. A write can leave the skill with another error, for example a file at the root
that a person added. Such a write succeeds, and the result lists the error.

The status codes:

| Status | Code | Case |
| --- | --- | --- |
| 400 | `bad_request` | The name, the path or the content breaks a rule. The body lists the issues. |
| 404 | `not_found` | The skill or the file does not exist. |
| 409 | `conflict` | The precondition fails. The body has the current `hash`. |
| 413 | `payload_too_large` | The body of `PUT /skills/{name}/files` is larger than 4 MiB. A `SKILL.md` above 4 MiB gives 400 with the rule `file-too-large`. |
| 428 | `bad_request` | The request has no `If-Match` and no `If-None-Match`. |

A path MUST be relative, with `/` separators, and without an empty, `.` or `..` part. A route never
reads or writes a file outside the folder of the skill. The name of a skill in a route MUST be a
folder name: no separator, no leading dot, at most 255 bytes. A new skill MUST follow the name rule.

The API writes an audit event for each change: `skill_update`, `skill_delete`, `skill_file_write`,
`skill_file_delete` and `skill_download`.

## Download as ZIP

`GET /skills/{name}/download` downloads one skill. `POST /skills/download` downloads up to 100
skills. The request is a POST with a JSON body, because a list of names can be long for a URL. The
rules for roles are the same as for the read of a skill.

The response has the type `application/zip`. The header `Content-Disposition` is an attachment. The
file name is `<name>.zip` for one skill with an ASCII name. For another name, it is `skill.zip` with a
`filename*` value. For several skills, it is `skills-<date>.zip`, with the date in UTC.

The ZIP file has one top-level folder `<name>/` for each skill. The folder holds the files of the
skill, exactly as stored, also a file `.gitkeep`. Dagu sorts the skills by name and the files by
path in byte order. It removes a name that occurs twice. Every entry has the time 1980-01-01 00:00:00
UTC, the mode 0644 and the method deflate. The same skills give the same bytes.

A skill with an error can be downloaded, so that the owner can repair it. The ZIP file marks
nothing. An unknown name gives 404 and no ZIP file: Dagu checks every name before it sends the first
byte. A request for more than 100 skills, or for files above 256 MiB in total, gives 400. Dagu
streams the ZIP file. A file that changes during the stream stops the stream with an error.

## Use in chat.completion

The `skills` key of a step lists the skills. [Spec 090](090-agent-tools.md) defines the key. A
step can name at most 20 skills. The estimated size of the catalog MUST NOT be above 2000 tokens
(the size in bytes of the lines `- <name>: <description>`, divided by 3.7). An excess is a
validation error that gives the numbers. Dagu never cuts the catalog. These limits are constants of
this spec, because the standard recommends a small catalog and the limit is a safety, not a
preference. They are not configuration keys.

The system message lists each skill as `- <name>: <description>`. The catalog and the tool schemas
do not change during a step, so the cache of the provider stays valid.

The tool `load_skill(name)` returns the body of the skill with the front matter removed. The result
is in the tags that the standard recommends for a client:

```text
<skill_content name="guide">
...the body...

Relative paths in this skill are relative to the folder of the skill. Read a file with the tool `read_skill_file`.
<skill_resources>
  <file>references/rules.md</file>
  <file>scripts/check.sh</file>
</skill_resources>
</skill_content>
```

The list holds the paths of the files that the model can read: the files of `references/` and
`scripts/`. It holds at most 100 paths. After 100 paths, a line `<!-- <n> more files not listed -->`
gives the number of the other files. A second call of `load_skill` for the same skill in the same
step returns a note that the skill is loaded. It does not return the body again.

The tool `read_skill_file(name, path)` returns the text of a file of `references/` or `scripts/`.
Dagu adds the tool only when a skill of the step has such a file. The `path` MUST be one of the files
of the snapshot. A path outside the skill, a file of `assets/`, `SKILL.md` and a file above 262144
bytes give an error result. The error result of a path that the skill does not have tells the model
which files it can read. The model
cannot read a binary file. The tool counts against `max_tools`. A tool of the map MUST NOT have the
name `load_skill` or `read_skill_file`.

Dagu does not run a script and does not fetch a file from a URL. A skill is data.

## Use in agent.run

[Spec 091](091-agent-node.md) defines the provider `claude`. Dagu mounts the folder of each skill
under `<agent directory>/.claude/skills/<name>/`. The mount holds every file of the skill. It holds
the original bytes of `SKILL.md`, which Dagu does not rewrite, and the files of `references/`,
`assets/` and `scripts/`. Every file has the mode 0400 and every directory the mode 0700. The restricted user
reads the mount and cannot write it. Dagu gives no file the execute permission and never runs a
script. Dagu passes the agent directory with `--add-dir` and adds the tool `Skill` to the tool lists.

The 20 skills limit applies to `agent.run` too. Claude Code builds its own listing of the skills.

## The UI contract

The Skills tab of the AI page follows these rules.

- The list has one row for each skill. Each row has a checkbox. A checkbox in the header selects all rows. The button Download is on when one row or more is selected. It downloads the ZIP file of the selection. The file name comes from the response header.
- A skill with an error shows the red state "Error", with the number of errors when it has more than one. The row tooltip lists at most 8 issues, each with its file and its message. The validation panel adds the rule of each issue. A skill with warnings only shows the amber state "Warning".
- The editor has a header with the name, the state and a Download action for the open skill. The token estimate is under the header.
- The editor has a file tree for `references/`, `assets/` and `scripts/`. A text file opens in the same editor, with the language of its extension. A binary file shows its name, its size and a download link. The user can add, rename and delete a file. A delete asks for a confirmation.
- The validation panel lists the errors (red) and the warnings (amber) of the whole skill.
- The editor shows "about N tokens" for the body, with markers at 5000 tokens and 500 lines. The number is approximate.
- Every save sends the hash that the editor read. A conflict shows a message, keeps the draft of the user and offers to reload the skill. The UI never overwrites a newer version without the user.
- A new skill is created with `If-None-Match: *`. The template of a new skill is a valid skill.
- The pickers of the canvas show a skill with an error as not usable, with the reason.
- Every text of the UI exists in English, in Chinese (zh-CN) and in Japanese.

## Validation

`dagu validate` loads the DAG. A DAG that names a skill reads the folder of the skill. A refused
skill makes the validation fail with a non-zero exit code. The message names the skill, each file
and each rule:

```text
skill "bad" is refused because it is not in the Agent Skills format: SKILL.md: the front matter key "model" is not allowed; the keys are name, description, license, compatibility, metadata, allowed-tools. Put custom data in metadata (rule front-matter-key) (folder /dags/skills/bad; Dagu searched /dags/skills)
```

A warning does not fail the validation. Dagu prints each warning at the level WARN, in the form
`skill "<name>": <file>: <message> (rule <id>)`. A step that names a refused skill fails at the
load of the DAG with the same message, and no model is called. The list of the API shows the same
errors and warnings.

A DAG that names more than 20 skills, or a catalog above 2000 tokens, fails with a message that
gives the limit and the value.

## Security

The text of a skill is untrusted content. A skill can try to direct a model against its purpose.
Only a person with the developer role writes a skill, and the audit log records each change.

A skill never adds a tool and never adds a permission. The keys `dags` and `tools` of the step are the
only authority. The key `allowed-tools` is informational: Dagu stores it, shows it, and never applies it.
Dagu never gives a step a tool because of `allowed-tools`.

Dagu never runs a file of a skill. A file in `scripts/` is data. It is stored, mounted, and shown
to the model as text. The model can ask a tool of the step to run it, and the tools of the step decide
whether this is allowed. A skill folder has no link, no hard link and no special file, so a skill
cannot read a file outside its folder. A path never leaves the folder.

A skill holds no secret. See [Secrets](#secrets).

## Errors

| Case | Result |
| --- | --- |
| The skill does not exist | Validation error: no folder, with the folders that Dagu searched. |
| The skill breaks a rule | Validation error and start error: the name, each file, each rule. |
| The step names more than 20 skills | Validation error. |
| The catalog is above 2000 tokens | Validation error. |
| A skill changes during a dispatch | The dispatch fails: the skill changed, retry. |
| A binary file of a skill changed after a run got its version | The step fails with the reason `skills_mount`. |
| A write has no precondition | 428. |
| A write has a stale hash | 409 with the current hash. |
| A download names an unknown skill | 404, no ZIP file. |

## Examples

A skill with the full layout:

```text
skills/pdf-processing/
  SKILL.md
  references/forms.md
  scripts/extract.py
  assets/template.docx
```

```yaml
---
name: pdf-processing
description: Extract text and tables from PDF files and fill PDF forms. Use when the user works with PDF files.
license: Apache-2.0
metadata:
  author: example-org
  version: "1.0"
---
# PDF processing

Read the form rules in references/forms.md before you fill a form.
```

An update with the hash, then the conflict:

```text
PUT /api/v1/skills/pdf-processing/files?path=references/forms.md
If-Match: 3b9c...e41a

200 {"hash":"77d0...a2c1","errors":[],"warnings":[],"files":[...]}

PUT /api/v1/skills/pdf-processing/files?path=references/forms.md
If-Match: 3b9c...e41a

409 {"code":"conflict","message":"the skill \"pdf-processing\" changed after the client read it (current hash 77d0...a2c1)","hash":"77d0...a2c1"}
```

A DAG that uses the skill:

```yaml
steps:
  - id: ask
    action: chat.completion
    with:
      provider: anthropic
      model: claude-sonnet-4-5
      prompt: Fill the form in the attached text.
      skills: [pdf-processing]
```

## Conformance

The binary-level tests are in `conformance/spec096_skills/`. A fake OpenAI-compatible endpoint and a
fake `claude` CLI stand in for the providers. No test needs an external service or the user ID 0. The
unit tests are in `internal/skill/`, `internal/spec/`, `internal/runtime/` and `internal/service/`.

| Test | Proves |
| --- | --- |
| `TestValidateAndStartRefuseEachBadSkill` | `dagu validate` and `dagu start` refuse each bad skill with the skill, the file and the rule, and no model is called. The rules: `skill-md-missing`, `front-matter-start`, `front-matter-end`, `front-matter-yaml`, `front-matter-key`, `front-matter-duplicate-key`, `name-missing`, `name-type`, `name-length`, `name-charset`, `name-hyphen-edge`, `name-hyphen-double`, `name-folder`, `description-missing`, `description-type`, `description-length`, `description-xml`, `license`, `compatibility`, `metadata`, `allowed-tools`, `body-empty`, `body-too-large`, `not-utf8`, `root-entry`, `hidden-file`, `path`, `file-too-large`, `too-many-files`, `symlink` and `hardlink`. |
| `TestValidateAcceptsGoodSkillsAndShowsWarnings` | A good skill with the full layout passes. The warnings `description-short`, `body-lines`, `body-tokens`, `reference-depth` and `file-too-large-to-read` are shown and do not refuse. |
| `TestCatalogCaps` | 20 skills and 2000 tokens are the limits. An excess is an error. |
| `TestChatCompletionUsesLoadSkillAndReadSkillFile` | `load_skill` returns the tagged body and the list of files. A repeat gets the note. `read_skill_file` reads a reference and a script, and refuses an asset and an escape. The run record has the canonical hash. |
| `TestSkillTextHasNoValueReference` | The body and a reference reach the model with a value reference as text. The value of the variable reaches no request. |
| `TestNoReadSkillFileToolWithoutReadableFiles` | A skill with no readable file adds no `read_skill_file`. |
| `TestAgentRunMountsTheFolderVerbatim` | The mount holds every file with the original bytes and the mode 0400. The record has the hash. |
| `TestTwoServersOnOneDirectoryRaceTwoWrites` | Two `dagu server` processes on one directory: one write wins, the other gets 409 with the current hash. |
| `TestServerFilesAndZip` | The file routes, the hash chain, the list with an invalid skill, the ZIP of one and of several skills, the unknown name and the delete. |
| `TestDispatchBundleCarriesTheSkillVersion` (unit) | The bundle of a dispatch holds the folder and the version. An edit after the dispatch does not reach the worker. |
| `TestPrepareDAGWorkspaceFileRefusesASkillThatChanged` (unit) | A skill that changes between the load and the bundle stops the dispatch. |

These rows have no binary-level test. Go tests of `internal/skill/` check them: the rule
`folder-link`, the cache, the lock that a holder leaves after a crash, and the refusal of a write
through a link. The roles of the API need a license, so a Go test of the handlers covers them. The
UI contract has the tests of the UI.

These rows are not covered: the rules `special-file`, `changing` and `total-too-large`. A test of
`total-too-large` writes more than 16 MiB, and a test of `changing` needs a writer that races the
reader.

## Open items

- The upload of a ZIP file to create or replace skills (import).
- A command `dagu skills validate` that checks a folder without a DAG.
- A `preload` mode that puts a short skill in the system message, with no tool call.
- Extra skill directories, such as `.agents/skills`, with an explicit key in the configuration file.
- A mode `name-only` for one skill of a step, and a way to hide a skill from the model.
- A check that warns when `allowed-tools` names a tool that the step does not have.
- A check that every relative link in the body names an existing file.
- The limits of the skills (4 MiB, 16 MiB, 256 files, 20 skills, 2000 tokens) are constants. They become configuration keys when a user needs another value.
- The rules that this spec adds to the standard (see [Source of the rules](#source-of-the-rules)) are not verified against another client. The rule for XML tags comes from the Anthropic API variant of the standard.
- Dagu does not apply the Unicode NFKC normalization of the name, which the reference validator of the standard applies.
