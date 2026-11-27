# Spec 096: Skills

## Status

Partially implemented.

DAG discovery skips the skills directory, as [Terms](#terms) states. `GET /skills` lists the skill
folders with their hash and the errors and the warnings of the format and the folder rules.
`GET /skills/{name}` reads one skill, and `GET /skills/{name}/files` streams one file. The reads use
the cache of [Source of truth](#source-of-truth). `PUT /skills/{name}` creates or replaces
`SKILL.md`, `DELETE /skills/{name}` deletes a skill, and `PUT` and `DELETE /skills/{name}/files`
write and delete one file, under the preconditions of
[Preconditions and conflicts](#preconditions-and-conflicts). Unit tests cover them, and a
conformance test covers discovery. The ZIP download is specified but not built yet.

## Scope

This spec defines:

- the format of a skill: the front matter of `SKILL.md`, the limits, and the rules that Dagu enforces
- the folder of a skill: the layout, the files, and the limits of the files
- the files as the only source of truth of the skills, the hash of a skill, and the preconditions of a write
- the skill routes of the REST API, and the download of skills as a ZIP file
- the skills directory, which DAG discovery skips

This spec does not define:

- the use of a skill by a step: the `skills` key, `chat.completion` and `agent.run`
- the transfer of a skill to a worker, and the record of the version that a run received
- the UI for skills, and `dagu validate` for skills
- the upload of a ZIP file, a command that checks a skill, and the other items of [Open items](#open-items)

## Goal

A skill gives instructions to a model. A skill that follows the Agent Skills standard and the rules
that Dagu adds to it works in Dagu without a change. [Source of the rules](#source-of-the-rules)
lists the added rules, such as the ban on another entry at the root of the folder. A skill that
breaks a rule of an error never reaches a model: Dagu refuses it and tells the author why. The skill
is a folder of files. A person, a git repository or the API can change it. Several Dagu instances
can share the folder.

## Related Specs

- Spec 090: Agent Tools
- Spec 091: Agent Node
- Spec 092: Agent Workers
- Spec 093: AI Endpoints

## Terms

A skill is a folder `skills/<name>/` of the skills directory. The file `SKILL.md` of the folder
defines it.

The skills directory is the directory `skills` at the root of the DAGs directory. The API reads
and writes the skills there.

DAG discovery never enters the directory `skills` at the root of a directory that it scans for DAG
files, also when it is recursive. On a file system that ignores case, this is also a directory
spelled `Skills`, because the path `skills` names it. Discovery lists no file below it. A file
symlink outside it that points into it is read as any other file symlink. A directory `skills`
deeper in the tree is an ordinary directory for discovery.

A refused skill is a skill that breaks one rule of an error in this spec. Dagu never loads a
refused skill. The API lists it with the state ERROR.

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

The front matter MUST NOT be larger than 16384 bytes (16 KiB). The body MUST NOT be empty. The body
MUST NOT be larger than 65536 bytes (64 KiB).

### The rules

Each rule has an identifier. The message of an issue gives the identifier, the file and the reason.

| Rule | Severity | Meaning |
| --- | --- | --- |
| `skill-md-missing` | error | The folder has no file `SKILL.md`. |
| `not-utf8` | error | `SKILL.md` or a file of `references/` or `scripts/` is not valid UTF-8. |
| `front-matter-start` | error | The file does not start with a `---` line. |
| `front-matter-end` | error | The front matter has no closing `---` line. |
| `front-matter-yaml` | error | The front matter is not valid YAML, is larger than 16 KiB, or uses the merge key `<<`. |
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
| `file-too-large-to-read` | warning | A file of `references/` or `scripts/` is larger than 262144 bytes. A model cannot read it through Dagu. |

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
- The 16 KiB limit of the front matter. The time to parse a large mapping grows faster than its
  size.
- The ban on the merge key `<<` in the front matter. Nested merges in a short file take exponential
  time to decode.
- The ban on an XML tag in the description. The Anthropic API variant of the standard states it.
- The limit of 128 characters for `license`. The standard asks for a short value.
- The ban on every key that the standard does not list. The standard does not forbid other keys, and many clients ignore them.
- The rules of [The folder](#the-folder), such as the ban on an entry at the root other than `SKILL.md`, `references/`, `assets/` and `scripts/`. The standard does not forbid other files.
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

The limits are constants of this spec. They are not configuration keys.

## Files and HA

### Source of truth

The folder `skills/<name>/` in the DAGs directory is the only source of truth of a skill. Dagu
keeps no state of a skill in the memory of a process. Every operation reads the folder from the disk
when it needs it.

Dagu supports several instances that share one DAGs directory, for example on a shared volume, and
use the scheduler lock for failover. The instances need no coordination of skills beyond the shared
files, the lock of a write and the hash precondition. Dagu does not depend on a file watcher.

Instances can also each keep their own copy of the DAGs directory and sync the copies with git.
Each copy then has its own lock, so the lock and the precondition guard the writes to one copy only.
Two instances can each accept a write with the same hash. Dagu does not merge the two changes: the
git sync meets them as changes of any other file of the DAGs directory and settles them by its own
rules. Each instance then reads the files that the sync leaves, with their hash.

A read can use a cache in the process. The key of the cache is the path of the folder and the data of
each entry: the path, the kind, the mode, the size, the modification time and, on Linux and macOS,
the status change time. A tool can set the modification time back, but not the status change time.
The cache gives a result only when every key matches. It neither gives nor keeps a result when the newest change is younger than 2 seconds at the
start of the walk.
It keeps at most 128 folders and at most 16 MiB. It keeps no file content: a read of a file reads
the disk and checks the SHA-256 that the read of the skill recorded. The cache is a function of the
disk. It never decides alone.

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
has one line for each entry of the folder that is not a directory. The lines are in the byte order
of the path, from the lowest. Every line has this form:

```text
<digest in hexadecimal><two spaces><path><line feed>
```

The digest of a regular file is the SHA-256 of its bytes, so for a skill of regular files the text
is the output of `sha256sum`. The path is relative to the skill folder. It uses `/` as separator.
It has no `./` prefix. The line feed is `\n`, also on Windows.

The hash does not depend on the order of the directory entries, the time stamps or the modes of the
files. A change of one byte of a file of 4 MiB or less, a change of a path, or the addition or the
removal of a line changes it.

A skill with an error has a hash too, so that a client can repair it. Its lines have the same form:

- An entry that is neither a regular file nor a directory (a link, a special file) has a digest of
  64 zeros.
- A file larger than 4 MiB is not read. Its digest is the SHA-256 of the text `oversize:<size>`.
  So an edit of such a file that keeps its size does not change the hash.
- A skill folder that is a link has one line with an empty path, `<digest><two spaces><line feed>`.
  The digest is the SHA-256 of the text `symlink:<target>`. So its hash is not the hash of an empty
  skill.
- An entry that breaks the rule `path`, a temporary file of a write, and every entry below a
  directory that Dagu does not enter have no line. Dagu does not enter a hidden directory, or a
  directory at the root other than `references/`, `assets/` and `scripts/`.
- In a folder of more than 1024 entries, the entries after the first 1024 have no line. The count
  holds files and directories, depth first, with the names of each directory in byte order.

The text has no other line.

### Preconditions and conflicts

Every write of the API carries the precondition of the client. Here a write is every route that
changes a skill: both `PUT` routes and both `DELETE` routes.

- `If-Match: <hash>` applies the write only when the skill still has this hash. The value can be in a
  pair of quotes, and a prefix `W/` is ignored. A value that is not 64 hexadecimal characters gives 400.
- `If-None-Match: *` applies the write only when the skill folder does not exist. This creates a skill.
  Of the writes, only `PUT /skills/{name}` accepts this header. Another value, or the header on
  another write, gives 400. So a delete and a file write need `If-Match`.

A write with both headers gives 400.

A write with neither header is refused with the HTTP status 428. A write with a stale hash, or a
create on an existing skill, gives 409 with the code `conflict` and the current hash (`hash`). The
write changes nothing. The check and the write are one step under the lock of the skill. So two writers that hold the same hash never both
succeed, also on two Dagu instances that share one DAGs directory. Copies that git syncs are outside
this guarantee, as [Source of truth](#source-of-truth) states.

The precondition does not see an edit of a file above 4 MiB that keeps its size, because the hash
holds only the size of such a file. Such a file is the error `file-too-large`, and the API never
writes one. A write of another file keeps it as it is. A delete or a replacement of it, or a delete
of the skill, removes it, which is what the client asked for.

The lock is a directory lock (`dirlock`) at `skills/.locks/<name>/`. It is held for the time of one
write. A holder that crashes gives the lock free after 30 seconds, and one writer takes it over. A
writer that has lost its lock commits nothing. A writer waits at most 15 seconds and then gets 409.
Another error of the lock gives 500. The folder `.locks/<name>/` goes when the skill folder is gone
after a write or a delete. The directory `.locks` is hidden: the listing ignores it. A repository
that syncs the DAGs directory SHOULD ignore `.locks/`.

A writer commits only within 22.5 seconds of its last renewal of the lock, and another writer judges
the lock free 30 seconds after that renewal, by its own clock. The instances that share a DAGs
directory MUST have clocks that agree within 7.5 seconds.

A read that did not settle (`changing`) holds no version, so its hash passes no precondition: the
write gives 409 with no `hash`. A write to a skill that Dagu cannot read (`unreadable`) gives 500.

A delete first renames the skill folder to a hidden folder `.deleted.<random>` of the skills
directory, then removes the files. A reader sees the whole skill or no skill. A reader that holds a
file open keeps the version that it opened. A file that cannot be removed stays in the hidden
folder, and the server logs it. The next delete of a skill tries such folders again. On Windows, a
file that a reader holds open blocks the delete for a few seconds at most; a delete that it still
blocks then gives 500.

### Secrets

A skill holds no secret. Dagu does not evaluate a value reference in a skill. A person who has
read access to the DAGs directory, to the API or to the ZIP file can read every file of a skill.

## The API

The routes are under `/api/v1`. They use camelCase names. All routes accept the `remoteNode` query
parameter. A read needs a signed-in user when authentication is on. A write, a delete and a file
write need the developer role or above, and the permission `write_dags`.

A JSON body of a skill route has a closed schema. An unknown field gives the status 400. A JSON body
above 8 MiB gives the status 413. The check applies to every `Content-Type`. The error never quotes
a value of the body. A request for a remote node gets the check of that node. The body of
`PUT /skills/{name}/files` is the bytes of the file, with the type `application/octet-stream`.
Another type, or none, gives 400.

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

`GET /skills` returns `skills`, the `SkillSummary` of the first 1000 folders in name order, and
`truncated`, which is true when the skills directory holds more folders. The server reads the
folders one at a time, so a listing holds the text of two files of one skill, at most 8 MiB, beyond
the read cache, which keeps at most 16 MiB.

`SkillSummary` has `name`, `description`, `hash`, `valid`, `errors`, `warnings`, `fileCount`,
`totalBytes`, `bodyLines` and `bodyTokens`. A description above 1024 characters is cut to 1024
characters. `SkillDetail` has `name`, `content`, `hash`, `errors`,
`warnings`, `files`, the values of `license`, `compatibility`, `allowedTools` and `metadata`, and the
`SkillSummary` of the skill in `summary`. A `SkillIssue` has `severity`, `rule`,
`file` and `message`. A `SkillFile` has `path`, `size`, `sha256` and `text`.

A write returns `SkillWriteResult`: the new `hash`, the `errors`, the `warnings` and the `files`.
The client uses the new hash for its next write. `DELETE /skills/{name}` returns 204 with no body.
`DELETE /skills/{name}/files` returns 200 with `SkillWriteResult`. `PUT /skills/{name}/files` refuses
the path `SKILL.md` with 400, because `SKILL.md` has its own route. `DELETE /skills/{name}/files`
refuses `SKILL.md` and a folder that the layout allows with 400, and removes the folders below
`references/`, `assets/` and `scripts/` that become empty. It deletes an entry that breaks the
layout with its content. Each part of its path MUST be the name of the entry as stored: on a file
system that ignores case, another spelling gives 404. `GET /skills/{name}` gives 409
when `SKILL.md` changed after the server read the skill. `GET /skills/{name}/files` gives 409
when the file is gone or has another size than the read of the skill recorded, or when the skill
did not settle while the server read it (`changing`). It gives 500 for a skill that the server
cannot read (`unreadable`). A file whose content
changes during the stream stops the stream before its last bytes, so a complete body always has
the hash in `ETag`. The server then closes the connection and adds nothing to the body. It gives 413 for a file above 4 MiB; the ZIP download includes such a file. The
response has the type `application/octet-stream`, and the header `Content-Disposition` is an
attachment.

A write validates the file that it writes. `PUT /skills/{name}` refuses a `SKILL.md` that breaks a
rule of the file. A write of another file refuses a path that breaks the layout, and a file above the
limits. It also refuses a file of `references/` or `scripts/` that is not UTF-8. The refusal is 400 with
the issues in `issues`. A write can leave the skill with another error, for example a file at the root
that a person added. Such a write succeeds, and the result lists the error.

The status codes:

| Status | Code | Case |
| --- | --- | --- |
| 400 | `bad_request` | The name, the path, the content or a precondition header breaks a rule. The body lists the issues. |
| 404 | `not_found` | The skill or the file does not exist. |
| 409 | `conflict` | The precondition fails. The body has the current `hash`. |
| 413 | `payload_too_large` | The body of `PUT /skills/{name}/files` is larger than 4 MiB, or `GET /skills/{name}/files` reads a file above 4 MiB. A `SKILL.md` above 4 MiB gives 400 with the rule `file-too-large`. |
| 428 | `bad_request` | A write has neither `If-Match` nor `If-None-Match`. |

A path MUST be relative, with `/` separators, and without an empty, `.` or `..` part. A route never
reads or writes a file outside the folder of the skill. The name of a skill in a route MUST be a
folder name: no separator, no leading dot, at most 255 bytes. A new skill MUST follow the name rule.

The API writes an audit event for each change: `skill_update`, `skill_delete`, `skill_file_write`,
`skill_file_delete` and `skill_download`. The resource of the event has the type `skill` and the
name of the skill as its ID.

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

## Security

The text of a skill is untrusted content. A skill can try to direct a model against its purpose.
Only a person with the developer role writes a skill, and the audit log records each change.

A skill never adds a tool and never adds a permission. The key `allowed-tools` is informational:
Dagu stores it, shows it, and never applies it.

Dagu never runs a file of a skill. A file in `scripts/` is data. A skill folder has no link, no hard
link and no special file, so a skill cannot read a file outside its folder. A path never leaves the
folder.

A skill holds no secret. See [Secrets](#secrets).

## Errors

| Case | Result |
| --- | --- |
| A write or a delete has no precondition | 428. |
| A write or a delete has a stale hash | 409 with the current hash. |
| A delete or a file write has `If-None-Match` | 400. |
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
If-Match: 3701b8400dca3109de303137c868e09c754b46fbe652a2694b7b3510f2ca6094

200 {"hash":"278472fbd277db601c4e96a1f12122f298e89be4d151520090c929814a0ef386","errors":[],"warnings":[],"files":[...]}

PUT /api/v1/skills/pdf-processing/files?path=references/forms.md
If-Match: 3701b8400dca3109de303137c868e09c754b46fbe652a2694b7b3510f2ca6094

409 {"code":"conflict","message":"the skill \"pdf-processing\" changed after the client read it (current hash 278472fbd277db601c4e96a1f12122f298e89be4d151520090c929814a0ef386)","hash":"278472fbd277db601c4e96a1f12122f298e89be4d151520090c929814a0ef386"}
```

## Conformance

The binary-level tests are in `conformance/spec096_skills/`. The unit tests are in
`internal/persis/file/dag/`, `internal/skill/` and `internal/service/frontend/api/v1/`.

| Test | Proves |
| --- | --- |
| `TestRecursiveDiscoverySkipsTheSkillsDirectory` | With recursive discovery, `dagu ls` lists no DAG file below `skills/` at the root of the DAGs directory, and lists the DAG files of a `skills` directory deeper in the tree. |
| `TestDiscoverRecursiveSkipsSkillsDir` (unit) | Discovery neither lists nor returns for watching anything below `skills/` at the root, and reads a `skills` directory deeper in the tree. |
| `TestDiscoverRecursiveSkipsSkillsDirInOtherCase` (unit) | On a file system that ignores case, discovery also skips a root directory spelled `Skills`. Where case matters, it reads it. |
| `TestFormatRules` (unit) | Each rule of the format gives its issue with the file and the severity, and the error of a refused skill names each broken rule. |
| `TestFolderRules` (unit) | Each folder rule other than `total-too-large`, `changing` and `unreadable` gives its issue, and a link is never read. The case of `special-file` runs on Unix only. |
| `TestReadNeverFollowsASwappedLink` (unit) | A file that becomes a link while Dagu reads the folder is never read through the link. |
| `TestReadOfAFolderThatNeverSettles` (unit) | A folder that changes during every read attempt gives the rule `changing`. |
| `TestHashCanonicalForm` (unit) | The hash is the SHA-256 of the `sha256sum` text of the files. The order, the times and the modes do not change it. |
| `TestPartialSkillIsRefused` (unit) | A folder with no `SKILL.md` is refused, and the temporary file of a write is ignored. |
| `TestReadOfATooLargeFolder` (unit) | A folder above 16 MiB gives the rule `total-too-large`, and each of its text files still reads from disk with its recorded hash. |
| `TestReadCachesOnlyASettledFolder` (unit) | A second read of a folder with no change in the last 2 seconds reads no file. A folder with a newer change is read again. |
| `TestSlowReadIsNotCached` (unit) | A read that began less than 2 seconds after a change is not cached, however long it takes. |
| `TestReadSeesEveryChange` (unit) | A change of the size of a file shows in the next read, also when the time stamp stays. |
| `TestReadSeesAnEditThatKeepsTheTimeStamp` (unit) | On Linux and macOS, an edit that keeps the size and sets the time stamp back shows in the next read of a cached folder. |
| `TestReadSeesALayoutChange` (unit) | A new folder at the root, a hidden folder or a hard link shows in the next read of a cached folder. |
| `TestOpenRefusesAChangedFile` (unit) | A file that changed after the read gives `ErrFileChanged`, at the open or at the end of the stream, and a link is never opened. |
| `TestTextRefusesAChangedFile` (unit) | A text file that changed after the read gives `ErrFileChanged`, never the new content under the old hash. |
| `TestCheckExistingPathRefusesEveryEscape` (unit) | A path that is absolute, has an empty, `.` or `..` part, a backslash or a control character is refused. |
| `TestUnreadableFolderNamesNoServerPath` (unit) | A folder that Dagu cannot read gives the rule `unreadable`, and the message names no server path. |
| `TestSkillsList` (unit) | `GET /skills` lists every folder sorted by name, also one with errors, and no plain file or hidden folder. |
| `TestSkillsListIsBounded` (unit) | `GET /skills` lists the first 1000 folders and sets `truncated` when the directory holds more. |
| `TestSkillsGet` (unit) | `GET /skills/{name}` returns the raw `SKILL.md`, the files and the issues, 404 for an unknown skill and 400 for a name that is not a folder name. |
| `TestSkillsReadFile` (unit) | `GET /skills/{name}/files` sends a text file and a binary file as stored, as an attachment of type `application/octet-stream` with the hash in `ETag`. A file above 4 MiB gives 413, a link 404 and a path that leaves the folder 400. |
| `TestSkillFileStreamAborts` (unit) | A file that changes during the stream aborts the response, and no error body joins the partial file. |
| `TestSkillsReadFileUnreadable` (unit) | `GET /skills/{name}/files` gives 500, not 404, for a skill that the server cannot read, and the body names no server path. |
| `TestSkillsPathTraversal` (unit) | A skill name that leaves the skills directory gives 400 or 404 for a read, a write and a delete, and no file outside it changes. |
| `TestCheckPath` (unit) | A write accepts `SKILL.md` and a path of the layout, and refuses an escape, a hidden name and a path outside the layout. |
| `TestWritePreconditions` (unit) | A create of an existing skill and a stale hash give a conflict with the current hash and change nothing. A write needs a precondition. The hashes chain. A bad `SKILL.md` is refused. |
| `TestWriteRefusesASkillThatDoesNotSettle` (unit) | The hash of a read that did not settle passes no precondition. |
| `TestWriteAndDeleteRefuseSymbolicLinks` (unit) | A write or a delete never goes through a link of the folder, of a folder below it or of the file. A delete of a linked skill or of a linked file removes the link only. |
| `TestWriteStaysInTheFolderThatItOpened` (unit) | A skill folder or a skills directory that becomes a link during the write leaves the link target as it was; the write stays in the folder that it opened and gives a conflict, or stops when the lock moved with the skills directory. |
| `TestDeleteStaysInTheFolderThatItOpened` (unit) | A skill folder that becomes a link during a file delete leaves the link target as it was, and the delete gives a conflict. |
| `TestDeleteOfANestedFileRemovesTheEmptyFolders` (unit) | A file delete removes the folders that become empty below the folder of the layout. |
| `TestDeleteFileRefusesALayoutFolder` (unit) | A file delete refuses a folder of the layout and removes a folder that breaks the layout with its content. |
| `TestDeleteFileNeedsTheStoredName` (unit) | Another spelling of `SKILL.md` or of a folder of the layout deletes nothing. |
| `TestDeleteFileWaitsForAReader` (unit) | On Windows, a file delete waits for a reader that holds the file open for a short time. |
| `TestLostLockCommitsNothing` (unit) | A writer that lost its lock writes, creates and deletes nothing, and leaves no folder. |
| `TestLockFailureIsNotAConflict` (unit) | A lock error other than a held lock is not a conflict. |
| `TestReadThatMeetsAWriteGetsOneVersion` (unit) | A read whose folder a write replaces between its walk and its read of the files reads again and gets the new version. |
| `TestTwoWritersRace` (unit) | Of two writers with the same hash, one wins and the other gets a conflict, also when a holder that died left the lock. |
| `TestReaderNeverSeesAHalfWrittenFile` (unit) | A reader during a stream of writes sees one complete version or `changing`, never a mix. |
| `TestReadAfterAWriteSeesIt` (unit) | A read right after a write gets the written version, also from a cache that held the version before, and the write returns the hash on disk. |
| `TestReadAfterAFileDeleteSeesIt` (unit) | A read right after a file write or a file delete gets the new version, also from the cache of another store on the same directory. |
| `TestDeleteOfASkillIsAtomic` (unit) | A file that a delete cannot remove never leaves a part of the skill. |
| `TestDeleteKeepsAnOpenFile` (unit) | A reader that opened a file before a delete reads the whole file. |
| `TestReadThatMeetsADeleteFindsNoSkill` (unit) | A read that meets the delete of its skill finds no skill, never a part of one. |
| `TestDeleteRemovesTheLockFolder` (unit) | A delete removes the lock folder of the skill. |
| `TestDeleteRemovesWhatEarlierDeletesLeft` (unit) | A delete removes the hidden folders that earlier deletes left. |
| `TestClosedBodyMiddleware` (unit) | A skills body with an unknown field, a wrong type or more than 8 MiB is refused for every `Content-Type`, and the error quotes no value. A file body passes as `application/octet-stream` only. |
| `TestSkillsPutNeedsAPrecondition` (unit) | `PUT /skills/{name}` gives 428 with no precondition and 400 for an `If-Match` that is not 64 hexadecimal characters. |
| `TestSkillsPutChainAndConflict` (unit) | A create, an update with the hash, a stale hash and a change on disk give 200 or 409 with the current hash. An invalid `SKILL.md` gives 400 with its rules. |
| `TestSkillsFiles` (unit) | The file routes chain the hashes, apply the rules of the folder, the 4 MiB limit and the preconditions, and delete an entry that breaks the layout. |
| `TestSkillsDelete` (unit) | `DELETE /skills/{name}` gives 428, 409, 400 for `If-None-Match`, 204 and then 404. |
| `TestSkillsWriteRefusesSymbolicLinks` (unit) | A file write through a linked skill folder or a linked folder of a skill gives 400 and writes nothing outside. |
| `TestSkillsTwoInstancesRaceOnOneDirectory` (unit) | Two API instances on one directory: one write wins, the other gets 409. |
| `TestSkillsWriteNeedsDeveloper` (unit) | A viewer reads skills and cannot write or delete them or their files. |
| `TestSkillsWritesFollowTheDAGWriteRules` (unit) | A write or a delete needs `write_dags`, and the read-only mode of Git Sync refuses it. |
| `TestSkillsWritesAreAudited` (unit) | Each write and each delete gives an audit event with the resource `skill`, the name and the hash, and no content. |
| `TestSkillsWriteFailureNamesNoServerPath` (unit) | A write that the disk refuses gives 500 and names no server path. |

The ZIP download has no test yet.

## Open items

- The upload of a ZIP file to create or replace skills (import).
- A command `dagu skills validate` that checks a folder without a DAG.
- Extra skill directories, such as `.agents/skills`, with an explicit key in the configuration file.
- A check that every relative link in the body names an existing file.
- The limits of the skills (4 MiB, 16 MiB, 256 files) are constants. They become configuration keys when a user needs another value.
- The rules that this spec adds to the standard (see [Source of the rules](#source-of-the-rules)) are not verified against another client. The rule for XML tags comes from the Anthropic API variant of the standard.
- Dagu does not apply the Unicode NFKC normalization of the name, which the reference validator of the standard applies.
