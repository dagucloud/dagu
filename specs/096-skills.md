# Spec 096: Skills

## Status

Partially implemented.

DAG discovery skips the skills directory, as [Terms](#terms) states. Unit tests and a conformance
test cover it. The format, the folder rules, the files and the hash, the API and the ZIP download are
specified but not built yet.

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
each entry: the path, the kind, the mode, the size and the time of the last change. The cache gives a result only
when every key matches. It does not give a result when the newest change is younger than 2 seconds.
It keeps at most 128 folders and at most 64 MiB of file content. The cache is a function of the disk.
It never decides alone.

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

- `If-Match: <hash>` applies the write only when the skill still has this hash. The value can be in quotes,
  and a prefix `W/` is ignored. A value that is not 64 hexadecimal characters gives 400.
- `If-None-Match: *` applies the write only when the skill folder does not exist. This creates a skill.
  Of the writes, only `PUT /skills/{name}` accepts this header. Another value, or the header on
  another write, gives 400. So a delete and a file write need `If-Match`.

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
write. A holder that crashes gives the lock free after 30 seconds. A writer waits at most 15 seconds
and then gets 409. The directory `.locks` is hidden: the listing ignores it. A repository that syncs
the DAGs directory SHOULD ignore `.locks/`.

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

`GET /skills` returns `skills`, the `SkillSummary` of the first 1000 folders in name order, and
`truncated`, which is true when the skills directory holds more folders. The server reads the
folders one at a time, so a listing keeps the text of one skill in memory, at most 16 MiB.

`SkillSummary` has `name`, `description`, `hash`, `valid`, `errors`, `warnings`, `fileCount`,
`totalBytes`, `bodyLines` and `bodyTokens`. `SkillDetail` has `name`, `content`, `hash`, `errors`,
`warnings`, `files`, the values of `license`, `compatibility`, `allowedTools` and `metadata`, and the
`SkillSummary` of the skill in `summary`. A `SkillIssue` has `severity`, `rule`,
`file` and `message`. A `SkillFile` has `path`, `size`, `sha256` and `text`.

A write returns `SkillWriteResult`: the new `hash`, the `errors`, the `warnings` and the `files`.
The client uses the new hash for its next write. `DELETE /skills/{name}` returns 204 with no body.
`DELETE /skills/{name}/files` returns 200 with `SkillWriteResult`. `PUT /skills/{name}/files` refuses
the path `SKILL.md` with 400, because `SKILL.md` has its own route. `GET /skills/{name}/files` gives 409
when the skill changed while the server read the file. It gives 413 for a file above 4 MiB; the ZIP
download includes such a file.

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
`internal/persis/file/dag/`.

| Test | Proves |
| --- | --- |
| `TestRecursiveDiscoverySkipsTheSkillsDirectory` | With recursive discovery, `dagu ls` lists no DAG file below `skills/` at the root of the DAGs directory, and lists the DAG files of a `skills` directory deeper in the tree. |
| `TestDiscoverRecursiveSkipsSkillsDir` (unit) | Discovery neither lists nor returns for watching anything below `skills/` at the root, and reads a `skills` directory deeper in the tree. |
| `TestDiscoverRecursiveSkipsSkillsDirInOtherCase` (unit) | On a file system that ignores case, discovery also skips a root directory spelled `Skills`. Where case matters, it reads it. |

The format, the folder rules, the hash, the API and the ZIP download have no test yet.

## Open items

- The upload of a ZIP file to create or replace skills (import).
- A command `dagu skills validate` that checks a folder without a DAG.
- Extra skill directories, such as `.agents/skills`, with an explicit key in the configuration file.
- A check that every relative link in the body names an existing file.
- The limits of the skills (4 MiB, 16 MiB, 256 files) are constants. They become configuration keys when a user needs another value.
- The rules that this spec adds to the standard (see [Source of the rules](#source-of-the-rules)) are not verified against another client. The rule for XML tags comes from the Anthropic API variant of the standard.
- Dagu does not apply the Unicode NFKC normalization of the name, which the reference validator of the standard applies.
