# Spec 104: References to Agent Output

## Status

Not implemented.

No version of Dagu checks references to agent data yet.

## Scope

This spec defines:

- agent data: the values that an untrusted step controls
- the two forms in which a step may reference agent data
- when Dagu checks these references, and the errors
- what the check cannot see

This spec does not define:

- which steps are untrusted. See [Spec 103](103-untrusted-steps.md).
- the tools of an `agent.run` step and the key `with.dags`. See Spec 090 and Spec 091.

## Goal

An untrusted step controls every value it produces. A trusted step reads the files and the values of
an untrusted step as data: it must not run them as code, and must not let Dagu expand a secret into
them. One rule covers this: a step may reference agent data in exactly two forms, and Dagu rejects
every other reference. See [When Dagu checks](#when-dagu-checks).

```text
  untrusted step "code"               trusted step "gate"
  +--------------------+              +--------------------------------------------+
  | outputs.tool_calls |---- (1) ---->| env:                                       |
  +--------------------+              |   - TOOL_CALLS: ${steps.code.outputs...}   |
                                      | with: { shell: bash }                      |
                                      | run: printf '%s' "$TOOL_CALLS" | jq ...    |
                                      |                   (2)                      |
                                      +--------------------------------------------+

  (1) the whole value of an env entry: ${steps.<id>.outputs.<name>}
  (2) "$NAME" in a run that sh or bash runs on the Dagu host
  Every other reference to agent data is a validation error.
```

Agent data is never a gate. A gate is a check whose result Dagu decides, such as the exit code of
the repository tests.

## Terms

**Agent data.** These values:

- each output of an untrusted step, published or in its `output:` variable, such as the typed
  result and the `tool_calls` of an `agent.run` step. A step that is untrusted by default is an
  untrusted step.
- each output of a `foreach` step whose body has an untrusted step, and of a step that runs a
  sub-DAG with an untrusted step at any depth. A step whose sub-DAG Dagu cannot name when it loads
  the DAG counts as such a step.
- in the child run of a tool call of the agent (Spec 090), each param that the agent sets: each
  input that the `params` of its entry of `with.dags` do not fix
- each output of a step that refers to agent data, the variable of an `env` entry that refers to
  it, and each other variable that Dagu sets from it, such as `DAG_PARAMS_JSON`

`${<id>.exit_code}`, `${<id>.stdout}` and `${<id>.stderr}` are not agent data: Dagu records the exit
code and the paths of the log files.

**Untrusted step, trusted step, work directory.** Defined in
[Spec 103](103-untrusted-steps.md#terms).

## The two allowed forms

1. **An `env` entry.** The whole value of an entry of the step `env` is
   `${steps.<id>.outputs.<name>}`. Dagu inserts the value of this form once and expands nothing in
   it.

2. **A shell variable.** `"$NAME"` in the `run` of a step, where `NAME` is a variable that holds
   agent data. The step sets `with.shell`, or else the DAG sets the root `shell`, to `sh` or `bash`.
   The default shell depends on the host, so it does not qualify. Dagu must start the shell on its
   own host: a step that runs in a container, over SSH, on a node of the inventory or through
   another executor does not qualify, whichever key of the step or the DAG sends it there. Dagu
   leaves `"$NAME"` to the shell, which reads the value from the environment as one word.

## Rejected references

Every other reference to agent data is a validation error:

| Rejected | Examples |
| --- | --- |
| Any other field | `preconditions`, `repeat_policy`, `eval`, `with`, `params`, `working_dir` |
| Any other form | `$NAME`, `${NAME}`, `"${NAME}"`, `${env.NAME}`, `${params.NAME}`, `${NAME.<path>}`, `$NAME.<path>`, `${<id>.outputs}`, `${<id>.output}` with a path or a slice |

In most of these places, Dagu puts the value into a text that it builds, where a shell or a remote
host can read it as code, or a second expansion can replace a `$NAME` in it with a secret. The rule
refuses them all, so it does not depend on how each field, shell or host treats a reference.

## When Dagu checks

| What | When | A violation |
| --- | --- | --- |
| Each step of the DAG, handler steps included | When Dagu loads the DAG | The DAG does not load. |
| Each DAG of `with.dags` of an `agent.run` step | When the step starts | The step fails at its start. |
| The DAG of a tool call | When the call starts its child run | The call gets an error result, and no child run starts. |

Dagu checks an untrusted step too, because it resolves the fields of that step and runs its
preconditions and handlers as the worker user.

The error names the step, the field and the reference, and the DAG of a tool:

```text
step "gate": the field steps[2].run refers to ${steps.code.outputs.tool_calls}, data of the untrusted step "code": make it the whole value of an env entry, and read it as "$NAME" in a run that sh or bash runs on this host
```

A valid trusted step:

```yaml
  - id: gate
    depends: [code]
    untrusted: false
    env:
      - TOOL_CALLS: ${steps.code.outputs.tool_calls}
    with: { shell: bash }
    run: printf '%s' "$TOOL_CALLS" | jq -e 'index("shell") != null'
```

The step `gate` needs `untrusted: false`, because in an agent DAG a step with `run` is untrusted by
default ([Spec 103](103-untrusted-steps.md#default-of-the-key)).

## What the check cannot see

The check reads references, not values. It cannot see:

- **What a program does with a value.** `eval "$NAME"`, `sh -c "$NAME"` or `python3 -c "$NAME"`
  runs it, and a value that starts with `-` can be read as an option. Give the value to a program
  as data, on the standard input, as `gate` does.
- **A variable that a program reads by name.** An `output:` variable is in the environment of each
  step that depends on its step, at any depth, and of each handler step, with no reference. Some
  programs read a name as code or as a path to run, such as `BASH_ENV`, `PATH`, `LD_PRELOAD` or
  `GIT_SSH_COMMAND`. Give agent data a name that only this data uses.
- **Agent data that reaches a step with no reference.** The files of the work directory, the log
  files of an untrusted step, and each output of a step that reads them. For example,
  `git diff --name-only` prints a file named `$(id)` as it is. Dagu cannot tell that such an output
  is agent data, so the author reads it only in the two allowed forms.

### Checking the changed files

The author checks the changed files in a trusted step before a push. For example, a step refuses a
change below `.github/`. The check gives the protected paths to git as a pathspec. A check that
reads the paths of `git status --porcelain` as text misses a path that git quotes, such as a name
with a special character.

```yaml
  - id: check_paths
    depends: [verify]
    untrusted: false
    working_dir: repo
    run: |
      if [ -n "$(git status --porcelain -- .github/)" ]; then
        echo "the agent changed .github/" >&2; exit 1
      fi
```

## Errors

| Case | Result |
| --- | --- |
| A reference to agent data outside the two allowed forms, on any step | `dagu validate` rejects the DAG with a nonzero exit code. |
| A DAG of `with.dags` that refers to an input that the agent sets outside the two allowed forms | The step fails at its start, or the call gets an error result. |

## Limits

Dagu checks the references to agent data, not the data. A program that runs a value or reads a
variable by name, and a step that reads the files of the work directory, handle agent data that no
check sees. See [What the check cannot see](#what-the-check-cannot-see).

## Conformance

Black-box tests go in `conformance/spec104_agent_output/`. No test is there yet.
