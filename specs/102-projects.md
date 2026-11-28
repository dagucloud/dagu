# Spec 102: Projects

## Status

Not implemented.

Until v3.0, the behavior of this spec sits behind an undocumented switch
that is off by default ([Decisions](#decisions)). With the switch off, Dagu
reads and writes the same files, and serves the same API, CLI, and UI, as it
does without this spec. Conformance tests for this spec turn the switch on.
From v3.0, projects are always on and the switch is gone.

## Scope

This spec covers:

- project names, display names, and IDs
- the registry of projects, and how each process learns of changes to it
- the layout of a project: its DAG directory, its state directories, and the
  paths that place them
- which state is server-wide and which belongs to a project
- which project's base config, secrets, profiles, notification routes, and
  incident policies a run uses
- how the CLI, steps, the API, MCP, and webhooks choose a project
- access: grants, the role of a user or API key in a project, and visibility
- the library, and how `dag.run` resolves its target
- creating a project, changing its display name, deleting it, a project
  changed on disk, and the default project
- scheduling, queues, and distributed execution across projects
- the wiki and audit entries of a project
- git sync per project
- the user interface contracts for projects
- migration of a v2 install on the first v3.0 start, its preview, and its
  report

This spec does not define:

- what base config, secrets, profiles, notification routes, and incident
  policies do once chosen. Their owning specs define that; this spec defines
  only which project's apply.
- MCP resource URIs and tool parameters beyond the `project` parameter and
  the wiki URIs. Specs 020 and 021 define them.
- report protocol 3 and Dagu Cloud project bindings. RFC 002 defines them;
  the registry only holds the binding.
- moving a DAG between projects with its history, and the All projects
  view. Both ship after v3.0.
- business calendars, which stay server-wide (Spec 084)
- users, authentication methods, and what each role may do
- scheduler high availability (Spec 085), which applies to the scheduler
  process as a whole
- a process per project, several scheduler processes that split projects
  between them, letting `dag.run` run a DAG of another project, and
  projects found from a `dagu.json` file

## Goal

Several teams share one Dagu server. Each team's DAGs run with the team's
own settings, and only people with a role in the team's project see them. A
DAG belongs to the project whose directory holds it, so the boundary does
not depend on labels.

An install without workspaces upgrades with no manual step and keeps its DAG
files, URLs, and commands. An install with workspaces upgrades with no
manual step, and no workflow changes silently which settings or secrets it
runs with.

Some cases need a step, and the preview lists each before the upgrade:

- An install whose paths overlap ([Overlapping Paths](#overlapping-paths))
  sets `paths.projects_dir` or moves a path.
- A DAG that migration finds with an [unsettled label](#unsettled-labels)
  loads once its label is removed or its file moves into a project.
- A DAG that v2 found through a symbolic link out of `<dags_dir>`, and a
  library DAG that v2 scheduled because `alt_dags_dir` lay inside
  `dags_dir`, are scheduled again once their files move into a DAG
  directory. See [What Moves](#what-moves).

## Decisions

| ID | Closed | Decision |
| --- | --- | --- |
| GI | 2026-11-24 | Incident providers stay server-wide, and only admins change them, as with notification channels. A project's incident policies name providers. See [Settings](#settings). |
| GL | 2026-11-24 | The behavior of this spec lands on `main` behind an undocumented switch that is off by default. v3.0 turns projects on and removes the switch. See [Status](#status). |

## Related Specs

- [RFC 003: Replace Workspaces with Projects](../rfcs/003-projects.md) gives
  the design and its reasons.
- [RFC 002: Dagu Cloud Connector](../rfcs/002-dagu-cloud-connector.md)
  defines report protocol 3 and project bindings.
- Step working directory: [Spec 013](013-step-run.md)
- Parallel and foreach: [Spec 018](018-parallel-and-foreach.md)
- Sub-DAG working directory: [Spec 019](019-sub-dag-working-directory.md)
- MCP server: [Spec 020](020-mcp-server.md)
- MCP read tool: [Spec 021](021-mcp-read-tool.md)
- Wiki page format: [Spec 034](034-wiki-page-format.md)
- File dependencies: [Spec 035](035-file-dependencies.md)
- SSH run: [Spec 042](042-ssh-run.md)
- Secrets providers: [Spec 069](069-secrets-providers.md)
- Runtime profiles: [Spec 070](070-runtime-profiles.md)
- Business calendars: [Spec 084](084-business-calendars.md)
- Scheduler high availability: [Spec 085](085-scheduler-ha.md)
- Backfill: [Spec 086](086-backfill.md)
- Node targeting: [Spec 089](089-node-targeting.md)
- Workflow tests: [Spec 101](101-workflow-tests.md)

## Terms

`<data_dir>` is `paths.data_dir`, `<projects_dir>` is `paths.projects_dir`,
and `<dags_dir>` is `paths.dags_dir`.

A project is a set of DAG files with its own runs, logs, base config,
secrets, profiles, notification routes, incident policies, and wiki.

The default project is the project named `default`. Every install has it.

The registry is the server-wide list of projects. See [Registry](#registry).

A project's DAG directory is the directory that holds its DAG files. See
[Layout](#layout).

A project's state directory is `<data_dir>/projects/<name>/`. Its state
directories are that directory and each `.projects/<name>/` and
`.projects/<name>.library/` directory under a
[path set apart](#paths-set-apart).

A project's library directories are `library/` in its state directory and
each `.projects/<name>.library/` directory under a path set apart. They hold
the runs of the library DAGs that run in the project. See
[Runs of Library DAGs](#runs-of-library-dags).

The library is the directory `paths.alt_dags_dir`. A library DAG is a DAG
file in the library, or a DAG defined in such a file. The library belongs
to no project.

A file that runs in place is a DAG file outside a project's DAG directory
that the registry records as that project's. Only migration records such
files. See [Files That Run in Place](#files-that-run-in-place).

The addressed project of a CLI command is the project the command acts on.
See [CLI](#cli).

A server-wide role is a role that applies to every project. A grant gives a
user, a group, or an API key a role below `admin` in one project. The role
of a user or API key in a project is the higher of its server-wide role and
its grant on that project.

A user or API key can see a project when it has a role in the project.

A live process is a server, scheduler, or coordinator process that has an
entry in the service registry. Every server, scheduler, and coordinator
process keeps an entry there while it runs.

A missing project is a registered project whose DAG directory does not exist
and that has no file running in place that exists.

An unattached state directory is a state directory whose recorded project ID
the registry lacks.

A run that is not finished is a run that is queued, running, or waiting for
a person.

## Behavior

### Names

A project name is a directory name, so it works as one on macOS, Windows,
and Linux.

Rules:

- A name matches `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`: 1 to 64 ASCII letters,
  digits, `_`, and `-`, starting with a letter or digit.
- A name is not a Windows device name, in any case: `con`, `prn`, `aux`,
  `nul`, `com0` to `com9`, or `lpt0` to `lpt9`.
- `default` is reserved, in any case. Only the default project has it.
- Names are unique without regard to case. A project named `Weather` cannot
  be created while `weather` exists.
- A name given in another case resolves to the stored spelling. Every path,
  URL, and record then uses that spelling.
- A name never changes. No UI, API, or CLI path renames a project.

| Name | Valid | Reason |
| --- | --- | --- |
| `weather` | yes | |
| `day-ahead` | yes | |
| `ETL_2` | yes | |
| `-ops` | no | starts with `-` |
| `_internal` | no | starts with `_` |
| `my.project` | no | `.` is not allowed |
| `天気予報` | no | not ASCII letters or digits |
| `Default` | no | reserved |
| `COM1` | no | Windows device name |
| 65 letters | no | longer than 64 characters |

### Display Names

- A project has a display name: any text up to 200 characters.
- Until an admin sets one, the display name is the name.
- An admin changes the display name in the UI or API at any time. Only the
  registry changes: no file, directory, URL, or record moves.
- Pages show the display name. Commands, API calls, and links use the name.

### Identity

- Each project has an ID, a random UUID assigned when the project is
  created.
- A project deleted and created again under the same name gets a new ID. Its
  grants, audit entries, and Dagu Cloud history never mix with the old
  project's.
- The default project gets its ID on the first v3.0 start. Each project that
  migration makes gets a new ID.
- Records that outlive a request refer to a project by ID: grants, audit
  entries, dispatch tasks, leases, and status reports.
- `--project`, `DAGU_PROJECT`, the API's `project` parameter, and
  `project_mappings` take a name and resolve it to an ID.
- Project IDs never appear in the UI.

### Registry

- The registry is `projects.json` in `<data_dir>`.
- For each project, it holds the ID, the name, the display name, the
  creation time, the slot seed, the Dagu Cloud binding (RFC 002), and, for a
  project that migration made, the v2 workspace name it came from. It also
  records the files that run in place as the project's, and whether the
  project is marked deleting. Server-wide, it records the DAG files that
  migration found with an [unsettled label](#unsettled-labels).
- A project's slot seed goes into the run ID of each slot of its schedules
  (Spec 085). It is `paths.data_dir`, spelled as configured, for the default
  project and each project that migration made, and the project's ID for any
  other. It is set when the project is registered and never changes, so it is
  the same after a restart and on every host that shares the storage.
- The registry is the only list of projects. Creating a project, changing
  its display name, deleting it, and migration change it. Nothing on disk
  adds to it.
- A directory under `<projects_dir>` is a project's DAG directory only when
  a project of that name is registered. A checkout's `scripts/`, `docs/`, or
  `.git/` is never a project.
- A `dagu.json` file in a DAG directory changes nothing.
- Every write to the registry holds one lock and checks the
  [name rules](#names) before it stores. A write that breaks them stores
  nothing.
- Each server, scheduler, and coordinator process checks the registry for
  changes every 5 seconds, also on shared storage that sends no notice of
  another host's write. When it changed, the process reads the registry and
  starts or stops using projects to match it, without a restart.
- Every write reaches every process, also when two writes land within one
  tick of the file system's modification time.
- A CLI command sees every project registered when it starts.

### Layout

```text
$DAGU_HOME/
  config.yaml            server-wide settings
  base.yaml              the default project's base config
  dags/                  the default project's DAG directory
  logs/admin/            admin and audit logs
  data/                  paths.data_dir
    projects.json        the registry
    server/              queues defined in config.yaml
    users/ apikeys/ auth/ ...  other server-wide state
    projects/
      default/           the default project's state directory
      weather/           the weather project's state directory
        runs/ logs/ artifacts/ settings/ state/
        library/         the runs of library DAGs in weather
          runs/ logs/ artifacts/ state/
  projects/              paths.projects_dir
    weather/             the weather project's DAG directory
      base.yaml          its base config
      *.yaml             its DAG files
      wiki/              its wiki
```

#### DAG Directory

- A project's DAG directory is `<projects_dir>/<name>/`. The default
  project's is `<dags_dir>`.
- A project's DAG directory holds its DAG files.
- Every project other than the default project keeps its wiki in `wiki/`
  and its base config in `base.yaml`, at the top of its DAG directory.
  `base.yaml` there is not a DAG, and DAG discovery skips it. The default
  project's wiki and base config are described in
  [The Default Project](#the-default-project).
- `paths.projects_dir` defaults to `projects` in the Dagu home that holds
  the default `dags` directory: the directory given by `--dagu-home`, else
  `$DAGU_HOME`, else `~/.dagu` when it exists. Without a Dagu home, it
  defaults to `projects` beside `dags` in the XDG config directory. It is
  set like the other `paths` settings.
- No state is ever written to a DAG directory or anywhere under
  `<projects_dir>`.

#### State Directory

- A project's state directory, `<data_dir>/projects/<name>/`, holds
  everything else the project has: `runs/`, `logs/`, `artifacts/`,
  `settings/` for its secrets, profiles, notification routes, and incident
  policies, and `state/` for the rest of its
  [project state](#server-wide-and-project-state).
- The default project's state directory is `<data_dir>/projects/default/`.
- `library/` in a state directory holds the runs of the library DAGs that
  run in the project, laid out as the project's own: `runs/`, `logs/`,
  `artifacts/`, and `state/` for their run work directories, process
  heartbeats, queues, and DAG state.
- The default project's is `<data_dir>/projects/default/library/`. No v2
  directory and no DAG's directory is there, so it holds the default
  project's library runs before and after migration moves the rest of the
  default project's state.
- Each of a project's state directories records the project's ID and name.

#### Paths Set Apart

- When `paths.log_dir`, `paths.artifact_dir`, `paths.dag_runs_dir`, or
  `paths.dag_run_work_dir` is set, by `config.yaml`, the environment, or a
  flag, each project's logs, artifacts, runs, or run work directories live
  under that path in `.projects/<name>/`, instead of in the state directory.
- The logs, artifacts, runs, or run work directories of the library DAGs
  that run in the project live under that path in
  `.projects/<name>.library/`. A project name has no `.`, so no project
  has that directory.
- Each such `.projects/<name>/` and `.projects/<name>.library/` directory
  is one of the project's state directories and records the project's ID
  and name.

#### Server-Wide State in `paths.data_dir`

Outside `<data_dir>/projects/`, Dagu keeps only server-wide state in
`<data_dir>`: the registry, the queues defined in `config.yaml` in
`server/`, and the rest of the Server row of
[Server-Wide and Project State](#server-wide-and-project-state). A DAG
root may still lie inside `<data_dir>` in a directory that holds no state,
as `paths.dags_dir: /data/dags` does in the Helm chart. See
[Overlapping Paths](#overlapping-paths).

#### Discovery

- `dag_discovery` settings in `config.yaml` apply to every project's DAG
  directory.
- Discovery never follows a symbolic link out of a project's DAG directory
  or out of the library, whatever `dag_discovery.symlinks` says. A file
  symlink that stays inside them follows `dag_discovery.symlinks`.
- When one of `<dags_dir>`, `<projects_dir>`, and `paths.alt_dags_dir` lies
  inside another, discovery in the outer one skips the inner one. Each DAG
  file belongs to one project or to the library.
- Discovery in `<dags_dir>` skips the files that run in place as another
  project's.

#### Overlapping Paths

A DAG directory never shares a directory with state. The DAG roots are
`<dags_dir>`, `<projects_dir>`, and `paths.alt_dags_dir`. The state roots
are `<data_dir>/projects/`, each
[path set apart](#paths-set-apart), and the directory of each server-wide
store, such as `<data_dir>/server/` or the one a `paths` setting like
`paths.users_dir` places.

Every server, scheduler, coordinator, and CLI process checks the paths in
effect after `config.yaml`, the environment, and flags. It refuses to
start, and says which paths overlap, when:

- a DAG root is `<data_dir>` or contains it;
- a DAG root is, contains, or lies inside a state root;
- two DAG roots are the same directory.

Any other directory inside `<data_dir>` may hold a DAG root.

`dagu migrate workspaces` is the one exception. It does not refuse to
start: it lists each overlap as an item that needs attention and goes on
with the rest of the preview.

An install whose `<data_dir>` is its Dagu home, as in the Helm chart, sets
`paths.projects_dir`, since the default would be `<data_dir>/projects/`.
The v3.0 Helm chart sets it.

#### Paths That Only Migration Reads

`paths.queue_dir`, `paths.suspend_flags_dir`, `paths.proc_dir`,
`paths.dag_state_dir`, `paths.webhooks_dir`, `paths.views_dir`, and
`paths.event_store_dir` place stores that belong to a project. They tell
migration where v2 kept that data, and the server does not use them.
When `paths.queue_dir`, `paths.suspend_flags_dir`, or `paths.proc_dir` is
set, the server logs a warning at startup.

#### File Systems

When `<projects_dir>` and `<dags_dir>` are on different file systems, the
server logs a warning at startup, and the projects page shows it to admins.
A container that mounts only `dags` would lose every project's DAG files
when it is recreated.

### Server-Wide and Project State

| Scope | Contents |
| --- | --- |
| Server | `config.yaml`, library DAG files, business calendars, the registry, the v2 webhook URLs that migration kept, users and their grants, API keys, OIDC and proxy authentication, the encryption key, license, upgrade check, the Dagu Cloud connection with its report and remote access state, remote nodes, coordinator and worker state (dispatch tasks, leases, active distributed runs, worker heartbeats), service registry, the scheduler lock, pause flag, and restart backoff, audit log, admin logs, notification channels and notification settings, incident providers, queues defined in `config.yaml` and the runs admitted to them |
| Project | DAG files, base config, runs, logs, artifacts, run work directories, git sync settings and clone, process heartbeats, run events, DAG state, suspend flags, DAG settings, pins, views, scheduler state and checkpoints, watch state, slot index, backfills, materializations, agent session cleanups, secrets, profiles, notification routes, incident policies and incidents, wiki, per-DAG webhooks and their public URLs, node inventories, other queues |

Rules:

- State that a later spec adds belongs to a project, unless it holds
  identity, authentication, licensing, or the state of the processes and
  workers that serve every project.
- A setting a workflow runs with belongs to a project. Business calendars
  are the one exception: the server keeps one set.

### Settings

A project owns its base config, secrets, profiles, notification routes, and
incident policies. It inherits none of them from the server or from another
project, except as
[Base Config of Migrated Projects](#base-config-of-migrated-projects)
states.

| Setting | A project's DAG uses |
| --- | --- |
| Base config | The project's base config, then the DAG file. See [Base Config](#base-config) |
| Secret | The project's secret of that name |
| Profile | The project's profile defaults, then the selected profile. A profile named by a run resolves in the project |
| Notification routes | The project's routes, which send to server-wide channels |
| Incident policies | The DAG's policy, which inherits the project's |

Rules:

- No server-wide layer of base config, secrets, profiles, routes, or
  policies applies to any project.
- A run reads its base config, secrets, profiles, notification routes, and
  incident policies in its project, whether it is started, retried, queued,
  or a sub-DAG run. A DAG's `workspace` label adds no layer of any of them,
  and chooses no default profile for its runs.
- A change to one project's settings never changes what another project's
  DAGs run with, except as
  [Base Config of Migrated Projects](#base-config-of-migrated-projects)
  states.
- Notification channels and notification settings, such as SMTP, are
  server-wide, and only admins change them. A project's routes name
  channels.
- Incident providers, such as PagerDuty, are server-wide, and only admins
  change them. A project's incident policies name providers.
- A project's users see a channel's or a provider's name and type, never its
  URL, secret, or credentials. Of the notification settings they see whether
  SMTP and its secrets are set and the OAuth provider, never the SMTP host,
  account, or sender, or an OAuth tenant or client ID.
- Changing a project's settings needs the role in that project that
  [Roles](#roles) lists.
- A credential that every project uses belongs in a secret provider
  (Spec 069), which each project's DAGs reference. A model API key that no
  DAG reads is a shared key of
  [Spec 093](093-model-connections.md#shared-keys) instead.

#### Base Config

- A DAG's base config is its project's base config, then the DAG file.
- The default project's base config is the file at `paths.base_config`.
- Every other project's base config is `base.yaml` in its DAG directory. A
  project without that file has no base config, unless migration made it.
  See [Base Config of Migrated Projects](#base-config-of-migrated-projects).
- A child started with `dag.run` runs in its caller's project and gets that
  project's base config. This holds whether the child is in the caller's
  file, in its own file, or in the library.

#### Encryption

- Every project encrypts its secrets with the server's key:
  `DAGU_ENCRYPTION_KEY`, or `auth/encryption_key` in `<data_dir>`.
- No project creates or holds a key of its own. A project's state directory
  copied to another server decrypts there only with the same key.

#### Roles

| Setting | Role in the project that changes it |
| --- | --- |
| Base config | `developer` or higher |
| Secrets | `manager` or higher |
| Profiles | `manager` or higher. A protected profile needs `admin` |
| Profile defaults | `developer` or higher. The default project's need `admin` |
| Notification routes | `developer` or higher |
| Incident policies | `developer` or higher |

### Choosing a Project

#### CLI

A command addresses the project named by `--project`, else by
`DAGU_PROJECT`, else the project whose DAG directory holds the current
directory, else the default project.

Rules:

- A directory under a project's DAG directory belongs to that project, also
  when `<projects_dir>` lies inside `<dags_dir>`.
- A current directory under `<projects_dir>` that no registered project
  uses addresses the default project.
- A name in `--project` or `DAGU_PROJECT` resolves without regard to case.
- A command that names a project that does not exist fails and changes
  nothing.
- A DAG file given by path must belong to the addressed project, in its DAG
  directory or running in place as its, or be in the library. A file of
  another project is refused, and the error names the project the file
  belongs to. A file that belongs to no project and lies outside the
  library, such as one in a temporary directory, runs in the addressed
  project.
- A local run starts its child process with `--project`. `DAGU_HOME` stays
  the server's, so the child reads the same `config.yaml`.
- `dagu server`, `dagu scheduler`, `dagu coordinator`, `dagu start-all`,
  and `dagu worker` serve every project and refuse `--project`.
- The control socket of a run is addressed by `<data_dir>`, its project,
  its DAG name, and its run ID, so runs of DAGs of the same name in two
  projects, or in two installs on one host, never share one. A lookup for a
  run that `<data_dir>` holds as running also tries the addresses that
  earlier versions used, which name no `<data_dir>`, so a run that such a
  version started stays reachable.
- `dagu project create <name>` creates a project, `dagu project delete
  <name>` deletes one, and `dagu project list` lists the registered
  projects with each one's display name, DAG directory, and state:
  `active`, `missing`, or `deleting`. See [Lifecycle](#lifecycle).
- `dagu test` with no argument selects the test files under the addressed
  project's DAG directory and beside its files that run in place. A test
  file given by path runs in the project of the DAG it tests. Spec 101
  defines the rest of `dagu test`.

#### Steps

- Every step that runs on the server runs with `DAGU_PROJECT` set to the name
  of its run's project. A `dagu` command in the step addresses that project
  from any working directory.
- A step on a worker does not get `DAGU_PROJECT`. Its `dagu` commands read
  the worker's own Dagu home.

#### API

- API paths that address DAGs, runs, secrets, profiles, notification routes,
  incident policies, base config, wiki pages, or git sync take a `project`
  parameter. It takes a name and resolves it without regard to case.
- Without `project`, a request addresses the default project.
- A request that names a project the caller cannot see gets `404`, as for a
  project that does not exist.
- A request for a DAG the project lacks gets `404`. The error names the
  projects the caller can see that have a DAG of that name.
- Every path that reads or writes base config, secrets, profiles,
  notification routes, or incident policies addresses one project. No path
  addresses them outside a project, so a client that writes v2's global
  entries gets an error and writes nothing.
- `/projects` lists the projects the caller can see. See
  [Lifecycle](#lifecycle) for create, display name changes, and delete.

#### MCP

- MCP tools that address DAGs, runs, secrets, profiles, notification routes,
  incident policies, base config, or wiki pages take `project`, with the
  default project as the default, as the API does.
- Wiki URIs are `dagu://wiki/{project}` and `dagu://wiki/{project}/{path}`.
- Specs 020 and 021 define the rest of each tool and URI.

#### Webhooks

- A per-DAG webhook URL is `/webhooks/{project}/{name}`. The URL selects the
  DAG, and the webhook's token or signature authenticates the request.
- `{project}` matches a registered project's name without regard to case.
  A URL that names no registered project gets `404`.
- `/webhooks/{name}` reaches the DAG that migration placed in a project
  under that name while that DAG exists, and otherwise the default
  project's DAG of that name.
- Neither URL reaches a library DAG: a request for one gets `404`.
- A project's webhooks, public ones included, belong to that project. Each
  is the webhook of one DAG name in that project, so two projects may each
  have a webhook for a DAG of the same name, each with its own token.
- A run that a webhook starts belongs to the project the URL reached.

#### Search

- Search reads the index of each project the caller can see, a few at a
  time, and merges the results.
- Search runs only when a caller asks. No server-wide index copies the
  projects' indexes.

### DAG Names

- A DAG is identified by its project and its name. Names are unique within
  a project, and two projects may each have a DAG of the same name.
- A DAG in a subdirectory of its project's DAG directory, such as
  `etl/daily.yaml`, is named `daily` when `dag_discovery.recursive` finds
  it.
- A DAG file moved by hand to another project's DAG directory starts over
  there with no runs. Its runs stay in the old project.

### Run Records

- The logs, step output, and artifacts of a run open from its project
  wherever that project's state directories are.
- After migration moves a run, its logs, step output, and artifacts open as
  before. No run record is rewritten.
- A path that a DAG sets outside its project's directories stays as the DAG
  set it and does not move.

### Access

- A grant is a user, group, or API key, a project, and a role below
  `admin`. Grants limit an API key to their projects, as they limit a user.
- A server-wide role applies to every project. A user or API key may have
  none.
- `admin` is server-wide only.
- A user or API key with no role in a project gets `404` for the project's
  DAGs, runs, logs, and wiki pages. The project appears in no list and no
  search result for it.
- Only admins grant users and groups roles in a project.
- API keys are server-wide. A user with the `manager` role in a project
  creates, edits, and deletes API keys whose grants all name projects in
  which that user is `manager`. A key with a server-wide role, or with a
  grant on a project in which the user is not `manager`, needs an admin.
- OIDC and proxy authentication read `project_mappings` and
  `default_project_access`, which have the shape of `workspace_mappings` and
  `default_workspace_access`. Until v3.1, they also read
  `workspace_mappings` and `default_workspace_access` as those keys, with a
  warning.
- Projects separate people who work through the UI, API, MCP, and CLI. They
  do not separate people who can write DAG files: whoever can change a
  project's DAG directory runs code with that project's secrets, and
  whoever can change the library runs code with the secrets of every
  project that calls it.

### Library

```text
projects/intraday    intraday DAG  ──dag.run──┐
                                              ├──▶ library: ABC
projects/day-ahead   day-ahead DAG ──dag.run──┘
                     each run is recorded in its caller's project
                     and uses that project's base config
```

#### Resolution

- `dag.run` resolves a name among the DAGs defined in the caller's file,
  then in the caller's project, then in the library. In the caller's
  project, a name resolves in its DAG directory, then among its files that
  run in place.
- A path target must lie inside the caller's project DAG directory or the
  library once symbolic links resolve, or be a file that runs in place as
  the caller's project's.
- A target in another project is a load error that names both projects. A
  target found in neither the caller's project nor the library is a load
  error that names the caller's project and the library.
- A `dag.run` inside a library DAG resolves in the same project, then the
  library.
- A project DAG shadows a library DAG of the same name: `dag.run` finds the
  project's DAG. A DAG defined in the caller's file shadows both.
- Every project can call the library. A library call is not a call across
  projects.

#### Runs of Library DAGs

- A library DAG runs in the project that starts it: the caller's project for
  `dag.run`, or the project given to an explicit start. Its run is recorded
  there, and it uses that project's base config, secrets, and profiles.
- A project keeps a library DAG's runs and DAG state apart from its own
  DAGs', in its [library directories](#terms). A project DAG added later
  under the same name starts with no runs and no DAG state, and the library
  DAG's runs in that project stay reachable.
- An explicit start of a library DAG records the whole run in the
  project's library directories: its run record, logs, artifacts, run work
  directory, process heartbeat, and queue item. A child of that run is
  recorded under it.
- A library DAG that `dag.run` starts is recorded under its caller's run,
  as every child is.
- A CLI command given a library DAG's file by path, such as `dagu status`,
  `dagu retry`, or `dagu stop`, reads and changes that library DAG's runs
  in the addressed project. Given a name, it addresses the project's DAG.
- DAG state (Spec 068) keeps the meaning of each scope within the project.
  A library DAG's name namespaces its state apart from a project DAG of the
  same name:

  | Scope | Namespaced by | Kept in |
  | --- | --- | --- |
  | `dag` | the DAG of the step | the library directories when that DAG is a library DAG, else the project's state |
  | `root_dag` | the DAG of the root run | the library directories when that DAG is a library DAG, else the project's state |
  | `global` | `with.namespace` | the project's state |
  | `custom` | `with.namespace` | the project's state |

  So a library DAG that `dag.run` starts shares `root_dag`, `global`, and
  `custom` state with its caller, and keeps its `dag` state apart from a
  project DAG of its name.
- API paths, URLs, and CLI commands address a library DAG in a project with
  `library` beside `project`.
- An explicit start of a library DAG is refused in a project that has a DAG
  of the same name.
- A library DAG that sets no `working_dir` has its file's directory as its
  DAG working directory (Specs 013 and 035), whichever project starts it.
- A library DAG's run is visible only to users with a role in the project
  that ran it.

#### Library Definitions

- Library DAGs are not listed among a project's DAGs and are not scheduled.
  Validation warns when a library DAG has a `schedule`.
- Every user can read library DAG definitions, through every project.
- Only admins change library DAGs through Dagu.

### Lifecycle

#### Create

- An admin creates a project in the UI or API, or with
  `dagu project create <name>`.
- The registry assigns the project's ID. Every process starts using the
  project without a restart, and the scheduler starts scheduling it.
- Dagu uses `<projects_dir>/<name>/` as the project's DAG directory, and
  creates it when it does not exist.
- A directory that already exists, such as one a git checkout at
  `<projects_dir>` brought, becomes a project only when an admin creates a
  project of its name. Its DAG files and its `base.yaml` then belong to the
  project.
- A new project has no secrets, profiles, notification routes, or incident
  policies, and the base config its directory holds, if any.
- Create is refused when the name breaks the [name rules](#names), when a
  project of that name exists in any case, and while a state directory of
  that name records another ID. The last refusal lasts until an admin
  restores or deletes that state directory on the projects page.
- Create is refused when the project's DAG directory would be, or contain,
  a [protected path](#protected-paths).

#### Protected Paths

The protected paths are each directory or file that another `paths`
setting names, whether set or defaulted, the default project's wiki
directory, `paths.base_config`, and `config.yaml`. No project's DAG
directory is or contains a protected path, so deleting a project never
removes another project's, the library's, or the server's files.

#### Display Name

An admin changes a project's display name in the UI or API at any time. See
[Display Names](#display-names).

#### Delete

An admin deletes a project in the UI or API, or with
`dagu project delete <name>`.

```text
admin: delete X
  │
  ├─ registry: mark X deleting
  │
  ├─ each live server, scheduler, and coordinator process:
  │    stop starting, admitting, and dispatching X's runs,
  │    close X's stores, record the release in its service registry entry
  │
  ├─ once every live process has recorded the release:
  │    X has a run that is not finished ──▶ refuse, remove the mark
  │    otherwise ──▶ remove X's DAG directory, its state directories,
  │                  its grants, each API key left with no role, and its
  │                  Dagu Cloud binding, then its registry entry
  │
  └─ a process that starts while X is marked deleting finishes the delete
```

Rules:

- While a project is marked deleting, no run of it starts.
- Delete removes nothing while a live process still uses the project's
  stores.
- A run that is not finished includes one queued in the project's own
  queues or in a queue defined in `config.yaml`.
- Delete is refused for the default project.
- Delete is refused while the project's DAG directory exists and is in a
  git checkout or contains one. The checkout owns those files, so they are
  removed there first.
- Delete is refused while the registry records a file that runs in place as
  the project's, whether or not the file exists now.
- Delete is refused while the project's DAG directory is or contains a
  [protected path](#protected-paths).
- A refused delete changes nothing. A mark it set is removed.

#### Changed on Disk

- A missing project is listed as missing and is not scheduled. It keeps its
  registry entry, grants, binding, and state until its DAG directory returns
  or an admin deletes it.
- A project whose DAG directory is gone but which has a file running in
  place that exists is not missing, and keeps running that file.
- A DAG directory renamed outside Dagu is not a project until an admin
  creates a project of its new name. The old project is missing.
- An unattached state directory is listed for admins to restore or delete.
  It is never attached to a project by its name.

#### The Default Project

- The default project cannot be deleted.
- An admin can change its display name.
- Its DAG directory is `<dags_dir>`, and its base config is the file at
  `paths.base_config`.
- Its wiki directory is the one v2 used: `paths.wiki_dir` when set, else
  the deprecated `paths.docs_dir` when set, else `<dags_dir>/docs` when
  that exists and `<dags_dir>/wiki` does not, else `<dags_dir>/wiki`.
- Its DAG files, DAG names, and URLs are those of the v2 install it came
  from.

### Scheduling

- One scheduler process schedules every project.
- A project created through any process starts scheduling without a
  restart. A project deleted, marked deleting, or missing is not
  scheduled.
- Pausing the scheduler pauses every project.
- Spec 085 applies to the scheduler process as a whole: one lock covers
  every project.
- A file that runs in place is scheduled in its project and in no other.
- Library DAGs are not scheduled.

### Queues

- A queue defined in `config.yaml` is server-wide. Runs from every project
  that use it wait in one store in `<data_dir>/server/`, and are admitted
  oldest first under the queue's one limit.
- Every other queue belongs to a project. Two projects that use a queue of
  the same name have two queues, each with its own limit.
- A queue page for a queue defined in `config.yaml` lists the runs of the
  projects the user can see. It counts the runs of the other projects
  without naming their projects or DAGs.
- No limit caps concurrent runs across every project. A queue defined in
  `config.yaml` limits the DAGs of every project that use it, and each
  worker's `max_active_runs` limits what it runs.

### Distributed Execution

- Dispatch tasks, leases, and status reports carry the project ID. A
  distributed run of a project DAG records its status in that project.
- Base config content, profile values, and secret values in a task come from
  the task's project.
- Every coordinator call that reads or writes a project's state carries the
  project ID: fetching a DAG, resolving a secret or profile, DAG state, run
  status and cancel requests, log and artifact streams, and agent session
  cleanups.
- A call without a project ID addresses the default project. The
  coordinator refuses such a call for a task of another project.
- A `dag.run` inside a distributed run resolves only in the task's project,
  then the library.
- A worker reads no project directory. It sees a project's files only through
  a step's `dependencies` (Spec 035).
- A worker's own `dags_dir` and `alt_dags_dir` serve only default-project
  tasks.
- A v3.0 coordinator gives a worker older than v3.0 only default-project
  tasks. A v3.0 worker does not connect to a coordinator older than v3.0.
  Each says why in its log, and the workers page shows it.
- A project's runs wait in their queues until a worker that can take them
  connects. Default-project runs keep running on older workers.

### Wiki

- A project's wiki is `wiki/` in its DAG directory. The default project's
  wiki is the directory [The Default Project](#the-default-project) names.
- A page's attachments are kept with the pages. A project other than the
  default project keeps its page revisions and page metadata in `wiki/` of
  its `state/` directory, so its DAG directory holds only pages and
  attachments. The default project keeps them where v2 did.
- A wiki link resolves in the page's project. Backlinks of a page list only
  pages of its project.
- A user without a role in a project gets `404` for its wiki pages.
- A project that migration made reads its wiki from its v2 directory until
  `<projects_dir>/<name>/wiki/` exists. See
  [Wiki of Migrated Projects](#wiki-of-migrated-projects).

### Audit

- An audit entry for an action in a project records the project's ID in
  `project_id` and its name at the time in `project`.
- An entry for a server-wide action records neither.
- Entries written before v3.0 keep their `workspace`.

### Git Sync

- `git_sync` in `config.yaml` syncs `<dags_dir>`, the default project's DAG
  directory.
- Every other project may have a git sync of its own, with the same pull,
  push, auto sync, and conflict handling as `git_sync`.
- Only an admin sets a project's repository, branch, path in the
  repository, and credentials, a token or an SSH key, in the UI or API.
- They are kept with the project's settings. The credentials are encrypted
  with the server's key, and no response returns them.
- Users who may edit the project's DAGs pull and push its files.
- A project's git sync keeps its clone in the project's state directory and
  copies files to and from the project's DAG directory. The DAG directory
  never holds `.git`.
- Several projects may sync one repository, each from its own path. Each
  keeps its own clone.
- Each file has at most one sync. When a project syncs a path of the
  repository that `git_sync` syncs, `git_sync` leaves that path out. When
  `<projects_dir>` lies inside `<dags_dir>`, `git_sync` leaves
  `<projects_dir>` out.
- A project's auto sync fetches its repository once per interval.

### User Interface

- The project selector lists the projects the user can see by display name.
  It appears only when the user can see more than one project.
- The name of a project appears only where people type it: CLI hints,
  `project_mappings`, and links.
- Creating a project asks for a display name and suggests a name from it,
  which the admin can change. When the display name has no ASCII letters or
  digits, the admin types the name. The dialog says that the name cannot
  change later.
- The access editor, the base config page, and the pages for secrets,
  profiles, notification routes, and incident policies work on one project.
- The projects page shows admins:
  - missing projects
  - unattached state directories, to restore or delete
  - the migration report
  - each file that runs in place, with a note that moving it changes its
    working directory
  - the warning that `<projects_dir>` and `<dags_dir>` are on different file
    systems
  - for a migrated project that has a `base.yaml` of its own, the global
    base config settings to add to it
- The Library page lists the library DAGs. Every user can read them; only
  admins change them.

### Migration

v3.0 migrates a v2 install on its first start, before it schedules or
serves anything. Every install migrates, with or without workspaces, since
the default project's state moves too.

#### First Start

- Every v2 server, scheduler, and coordinator is stopped before the first
  v3.0 start. Workers may keep running.
- The first v3.0 process to start for a `DAGU_HOME` migrates under a lock
  in `<data_dir>`. Other v3.0 processes wait for it. A worker, which keeps
  no state of its own, and a command that reads no state, such as
  `dagu version`, neither migrate nor wait.
- Migration waits while a v2 scheduler or coordinator is in the service
  registry, and logs what it waits for. A v2 server has no service registry
  entry, so migration cannot detect one; stopping it is the operator's
  step.
- It then cancels every run whose process is still live, as `dagu stop`
  does, and moves a run's files only after its process exits. Until every
  such process exits, it moves nothing, and the health endpoint names each
  run it waits for. A run on a worker is marked cancelled, and the
  coordinator stops it when the worker next connects.
- A process killed during migration resumes it on its next start.
- While migration waits or runs, the health endpoint of every v3.0 process,
  one that waits for another to migrate included, answers `200` and reports
  progress. Every other request gets `503`.
- Before it copies a state directory to another file system, migration
  checks free space there. When there is not enough, it moves nothing and
  waits: the health endpoint names the path and the space it needs, and
  migration starts once there is room. The process does not exit.
- Downgrading after migration is not supported. Going back to v2 means
  restoring `<data_dir>` and the DAG directories from a backup taken before
  the upgrade.

#### What Moves

| v2 | v3.0 |
| --- | --- |
| Workspace `X` | Project `X` in the registry, with a new ID |
| DAG labelled `workspace=X`, where `X` is a v2 workspace | Project `X`'s DAG. Its file stays where it is and runs in place. Its runs, logs, artifacts, DAG state, suspend flags, and DAG settings move to `X`'s state directories |
| DAG without a `workspace` label | The default project's DAG. Its file stays in `<dags_dir>`. Its runs, logs, and state move to the default project's state directories |
| On an install with workspaces, DAG whose `workspace` label is not a valid workspace name, has two values, or names no v2 workspace | Recorded, and does not load. Its file stays in `<dags_dir>`, and its runs and state move to the default project's state directories. See [Unsettled Labels](#unsettled-labels) |
| File with several documents | The first document decides the file's project. See [Files With Several Documents](#files-with-several-documents) |
| DAG under `paths.alt_dags_dir` | A library DAG, in place |
| Global base config (`paths.base_config`) | The default project's base config, at the same path |
| `<dags_dir>/workspaces/X/base.yaml` | Stays where it is. See [Base Config of Migrated Projects](#base-config-of-migrated-projects) |
| Secrets and profile entries of `X` | Project `X`'s secrets and profiles, still encrypted with the server's key |
| Notification routes and incident policies of `X` | Project `X`'s routes and policies |
| Global secrets, profiles, profile defaults, notification routes, and incident policies | The default project's. See [Settings of Migrated Projects](#settings-of-migrated-projects) |
| Notification channels, notification settings, and incident providers | Server-wide |
| Per-DAG wiki directory `<wiki>/X/<dag>`, and pages under `<wiki>/X/` | Project `X`'s wiki, read and written where it is |
| User or API key with roles in `X` | Grants on project `X` |
| User or API key with `workspace_access.all: false` | No server-wide role, and `viewer` on the default project |
| `workspace_mappings`, `default_workspace_access` | Read as `project_mappings` and `default_project_access` until v3.1 |
| `workspace` label | An ordinary label, apart from the two rules in [`workspace` Labels After Migration](#workspace-labels-after-migration) |

`<wiki>` is the default project's wiki directory.

Rules:

- Migration moves no DAG file, base config file, or wiki page, and edits
  none. It writes the registry and moves state.
- A DAG outside the library that ran on v2 runs on v3.0 from the same file,
  in the same working directory, under the same name, with the same base
  config, secrets, profile, notification routes, and incident policies.
- One exception is a DAG that v2 found through a symbolic link out of
  `<dags_dir>`. Discovery does not follow that link, so the DAG is not
  listed or scheduled until its file moves into a DAG directory. The
  preview and the report list it.
- The other exceptions are a DAG with an
  [unsettled label](#unsettled-labels), a later document of a
  [file with several documents](#files-with-several-documents), and a
  workspace DAG whose name a DAG in `<projects_dir>/X/` already has when
  migration runs. In the last case, the DAG in `<projects_dir>/X/` runs, as
  [Files That Run in Place](#files-that-run-in-place) says, and the preview
  and the report list the pair before and after the upgrade.
- A DAG under `paths.alt_dags_dir` that v2 scheduled because
  `alt_dags_dir` lay inside `dags_dir` is a library DAG and is not
  scheduled. Moving its file into a project schedules it again. The preview
  and the report list it.
- The default project's runs, DAG state, queues, suspend flags, DAG
  settings, and the rest of its state move from v2's directories in
  `<data_dir>`, and its logs from the default log directory, into its state
  directories.
- State moves by rename. A directory on another file system, such as one
  that `paths.queue_dir` placed, is copied.
- A v2 directory that is a symbolic link or a junction moves as a link.
  The state stays where the link leads, and the project's stores reach it
  through the link. When such a directory also holds state that stays, as
  the log directory holds the admin logs, the project's state moves within
  the link's target into `.projects/default/`, and the project's directory
  is a link to it.
- Server-wide state that v2 keeps beside project state stays where it is:
  the admin logs in the log directory, and the notification channels,
  incident providers, and the scheduler's lock, pause flag, and restart
  backoff beside the default project's routes, policies, and scheduler
  state.
- Runs waiting in a queue keep their order and move to their DAG's
  project's queue, those in a queue defined in `config.yaml` included. The
  release that admits queues defined in `config.yaml` across projects moves
  their items to `<data_dir>/server/`.
- A schedule slot gets the same run ID on v3.0 as on v2, so a slot that ran
  before the upgrade does not run again after it.
- Per-DAG webhook URLs keep their paths and tokens and reach the DAG in its
  project. See [Webhooks](#webhooks).

#### Files That Run in Place

- Each workspace DAG file runs in place. The registry records its path as
  project `X`'s. `X` lists, schedules, and runs it under the same name, with
  `X`'s settings, runs, and state. No other project reads it.
- Migration records only the files it finds. A file added later belongs to
  the DAG directory that holds it.
- The operator may move a file that runs in place to `<projects_dir>/X/` at
  any time, by hand or in git. The file then belongs to `X` by its
  directory, keeps its name and run history, and its record ends.
- While both files exist, the file in `<projects_dir>/X/` wins and the
  record ends. This also holds when the file in `<projects_dir>/X/` exists
  before migration runs.
- A record stays while its file is absent, as during a `git pull` or an
  editor's save that replaces the file. While the file is absent, the DAG
  is not listed or scheduled. When the file returns to the recorded path,
  it runs in place as `X`'s again.
- The projects page lists each record whose file is absent, and an admin
  can end it there. A record ends only when `X`'s DAG directory gets a DAG
  of the same name, or when an admin ends it.
- A DAG that sets no `working_dir` has its file's directory as its DAG
  working directory (Specs 013 and 035), so moving its file changes where
  it runs.
- `git_sync` keeps syncing `<dags_dir>`, the files that run in place
  included.

#### Base Config of Migrated Projects

- While project `X` has no `<projects_dir>/X/base.yaml`, it reads its base
  config in v2's order: the file at `paths.base_config`, then
  `<dags_dir>/workspaces/X/base.yaml`, then the DAG file. A change to the
  default project's base config then still reaches `X`.
- Once `<projects_dir>/X/base.yaml` exists, it alone is `X`'s base config.

#### Settings of Migrated Projects

- v2's global secrets, profiles, profile defaults, notification routes, and
  incident policies become the default project's.
- Project `X` also gets a copy of each global entry it inherited on v2:
  every global secret, profile, and profile default; the global routes when
  `X`'s route set has `inheritGlobal`; and the global policies when `X`'s
  policy set has `inheritParent`.
- An entry of `X`'s own with the same name stays as it is.
- Both inheritance flags are then dropped.
- A later change to a default-project entry does not reach its copies.

#### Wiki of Migrated Projects

- Project `X`'s wiki is `<wiki>/X/` until `<projects_dir>/X/wiki/` exists.
  Its pages keep their attachments, revisions, and page metadata.
- The default project's wiki leaves `<wiki>/X/` out, compared without regard
  to case. Its lists, search, and reads show no page there, and a write
  there, including a rename into or out of it, is refused as a conflict.
- Once `<projects_dir>/X/wiki/` exists, `X` uses it alone. The default
  project's wiki still leaves `<wiki>/X/` out.

#### Access After Migration

- Each user and API key can see and do what it could on v2, and no more,
  with three exceptions: every user can read a library DAG that carried a
  `workspace` label; a user limited to workspaces can read default-project
  DAGs through MCP, as through the UI on v2; and a user with a role in the
  default project can read the past runs of a DAG with an
  [unsettled label](#unsettled-labels). The report lists each of these.
- A workspace name that migration changed reads, in `workspace_mappings`,
  as its project's name.

#### Names of Migrated Projects

- A workspace whose name breaks the [name rules](#names), `default` and two
  names that differ only in case included, becomes a project with a valid
  name made from the workspace name. The project keeps the workspace name as
  its display name.
- When that name is taken, a number is added to it.
- No DAG name changes.

#### `workspace` Labels After Migration

- A `workspace` label places nothing. Apart from the two rules below, it is
  an ordinary label.
- A default-project DAG whose `workspace` label names a migrated project,
  by its name or by the v2 workspace name it came from, does not load, and
  the error names the project. Moving the file to `<projects_dir>/X/` or
  removing the label fixes it.
- A file that migration recorded for an
  [unsettled label](#unsettled-labels) does not load.
- Both rules read the first document of a file. A later document's
  `workspace` label is an ordinary label.

#### Unsettled Labels

A `workspace` label is unsettled when its value is not a valid v2 workspace
name, when it has two different values, or when it names neither a v2
workspace nor a project that migration makes, as after a typo. On v2, only
users with access to every workspace saw such a DAG.

- On an install that had at least one workspace, migration records in the
  registry each DAG file in `<dags_dir>` whose first document has an
  unsettled label. A recorded file does not load, and the error names the
  label. Loaded in the default project, the DAG would be visible to every
  user with a role there.
- An install without workspaces has no such records, since no v2 user
  there could be limited to a workspace.
- Only migration records such files. A file added or labelled later is an
  ordinary default-project DAG, unless its label names a migrated project.
- A record ends when the file's first document has no `workspace` label,
  when the file moves into a project's DAG directory, or when an admin
  ends it on the projects page. The DAG then loads where its file is. The
  record stays while the file is absent.
- A later document of a file is never recorded, and its label is ordinary.
- The preview and the report list each recorded file.

#### Files With Several Documents

On v2, a later document of a file loaded with its own `workspace` label's
base config. A file belongs to one project, so on v3.0:

- The first document decides the file's project: a migrated project when it
  carries that project's label, else the default project.
- A later document runs, as a child, in its caller's project with that
  project's base config, like any child in its caller's file.
- The preview and the report list each later document whose `workspace`
  label differs from the first document's, since its base config changes.

#### Report

When migration finishes, the server logs a report, and the projects page
shows it to admins. The report lists what moved and each item that needs
attention:

- each run that migration cancelled
- each project's files that run in place, and whether the project reads its
  base config in v2's order
- each global entry copied into a workspace's project
- each workspace whose name broke the name rules, with its project's name
- each `dag.run` target that lands in another project
- each queue not defined in `config.yaml` that DAGs in different projects
  share
- each library DAG and its static callers, since a library DAG gets the
  caller's project base config
- each DAG under `paths.alt_dags_dir` that v2 scheduled because
  `alt_dags_dir` lay inside `dags_dir`, and that is no longer scheduled
- each DAG that v2 found through a symbolic link out of `<dags_dir>`, and
  that is no longer listed or scheduled
- each library DAG with a `workspace` label, which every user can now read
- each `dag.run` target that contains a variable, which migration cannot
  resolve
- each DAG file recorded for an unsettled `workspace` label, which does not
  load
- each later document of a file whose `workspace` label differs from the
  first document's
- each workspace DAG whose name a DAG in `<projects_dir>/X/` already has,
  so the file in `<projects_dir>/X/` runs in its place

The text of the report is not part of this contract. For example:

```text
dagu: made 2 workspaces into projects
  billing        12 DAGs, base config, 3 secrets, 1 incident policy
  ops            4 DAGs; workspace "-ops", which is not a valid name
needs attention:
  dag.run        billing/report calls sim/score in another project
  global copy    secret DB_PASS copied into billing and ops
  cancelled      2 runs that were still running
```

#### Preview

- `dagu migrate workspaces` prints what the first start would do and
  changes nothing on disk. It can run against a live v2 install, or a copy
  of one.
- It shows the names and display names of the projects, the moves, each
  path that moves, and every item the report would list as needing
  attention. In place of cancelled runs, it lists the runs that are live
  when it runs.
- It also lists each [overlap of paths](#overlapping-paths) that would stop
  v3.0 from starting, and does not refuse to run because of one.

## Constraints

- Each server, scheduler, and coordinator process stats the registry once
  every 5 seconds, whatever the number of projects, and reads it only when
  it changed.
- A project with no restart policy runs no restart scanner, a project with
  no open backfill runs no backfill dispatcher, and a project with no DAG
  that watches a folder runs no folder watcher.
- The scheduler writes a project's watermarks only when one changed.
- One file watcher covers `<projects_dir>`, whatever the number of projects.
- Admitting a run to a queue defined in `config.yaml` reads that queue's
  store in `<data_dir>/server/` and no project's process heartbeats.
- Search reads one index per project the caller can see, only when a caller
  asks.
- A page subscribes only to the live updates of the project it shows. Live
  update topics of DAGs with the same name in two projects never mix.
- A project without git sync does no git work. A project with auto sync
  fetches its repository once per interval.
- Each project's caches keyed by DAG or run keep the bounds the default
  project's caches have.
- The project ID adds 36 bytes to each dispatch task, lease, status report,
  and audit entry. A run record gains nothing.
- Migration costs one rename per moved directory, not one write per stored
  run.

## Errors

### Names and Create

- Create with a name that breaks the [name rules](#names) is refused and
  changes nothing.
- Create with the name of an existing project, in any case, is refused.
- Create while a state directory of that name records another ID is
  refused until an admin restores or deletes that state directory.
- Create is refused when the project's DAG directory would be, or contain,
  a [protected path](#protected-paths).

### Delete

Delete is refused, and changes nothing, when:

- the project is the default project;
- the project has a run that is not finished;
- the project's DAG directory exists and is in a git checkout or contains
  one;
- the registry records a file that runs in place as the project's;
- the project's DAG directory is or contains a
  [protected path](#protected-paths).

### Choosing a Project

- A CLI command whose `--project` or `DAGU_PROJECT` names no registered
  project fails and changes nothing.
- A CLI command given by path a DAG file of a project other than the
  addressed one refuses it, and the error names the file's project.
- A command that serves every project fails when given `--project`.
- A command or request that would start a run in a project marked deleting
  is refused.
- An API request that names a project the caller cannot see, or one that
  does not exist, gets `404`.
- An API request for a DAG the project lacks gets `404`, and the error
  names the projects the caller can see that have a DAG of that name.

### Loading

- A `dag.run` target in another project is a load error that names both
  projects. The step fails.
- A `dag.run` target found in neither the caller's project nor the library
  is a load error that names the caller's project and the library. The step
  fails.
- A default-project DAG whose `workspace` label names a migrated project,
  by its name or by the v2 workspace name it came from, does not load, and
  the error names the project.
- A DAG file that migration recorded for an unsettled `workspace` label
  does not load, and the error names the label.
- An explicit start of a library DAG in a project that has a DAG of the
  same name is refused.
- A library DAG with a `schedule` loads with a validation warning and is
  not scheduled.

### Distributed Execution

- The coordinator refuses a call without a project ID for a task of a
  project other than the default project.
- A v3.0 coordinator gives a worker older than v3.0 no task of a project
  other than the default project. Both log why, and the workers page shows
  it.
- A v3.0 worker does not connect to a coordinator older than v3.0, and logs
  why.

### Startup

- When a DAG root overlaps state or another DAG root, as
  [Overlapping Paths](#overlapping-paths) lists, the process refuses to
  start and names the paths.
- When `<projects_dir>` and `<dags_dir>` are on different file systems, the
  server logs a warning.
- When `paths.queue_dir`, `paths.suspend_flags_dir`, or `paths.proc_dir`
  is set, the server logs a warning. It uses no path that only migration
  reads. See
  [Paths That Only Migration Reads](#paths-that-only-migration-reads).

### Migration

- A process with projects off on a `<data_dir>` that migration finished
  logs a warning at startup: it does not see the migrated state.
- While migration waits or runs, the health endpoint answers `200` with
  progress, and every other request gets `503`.
- When free space is short, migration moves nothing and waits, and the
  health endpoint names the path and the space it needs.
- A killed migration resumes on the next start.

## Examples

Each example starts from `DAGU_HOME=/srv/dagu`, with the default paths,
`paths.alt_dags_dir: /srv/dagu/library`, and these projects registered:
`default`, `intraday`, and `day-ahead`.

```text
/srv/dagu/
  base.yaml
  dags/
    cleanup.yaml
  projects/
    intraday/
      base.yaml
      forecast.yaml
    day-ahead/
      auction.yaml
  library/
    ABC.yaml
```

### Layout

The state directories are `/srv/dagu/data/projects/default/`,
`/srv/dagu/data/projects/intraday/`, and
`/srv/dagu/data/projects/day-ahead/`. Each records its project's ID and
name. Nothing is written under `/srv/dagu/projects/`.

With `paths.log_dir: /var/log/dagu`, the logs of `intraday` are under
`/var/log/dagu/.projects/intraday/`, and that directory records the ID and
name of `intraday`.

The library DAGs that run in `intraday` keep their runs in
`/srv/dagu/data/projects/intraday/library/`, and, with
`paths.log_dir: /var/log/dagu`, their logs in
`/var/log/dagu/.projects/intraday.library/`.

`forecast` runs with `/srv/dagu/projects/intraday/base.yaml`, then
`forecast.yaml`. `auction` runs with no base config. `cleanup` runs with
`/srv/dagu/base.yaml`, then `cleanup.yaml`.

### Choosing a Project in the CLI

| Current directory | Command | Addressed project |
| --- | --- | --- |
| `/srv/dagu/projects/intraday` | `dagu start forecast` | `intraday` |
| `/srv/dagu/projects/intraday` | `dagu start --project day-ahead auction` | `day-ahead` |
| `/tmp` | `DAGU_PROJECT=Intraday dagu start forecast` | `intraday` |
| `/tmp` | `dagu start cleanup` | `default` |
| `/srv/dagu/projects` | `dagu start cleanup` | `default` |

`dagu start --project day-ahead /srv/dagu/projects/intraday/forecast.yaml`
fails, and the error names `intraday`.

`dagu start --project nowhere cleanup` fails and starts no run.

### A Library DAG Called From Two Projects

```yaml
# /srv/dagu/projects/intraday/forecast.yaml
steps:
  - id: facility
    action: dag.run
    with:
      dag: ABC
```

```yaml
# /srv/dagu/projects/day-ahead/auction.yaml
steps:
  - id: facility
    action: dag.run
    with:
      dag: ABC
```

A run of `forecast` records a child run of `ABC` in `intraday`, which uses
`/srv/dagu/projects/intraday/base.yaml` and the secrets of `intraday`. A
state step of `ABC` with `scope: dag` keeps its value in the library
directories of `intraday`; one with `scope: root_dag` shares the value of
`forecast`'s run. A
run of `auction` records a child run of `ABC` in `day-ahead`. A user with a
role only in `day-ahead` sees the second child run and gets `404` for the
first.

`ABC` is not listed among the DAGs of `intraday` or `day-ahead`, and is not
scheduled.

### A Project DAG Shadows a Library DAG

After the operator adds `/srv/dagu/projects/intraday/ABC.yaml`:

- `forecast`'s `dag.run` of `ABC` runs the project's `ABC`, which has no
  runs and no DAG state.
- The runs of the library `ABC` in `intraday` stay reachable with `library`
  beside `project`.
- An explicit start of the library `ABC` in `intraday` is refused.
- `auction`'s `dag.run` of `ABC` still runs the library `ABC` in
  `day-ahead`.

### `dag.run` Across Projects

```yaml
# /srv/dagu/projects/day-ahead/auction.yaml
steps:
  - id: forecast
    action: dag.run
    with:
      dag: forecast
```

`forecast` is in neither `day-ahead` nor the library. The step fails with a
load error that names `day-ahead` and the library. The same target given as
`/srv/dagu/projects/intraday/forecast.yaml` fails with a load error that
names `day-ahead` and `intraday`.

### Names

- `dagu project create weather` creates `weather` with display name
  `weather`, and creates `/srv/dagu/projects/weather/`.
- Then `dagu project create Weather` is refused.
- `dagu project create -- -ops`, `dagu project create default`, and
  `dagu project create con` are refused by the name rules, and create no
  directory. Without `--`, the CLI reads `-ops` as flags.

### Overlapping Paths

- With `DAGU_HOME=/data`, `paths.data_dir: /data`, and no
  `paths.projects_dir`, `<projects_dir>` is `/data/projects`, which is
  `<data_dir>/projects/`. Every process refuses to start and names both
  paths.
- Adding `paths.projects_dir: /data/dag-projects` lets it start.

### Delete

- With a run of `auction` queued, deleting `day-ahead` is refused. The
  project keeps its directory, state, and grants, and is no longer marked
  deleting.
- When every run of `auction` has finished, deleting `day-ahead` removes
  `/srv/dagu/projects/day-ahead/`, `/srv/dagu/data/projects/day-ahead/`,
  the grants on `day-ahead`, and its registry entry. Each API key whose
  only role was a grant on `day-ahead` is removed.
- Deleting `default` is refused.

### Migrating a Workspace

A v2 install has workspace `billing` and these files:

```text
/srv/dagu/dags/
  cleanup.yaml               no workspace label
  invoices.yaml
  workspaces/billing/base.yaml
```

```yaml
# /srv/dagu/dags/invoices.yaml
labels:
  - workspace: billing
steps:
  - run: ./send-invoices.sh
```

After the first v3.0 start:

- The registry holds `default` and `billing`, each with a new ID.
  `billing`'s record names its v2 workspace and records
  `/srv/dagu/dags/invoices.yaml` as a file that runs in place.
- `invoices` is listed, scheduled, and run only in `billing`, under the
  name `invoices`, from `/srv/dagu/dags/invoices.yaml`. It runs with
  `/srv/dagu/base.yaml`, then `/srv/dagu/dags/workspaces/billing/base.yaml`,
  then `invoices.yaml`.
- The past runs of `invoices` are in `/srv/dagu/data/projects/billing/`, and
  their logs open as before. The past runs of `cleanup` are in
  `/srv/dagu/data/projects/default/`.
- No file under `/srv/dagu/dags/` changed.
- After the operator moves `invoices.yaml` to
  `/srv/dagu/projects/billing/invoices.yaml`, `invoices` keeps its name and
  runs in `billing`, and its record ends.
- After the operator adds `/srv/dagu/projects/billing/base.yaml`,
  `invoices` runs with that file, then its DAG file.
- A new file `/srv/dagu/dags/payroll.yaml` with the label
  `workspace: billing` does not load, and the error names `billing`.

### A Workspace Name That Breaks the Rules

A v2 workspace `-ops` becomes project `ops` with display name `-ops`. The
report and the preview both list it. `workspace_mappings` that name `-ops`
grant the same access on `ops` until v3.1.
