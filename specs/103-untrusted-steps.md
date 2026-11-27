# Spec 103: Untrusted Steps and Step Secrets

## Status

Partially implemented.

One part is built: `dagu validate` rejects a secret `ref` that starts with `ai/`, as
[Spec 069](069-secrets-providers.md) also states.

No version of Dagu reads the other keys of this spec yet. `dagu validate` rejects the step keys
`secrets` and `untrusted`, and a configuration that sets the block `untrusted` has no effect.

## Scope

This spec defines:

- the step key `secrets`, step scope, and how a worker resolves a registry secret for one step
- the step key `untrusted` and its default
- the rules for an untrusted step, and its isolation as the restricted user
- the configuration block `untrusted`
- the protections that keep an untrusted step from reaching a later trusted step through `HOME`,
  `XDG_` folders or git

This spec does not define:

- the references that a step may make to agent output. See [Spec 104](104-agent-output.md).
- the `secrets:` block of a DAG and the secret providers. See
  [Spec 069](069-secrets-providers.md).
- the agent worker and its start checks. See [Spec 092](092-agent-workers.md).
- the action `agent.run`. See Spec 091.

## Goal

A run can mix code that the author trusts with code that an agent controls. In such a run, each
step gets only the secrets that it names. A step that runs agent-controlled code runs as a separate
Unix user, alone in its run. It cannot read the secrets, the client certificate or the configuration
of the worker, and it cannot change `.git`.

```text
                     worker process: the worker user
                     holds the secrets, the client certificate, the configuration
                                        |
              +-------------------------+--------------------------+
              |                                                    |
        trusted step                                        untrusted step
        - runs as the worker user                           - runs as the restricted user
        - gets the secrets it lists                         - gets no secret
        - keeps its DAGU_ variables                         - gets no DAGU_ variable except
                                                              DAGU_OUTPUT_FILE
        - runs next to other steps                          - runs alone in its run, and one
                                                              at a time in the process
```

## Terms

**Step that runs a process.** A step with `run`, `command` or `script`, or with the action
`harness.run`.

**Untrusted step.** A step that runs code that an agent wrote or controls. Dagu decides which steps
are untrusted by the key `untrusted` and its [default](#default-of-the-key).

**Trusted step.** Any other step of the run.

**Agent DAG.** A DAG that has an `agent.run` step. Dagu counts the steps of the DAG, the handler
steps and the steps in the body of each `foreach` step. A step of a sub-DAG does not count for the
parent.

**Restricted user.** The Unix user and group that run an untrusted step. The configuration keys
`untrusted.uid` and `untrusted.gid` set them.

**Worker user.** The user of the Dagu process that runs the step.

**Work directory.** The directory that `DAG_RUN_WORK_DIR` names.

**Registry secret.** A secret of the `secrets:` block with `ref`. A provider secret has `provider`
and `key`. See Spec 069.

**Step scope.** A DAG has step scope when it has an untrusted step or a step with the key
`secrets`. See [Step scope](#step-scope).

**Git folder.** A folder that git uses as repository storage:

- the folder that a `.git` file or a `.git` link names (`gitdir:`)
- the folder that the file `commondir` of that folder names
- a folder that git opens as a repository, with `HEAD`, `objects` and `refs`, also when `objects`
  or `refs` is a link, and also when it is the work directory

## Step secrets

### The key

A step can have the key `secrets`: a list of names from the `secrets:` block of the DAG.

```yaml
secrets:
  - name: GIT_TOKEN
    ref: git-token
steps:
  - id: push
    secrets: [GIT_TOKEN]
    run: git push origin HEAD
```

`dagu validate` rejects these cases:

- a name that the DAG does not declare. The error names the step and the name.
- a name that appears twice in one list
- `secrets` on an untrusted step, including a step with `untrusted: true`. The error names the
  step.
- `secrets` on a step that runs a process and has no key `untrusted`, in an agent DAG. The step is
  untrusted by default, and the author must write `untrusted: false`. See [Default of the
  key](#default-of-the-key).
- `secrets` in `defaults`. The error names the path `defaults.secrets` and tells the author to set
  the key on each step.
- `secrets` on a step in the body of a `foreach` step. The error has the path
  `<step>.foreach.steps[<i>].secrets`.
- a `ref` that starts with `ai/`. The AI feature of Spec 093 owns this prefix, and no DAG can read
  it.
- in a DAG with step scope, a reference `${NAME}` to a secret, in a field of a step that does not
  list `NAME`. The error names the step, the field and the name.

### Step scope

| DAG | A step gets |
| --- | --- |
| Without step scope: no untrusted step and no step with `secrets` | Every secret of the DAG, as in Spec 069. |
| With step scope | Only the secrets that its list names. A step with no list gets no secret. |

An agent DAG always has step scope, because its agent step is untrusted.

A secret that a step gets is a variable of the step process and a value for `${NAME}` in the fields
of the step. A secret that the step does not get is absent from both. Dagu masks the values of all
secrets of the run in every log and every record, as in Spec 069.

### Resolution

Dagu resolves the provider secrets when the run starts, as in Spec 069. A secret that fails stops
the run before the first step.

On a worker, a registry secret has two phases:

1. When the run starts, the worker asks the coordinator to check each registry secret, with no
   value. A secret that fails stops the run before the first step.
2. When a step that lists the secret starts, the worker asks the coordinator for the value. The
   request names the step.

```text
  worker                                       coordinator
    |                                               |
    |  run starts: check each registry secret       |
    |---------------------------------------------->|  returns no value
    |<------------------------------- ok or fail ---|  fail: the run stops before the first step
    |                                               |
    |  step "push" starts:                          |
    |  value of GIT_TOKEN for step "push"           |
    |---------------------------------------------->|  checks the three conditions below
    |<------------------------------------ value ---|  or PermissionDenied: the step fails
    |                                               |
    |  step "push" ends: GIT_TOKEN leaves the variables of every later process.
    |  The value stays in memory until the run ends, only for masking.
```

When a step ends, the worker removes its secrets from the variables of every later process. The
worker keeps the values in memory until the run ends, only to mask them.

The coordinator gives the value only when all these conditions are true:

- the lease of the attempt is valid, as for any secret
- the step is a step of the DAG of the attempt, and its `secrets` list names the secret
- the attempt has no final status, and the step has no final status in the reports of the worker

Otherwise the request fails with `PermissionDenied`, and the step fails. The error names the step
and the secret, and holds no value.

A process with no coordinator resolves a registry secret when the run starts.

## Untrusted steps

### The key

`untrusted: true` marks a step as untrusted, and `untrusted: false` marks it as trusted. The key is
valid only on a step that runs a process.

An `agent.run` step with a CLI provider is always untrusted. The key `untrusted` is redundant on
it, and `untrusted: false` on it is a validation error.

`dagu validate` also rejects `untrusted`:

- on a step with another action
- on a step with `container`
- in `defaults`. The error names the path `defaults.untrusted` and tells the author to set the key
  on each step.
- on a step in the body of a `foreach` step. The error has the path
  `<step>.foreach.steps[<i>].untrusted`.

### Default of the key

```text
  agent.run step with a CLI provider? -- yes --> untrusted
    | no
    v
  step that runs a process? ----------- no ---> not untrusted; the key is invalid on it
    | yes
    v
  step sets untrusted? ---------------- yes --> the value of the key
    | no
    v
  agent DAG? -------------------------- yes --> untrusted
    | no
    v
  trusted
```

In an agent DAG, the agent can change the repository files, and a step that runs a process runs
them. With this default, a step that the author forgot to mark runs as the restricted user and gets
no secret, instead of running agent code with the secrets and the rights of the worker. A DAG that
is not an agent DAG does not change: a step is untrusted only with `untrusted: true`.

The author makes a step trusted with `untrusted: false`. The step then runs as the worker user and
keeps its `DAGU_` variables.

The key `secrets` does not change the default:

| Agent DAG | `untrusted` | `secrets` | Result |
| --- | --- | --- | --- |
| Yes | absent | absent | Untrusted. Runs as the restricted user. |
| Yes | absent | listed | Validation error, shown below. |
| Yes | `false` | listed or absent | Trusted. Gets the listed secrets. |
| Yes or no | `true` | listed | Validation error: an untrusted step cannot have secrets. |
| Yes or no | `true` | absent | Untrusted. |
| No | absent or `false` | listed or absent | Trusted. Gets the secrets that [step scope](#step-scope) gives it. |

Dagu gives this error on the path `secrets` of the step:

```text
step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets
```

The author fixes it in one of two ways: add `untrusted: false`, and the step runs as the worker
user with its secrets; or remove `secrets`, and the step runs as the restricted user.

A trusted step of an agent DAG checks or publishes, such as the check of the changed files or the
push. It does not run a file of the repository. See [Spec 104](104-agent-output.md).

The default does not apply to a step with another action, such as `git.checkout`, `http.request`
or `chat.completion`. These steps run no process of the work directory. A step that runs a process
in the body of a `foreach` step cannot have the key, so in an agent DAG it is untrusted by default.

Untrusted steps run one at a time and alone in their run. In an agent DAG most steps are untrusted
by default, so the steps of the run execute one after the other.

Dagu computes the effective value once, when it loads the DAG. The runtime, the validation of
output references and the editor read this value. The editor shows it in the Security section of
the step.

### Rules in every process

These rules apply to every untrusted step, in every process:

- The step gets no secret.
- The step process gets no variable whose name starts with `DAGU_`, except `DAGU_OUTPUT_FILE`.

The preconditions, the command substitutions (`$(...)`), the capture of `output` and the handlers
of the step are trusted. Dagu runs them as the worker user, so the author does not run a file of
the work directory in them.

### Isolation

A process whose configuration sets `untrusted.uid` and `untrusted.gid` runs every untrusted step
with isolation. An agent worker always has isolation. A process with neither these keys nor
`untrusted.isolation: off` fails every untrusted step. See [Without isolation](#without-isolation).

The life of an untrusted step with isolation:

```text
  wait until no other untrusted step runs in this process
  wait until no other step of the run runs
        |
  check that the working directory is inside the work directory
        |
  give the work directory to the restricted user
  (except .git, git folders, and the folders on the way to them)
        |
  make a private HOME and TMPDIR (mode 0700)
        |
  run the process as the restricted user
  (no supplementary group, no capability, new session)
        |
  the step ends: success, failure, timeout or stop
        |
  SIGKILL every process of the restricted user; wait until none is left
        |
  check the work directory; remove what is not allowed
        |
  give the work directory back to the worker user; restore git folder modes
        |
  read the outputs of the step
```

The rules, in order:

1. **One at a time.** The process runs one untrusted step at a time. A step that waits for the end
   of the previous untrusted step has the status `running` and the log line
   `untrusted: waiting for the restricted user`. While an untrusted step runs, no other step of the
   same run runs:
   - The untrusted step waits for the running steps of its run to end. Its log has the line
     `untrusted: waiting for the end of the other steps of the run`.
   - Another step of the run waits for the untrusted step to end. This includes the preconditions
     and the repeat conditions of a step. Its log has the line
     `untrusted: waiting for the end of the untrusted step`.

2. **Place.** The working directory of the step MUST be inside the work directory of the run.
   Otherwise the step fails:

   ```text
   untrusted step "verify": the working directory /srv is not in the work directory of the run
   ```

   The restricted user MUST be able to pass each folder above the work directory (the bit x for
   others, or an ACL entry of the user). A folder of the worker user at `paths.dag_run_work_dir`
   or below it that it cannot pass, and that has no access ACL, gets the access ACL entry
   `user:<uid>:--x` until the step ends. Then the ACL is removed and the folder gets its mode back.
   No other user can pass these folders during the step. Dagu makes them with the mode `0750`. A
   file system without POSIX ACLs fails the step. Any other folder that the restricted user cannot
   pass fails the step:

   ```text
   untrusted step "verify": reach the work directory /data/run: the folder /data has the mode 0700: the restricted user cannot pass it
   ```

3. **Files.** Before the process starts, Dagu makes the restricted user the owner of every file and
   folder of the work directory, with these exceptions:
   - Each entry named `.git`, and everything in it, keeps the worker user as owner. The restricted
     user can read it and cannot write it.
   - Each git folder follows the same rule. A git folder outside the work directory fails the step.
   - The work directory, and each folder on the way to a git folder or to a `.git` entry, keeps the
     worker user as owner and gets the mode `1777`. The restricted user can create and remove its
     own entries there, and cannot rename or remove an entry of another owner.
   - Each Unix socket keeps its owner and its mode. A connect needs only write permission on the
     socket, which its owner can set, so the restricted user could drive the program that listens,
     such as a daemon that runs as root.

   A link that replaces a path while Dagu changes an owner or a mode cannot redirect the change. A
   change of owner would also give away the other paths of an entry with more than one link. So a
   file with more than one link, such as one that a package manager links from its cache, keeps its
   owner when its group is not the group of the restricted user, others cannot write it, it has no
   POSIX ACL, and it has no set-user-ID or set-group-ID bit. The restricted user cannot write it or
   link it. Any other file, link or FIFO with more than one link fails the step. In a git folder,
   such an entry fails the step only when Dagu must change its owner or its mode, or when it has a
   POSIX ACL. A device node fails the step, because the restricted user could open the device.

   Before it changes the first owner, Dagu makes the file `<work directory>.untrusted-handoff` next
   to the work directory. The restore of rule 8 removes it once the work directory is safe again.
   While the file exists, the next untrusted step fails before it gets the work directory, and every
   step, precondition and repeat condition of a run with an untrusted step fails and stops the run,
   in any process. So a process that stops during an untrusted step, or a restore that stops the
   run, leaves no work directory that a later step uses. The operator removes the work directory
   and the file.

   During the step, a work directory looks like this:

   ```text
   $DAG_RUN_WORK_DIR/           worker user, mode 1777   (on the way to .git)
   |-- verify_1.log             restricted user
   `-- repo/                    worker user, mode 1777   (on the way to .git)
       |-- .git/                worker user: readable, not writable
       |-- scripts/test.sh      restricted user
       `-- src/                 restricted user
   ```

4. **Home.** Dagu creates a new folder for the step, owned by the restricted user, with the mode
   `0700`. `HOME` and `TMPDIR` name it. Dagu removes it when the step ends.

5. **Process.** The step process runs with the user ID and the group ID of the restricted user. It
   has no supplementary group and no capability. It runs in a new session, with no controlling
   terminal. It gets no descriptor of the worker but 0, 1 and 2, also none that the worker
   inherited from the process that started it.

6. **Files of the attempt.** The MCP config of the attempt (Spec 091) holds the secret of the tool
   bridge. The restricted user owns it, with the mode `0400`. The restricted user can read and
   cannot write the skills folder (Spec 091) and the script of the step, and can run the script.
   The script keeps the worker user as owner and gets the group of the restricted user with the
   mode `0550`, so no other user can read it. The restricted user can write the file of `DAGU_OUTPUT_FILE`.

7. **End.** The step ends after a success, a failure, a timeout or a stop. Dagu then sends
   `SIGKILL` to every process of the restricted user: every thread group that has a live task of
   that user, including a group whose leader is a zombie. Dagu waits until no live task of that
   user is left. Then it makes the worker user the owner of every file and folder of the work
   directory again, restores the mode of each entry of a git folder, and reads the outputs of the
   step. The host MUST set `kernel.yama.ptrace_scope` to 1 or more, so that no process of the
   restricted user can trace the process that sends this `SIGKILL`. Otherwise an untrusted step
   with isolation fails before it starts.

   Before the end, when a root process of the step ends while no other root process of the step
   runs or starts, Dagu also sends `SIGKILL` to every process of the restricted user. So a process
   that the root process left cannot keep the output of the step open, and the step ends. This
   happens before the next root process of the step starts, as between the commands of a step
   with several commands. A root process of the step starts only when Dagu can watch it, and,
   when no other root process of the step runs, only once no process of the restricted user is
   left. Otherwise its start fails.

8. **Checks at the end.** Dagu checks the work directory before it gives it back. It fails the step
   and removes the entry for each of these entries of the restricted user:
   - a new `.git` entry, or a new entry in a git folder
   - a new folder that git opens as a repository, the work directory included. Dagu removes its
     files `HEAD`, `commondir` and `gitdir`.
   - a link to a path outside the work directory or into a git folder, judged both by the text of
     its target and by the path it reaches through other links. An entry that does not exist yet
     counts as a folder.
   - an entry other than a folder with more than one link, such as a link with a second path in a
     folder of mode `1777` of root, where root would follow it once it belongs to the worker user

   Dagu removes the HOME of the step before these checks, so a file that the step linked from its
   HOME, such as one of a package cache, has one link left and stays.

   A link that the handoff gave and that keeps its path and its target is a link of the worker
   user and stays, such as a link of a Python virtual environment to the interpreter of the host.
   The restricted user can move such a link, and at its new path the link counts as its own.

   A file of the restricted user loses its set-user-ID and set-group-ID bits. An entry of the
   restricted user loses its POSIX ACLs, so that a default ACL does not pass to a `.git` that a
   later trusted step makes.

A failure of any action of this list fails the step. The error names the action and the path.

These failures also stop the run, because Dagu cannot give a safe work directory to a later step:

- A task of the restricted user is still alive 30 seconds after `SIGKILL`.
- An entry of a git folder, or a `.git` entry, changed or is gone. Another path of a file with
  more than one link changes its link count and its times, as another clone of the repository
  does, so these do not count for such a file.
- Dagu cannot read a part of the work directory, cannot remove an entry of rule 8, or finds a tree
  more than 256 folders deep.

After a failure of the first two kinds, every later step of a run with an untrusted step fails in
the same process. The restricted user can cause a failure of the last kind by itself, so it stops
only the runs on that work directory, through the file of rule 3.

### The restricted user

The configuration keys `untrusted.uid` and `untrusted.gid` set the restricted user. The key
`untrusted.isolation` turns isolation off.

```yaml
untrusted:
  uid: 10001
  gid: 10001
```

| Key | Type | Rule |
| --- | --- | --- |
| `untrusted.isolation` | `on` or `off` | Default `on`. With `on`, an untrusted step needs `untrusted.uid` and `untrusted.gid`. With `off`, it runs without isolation. `off` with `untrusted.uid` or `untrusted.gid` is an error. |
| `untrusted.uid` | integer | Required with `untrusted.gid`. From 1 to 4294967294. Not the user ID of the process. |
| `untrusted.gid` | integer | Required with `untrusted.uid`. From 1 to 4294967294. Not the group ID of the process. |

A value out of range, one key without the other, a value of the wrong type, or
`untrusted.isolation: off` with a restricted user stops Dagu at start. The error names the key, the
expected range and the value. The user needs no entry in `/etc/passwd`. Only untrusted steps use
this user ID.

Each process that runs untrusted steps MUST have its own restricted user. Two processes may share a
restricted user only when each has its own PID namespace, with its own `/proc`, and neither sees the
work directories of the other. Two containers that share no PID namespace and no volume meet this
rule. The rule [one at a time](#isolation) holds within one process only. Two processes that share
a restricted user and break this rule could kill each other's untrusted step at its end, and an
untrusted step of one could reach the work directory of the other.

The block `untrusted` is read from the configuration file only. If the variable `DAGU_UNTRUSTED`
exists in the environment, Dagu stops at start. The error names the variable and the key, and never
shows the value:

```text
config: the environment variable DAGU_UNTRUSTED is not read: set the block untrusted in the configuration file
```

`dagu config` prints the three values.

### Without isolation

Isolation fails closed. Without `untrusted.uid` and `untrusted.gid`, Dagu does not run an untrusted
step as the worker user, because the agent controls the code of the step. The step fails before it
starts, with the reason `isolation_required`, and the message names the keys:

```text
untrusted step "verify": isolation_required: the configuration sets no untrusted.uid and no untrusted.gid, and untrusted.isolation is on: set untrusted.uid and untrusted.gid, or set untrusted.isolation to off for development
```

This failure is an error of the step, not a failure kind of an attempt. It applies to every
untrusted step, including a step that is untrusted by default and an `agent.run` step.

A process with `untrusted.isolation: off` runs an untrusted step as the worker user. The [rules in
every process](#rules-in-every-process) still apply. At start, the process logs a warning that names
the key `untrusted.isolation`. The step log has the line:

```text
untrusted: isolation is off: the key untrusted.isolation is off
```

This is the mode for development on a host, and for the conformance tests that run without a
restricted user. An agent worker does not start with `untrusted.isolation: off`:

```text
--agent: key "untrusted.isolation" is "off", not "on": an agent worker always has isolation
```

### Protections for later trusted steps

An untrusted step can leave files behind that a later trusted step reads. Dagu closes these paths:

- **`.git`.** The restricted user cannot write, rename or replace `.git` or a git folder. See rule 3
  of [Isolation](#isolation).
- **`HOME` and `XDG_` folders.** With isolation, the restricted user may be able to write the `HOME`
  of the worker or an `XDG_` folder, its default below `HOME` included, or replace one through a
  folder or a link on the way. A folder with an access ACL counts as one it can write. Then each trusted step of the run gets a new private `HOME` and private `XDG_`
  folders, with the mode `0700`, owned by the worker user. Dagu removes them when the step ends. A
  temp folder of the worker that would let the restricted user replace the private `HOME` fails
  the step. The steps of a child run
  that an untrusted step started get them too, and the git setting below. The agent cannot plant a
  file that a later trusted step reads, such as `.gitconfig`.
- **Bare repositories.** In a run with an untrusted step, every process gets the git setting
  `safe.bareRepository=explicit`. Dagu adds it through `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_<n>` and
  `GIT_CONFIG_VALUE_<n>`, after the entries these variables already have. Git reads
  `GIT_CONFIG_PARAMETERS` after them, so when that variable is set, Dagu also adds the setting at its
  end. So git opens a bare repository only when the command names it.

## Errors

`dagu validate` rejects these cases with a nonzero exit code:

- a name in a step `secrets` list that the DAG does not declare, or that appears twice
- `secrets` on an untrusted step, including `untrusted: true` with `secrets`
- `secrets` on a step that runs a process and has no key `untrusted`, in an agent DAG
- a secret `ref` that starts with `ai/`
- a reference `${NAME}` to a secret that the step does not get, in a DAG with step scope
- `untrusted` on a step that does not run a process, on a step with `container`, or in `defaults`
- `secrets` in `defaults`
- `untrusted` or `secrets` on a step in the body of a `foreach` step
- `untrusted: false` on an `agent.run` step with a CLI provider

These cases stop a process at start with exit code 1:

- an invalid `untrusted.uid`, `untrusted.gid` or `untrusted.isolation`, or `untrusted.isolation:
  off` with a restricted user
- the variable `DAGU_UNTRUSTED` in the environment
- `untrusted.isolation: off` on an agent worker

These cases fail a step or a run:

| Case | Result |
| --- | --- |
| A request for a registry secret from a step that does not list it, or after the step ends | `PermissionDenied`. The step fails. |
| An untrusted step whose working directory is outside the work directory | The step fails. |
| A failed action of [Isolation](#isolation), such as a change of owner | The step fails. The error names the action and the path. |
| An untrusted step in a process with no restricted user and no `untrusted.isolation: off` | The step fails with the reason `isolation_required`. |
| A task of the restricted user alive 30 seconds after `SIGKILL`, or a changed or missing git entry | The step fails and the run stops. Every later step of a run with an untrusted step fails in the same process. |
| A step of a run with an untrusted step whose work directory has the file `<work directory>.untrusted-handoff` | The step fails and the run stops, in any process. |

## Limits

- An untrusted step shares the kernel with the worker. A kernel flaw can break the isolation.
- A trusted step that runs a file of the work directory runs agent code.
- Any user can read the arguments of a process in `/proc/<pid>/cmdline`. No process of the
  restricted user runs at the same time as a trusted step of its run, but a process of another run
  can read them. Pass a secret in the environment, never in an argument.

## Open items

Each item has a default. The default applies until the owner decides.

| Item | Default |
| --- | --- |
| The name of the key `untrusted`. | As in this spec. |

## Conformance

Black-box tests go in `conformance/spec103_untrusted_steps/`. No test is there yet.

The built part has this test:

| Test | Proves |
| --- | --- |
| `TestSecretValidation` (`conformance/spec069_secrets/`) | `dagu validate` rejects a secret `ref` that starts with `ai/`. |

A test that switches to the restricted user needs Linux, user ID 0 and the no-new-privileges flag.
It skips on other hosts. Run these tests in a root container with the no-new-privileges flag.
