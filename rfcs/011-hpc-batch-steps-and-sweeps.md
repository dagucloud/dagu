# RFC 011: HPC Batch Steps and Sweeps

- **Status:** Proposed
- **Date:** 2026-11-11
- **Affected areas:** `spec`, `ir`, `runtime/builtin` (new `slurm` executor,
  `foreach`, `dag` parallel), `runtime` (retry), `executor/registry`,
  `specs`, and `docs`

## Summary

Make Dagu fit the way researchers run experiments: many variants of one job,
submitted to a cluster scheduler, inspected, fixed, and rerun.

Three changes:

1. **A `slurm.run` action.** The step's command runs as a Slurm job. Dagu
   submits it with `sbatch`, follows it, streams its output into the step log,
   cancels it with `scancel` when the step stops, and takes the job's exit
   code. `pbs.run`, `lsf.run`, and `sge.run` follow with the same field
   vocabulary.
2. **Retry only the items that failed.** Retrying a `parallel` or `foreach`
   step reruns the items that failed or never finished and keeps the results
   of the items that succeeded. `parallel` already behaves this way in
   practice; the spec makes it a rule, and `foreach` gains it.
3. **A `matrix` item source.** `parallel` and `foreach` accept named value
   lists and run one item per combination, with `exclude` for combinations to
   skip.

```yaml
steps:
  - id: train
    action: dag.run
    with:
      dag: train-one
    parallel:
      matrix:
        lr: ["0.1", "0.01", "0.001"]
        seed: ["1", "2", "3"]
      exclude:
        - { lr: "0.1", seed: "3" }
      max_concurrent: 4

---
name: train-one
params:
  - name: lr
    type: string
  - name: seed
    type: string
steps:
  - id: fit
    action: slurm.run
    with:
      partition: gpu
      gpus: 1
      cpus: 8
      mem: 32G
      time: "12:00:00"
      command: python train.py --lr ${params.lr} --seed ${params.seed}
```

## Problem statement

A common research loop looks like this: write a training or simulation script,
run it with a set of parameter values on a cluster, look at the results, change
the script or the values, and run the batch again. Runs are started by a person
or an agent, not by a schedule. Jobs are mostly independent, and each can take
hours on a GPU node.

Dagu covers much of that loop already: a run history with logs per step, an
artifacts directory with previews, `dagu retry` that keeps a run's succeeded
steps, `dagu exec` for one-off commands, fan-out with `parallel` and
`foreach`, and an MCP server for agents. Three gaps make it a poor fit:

| Gap | What happens today |
| --- | --- |
| No cluster scheduler support | Compute on HPC systems is reached through Slurm, PBS, LSF, or SGE. A `run:` step can call `sbatch --wait`, but the job's output stays in Slurm's own file instead of the step log, nothing calls `scancel` when the step is aborted or times out, and the job's state and exit code are not shown |
| Retrying a sweep reruns too much | `foreach` reruns every item on retry (`Run` in `internal/runtime/builtin/foreach/foreach.go`). Spec 018 says per-item retry is not defined for either `parallel` or `foreach`. Rerunning 40 finished GPU jobs to fix 2 is not acceptable |
| No matrix | A sweep over two or three parameters needs a script that builds the item list, and skipping a few combinations means editing that list |

## Goals

1. A step can run as a Slurm job with its resources declared in YAML, and the
   run shows the job's state, output, and exit code like any other step.
2. Stopping a step, or its timeout, cancels the cluster job.
3. Retrying a fan-out step reruns failed and unfinished items only, and the
   step's aggregate output still covers every item.
4. A sweep over several parameters is declared without a generator script.
5. Nothing changes for DAGs that use none of these features, except the
   `foreach` retry behavior described under
   [Compatibility](#compatibility-and-rollout).

## Non-goals

- Running Dagu itself inside a cluster allocation, or managing allocations.
- Native scheduler arrays (`sbatch --array`). Each item submits its own job in
  this RFC. See [Open questions](#open-questions).
- Reusing results across separate runs, such as matching a new run's items to
  an older run's successes. Retry works within one run.
- Reading experiment metrics or comparing runs by metric. Experiment trackers
  do that.
- Reattaching to a cluster job after the Dagu process that submitted it dies.
  See [Failure behavior](#failure-behavior).

## Design

### Part 1: the `slurm.run` action

#### Where it runs

The step runs on a host where the Slurm client commands (`sbatch`, `squeue`,
`sacct`, `scancel`) work and that shares a filesystem with the compute nodes,
usually a login node. A distributed deployment routes the step there with
`worker_selector`. Submitting over SSH from another host is left for later.

#### Fields

```yaml
- id: fit
  action: slurm.run
  with:
    partition: gpu          # --partition
    account: lab-a          # --account
    qos: normal             # --qos
    time: "12:00:00"        # --time
    cpus: 8                 # --cpus-per-task
    mem: 32G                # --mem
    gpus: 1                 # --gpus
    nodes: 1                # --nodes
    ntasks: 1               # --ntasks
    constraint: a100        # --constraint
    job_name: fit           # --job-name, default dagu-<dag>-<step>
    extra_args: ["--exclusive"]
    poll_interval: 30s
    command: python train.py --lr ${params.lr}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `command` or `script` | Required, one of the two | The job's body, written to a batch script run by the step's shell |
| `partition`, `account`, `qos`, `time`, `cpus`, `mem`, `gpus`, `nodes`, `ntasks`, `constraint`, `job_name` | Unset, so the cluster's defaults apply | Map one-to-one to the `sbatch` option in the comment |
| `extra_args` | None | Further `sbatch` options, passed as separate arguments |
| `poll_interval` | `30s`, minimum `10s` | How often Dagu asks Slurm for the job's state |

The step's environment and working directory apply as for a `run:` step.

Validation rejects `extra_args` entries that Dagu manages: `--wait`,
`--parsable`, `--output`, `--error`, `--array`, `--chdir`, and their short
forms. The field set is declared as a closed JSON schema with
`RegisterExecutorConfigSchema`, as the Kubernetes executor does.

#### Lifecycle

The executor implements `Executor`, `ExitCoder`, and `StatusDetailsProvider`
(`internal/runtime/executor/executor.go`). It follows the shape of the
Kubernetes executor, which submits a Job, polls it, streams its logs, and
deletes it when cancelled (`internal/runtime/builtin/kubernetes/executor.go`).

1. **Submit.** Write the batch script into the step's work directory and run
   `sbatch --parsable --output=<path> --error=<path> --chdir=<dir> <options>
   <script>`. The output paths are files in the step's log directory, which
   must be on the shared filesystem. The command line is built as an argument
   list, never through a shell.
2. **Record.** Put the job ID in the step log and in the step's status
   details, so the UI and `dagu status` show it.
3. **Follow.** While the job is pending or running, poll
   `squeue -h -j <id> -o %T` every `poll_interval`, and copy new lines from the
   job's output and error files into the step's stdout and stderr as they
   grow. Show `PENDING` with its reason, and `RUNNING`, in the status details.
4. **Finish.** When the job leaves the queue, read
   `sacct -j <id> -n -P -X -o State,ExitCode`, copy the rest of the output
   files, and return.
5. **Stop.** On abort, step timeout, or `Kill`, run `scancel <id>`, wait up to
   30 seconds for the job to leave the queue, and return.

#### Exit codes and states

| Slurm state | Step result | Exit code |
| --- | --- | --- |
| `COMPLETED` | Succeeded | 0 |
| `FAILED` | Failed | The job's exit code from `sacct` (`N` of `N:S`) |
| `TIMEOUT` | Failed | 124, with "time limit reached" in the error |
| `OUT_OF_MEMORY` | Failed | 137, with "out of memory" in the error |
| `CANCELLED` by Dagu | Aborted | As for any aborted step |
| `CANCELLED` by someone else, `NODE_FAIL`, `PREEMPTED`, `BOOT_FAIL`, `DEADLINE` | Failed | 1, with the state in the error |

`retry_policy` matches exit codes through the error today, not through
`ExitCoder` (`shouldRetryNode` in `internal/runtime/runner.go`).
`shouldRetryNode` is changed to ask `ExitCoder` first, so that a policy such as
`retry_policy: {exit_code: [137]}` works for scheduler steps too.

#### Load on the scheduler

Slurm sites limit how often users query the controller. `poll_interval` has a
floor of 10 seconds and defaults to 30. A run with many items runs at most
`max_concurrent` jobs, and each polls on its own. Batching the queries of one
run into a single `squeue` call is an implementation option, not a contract.

#### PBS, LSF, and SGE

They follow as `pbs.run`, `lsf.run`, and `sge.run` with the same fields where
the scheduler has the concept, and `extra_args` for the rest:

| Action | Submit | Follow | Cancel |
| --- | --- | --- | --- |
| `pbs.run` | `qsub` | `qstat` | `qdel` |
| `lsf.run` | `bsub` | `bjobs` | `bkill` |
| `sge.run` | `qsub` | `qstat`, then `qacct` | `qdel` |

Each is a separate executor sharing the submission and log-following code.

### Part 2: retry only the items that failed

#### `parallel`

Each item of a `parallel` step is a child DAG run. Its run ID is derived from
the parent run ID, the step, the child DAG, and the item's parameters
(`GenerateSubDAGRunIDForTarget` in `internal/runtime/eval.go`). When the step
runs again in the same parent run, through `dagu retry` or the step's
`retry_policy`, a child whose recorded status is `Succeeded` returns that
result without running, and a failed child resumes as a retry of its own run
(`internal/service/coordinator/subflow/local.go`).

This RFC makes that behavior part of Spec 018:

- Retrying a `parallel` step reruns the items whose child runs did not
  succeed, and returns the recorded result of the items that did.
- The aggregate output lists every item in item order, reused or not.
- An item with `repeat_policy` gets a new child run on every repetition, as
  today, and is not reused.

No code change is expected beyond tests that pin the behavior.

#### `foreach`

`foreach` runs its item bodies in memory and writes per-item records under the
step's log directory for observability only. Nothing reads them back, so a
retry runs every item again.

The change:

1. **Item identity.** An item is identified by its `key` when the step sets
   one, and otherwise by a hash of its resolved value. The index is not used,
   so inserting an item does not change the identity of the others.
2. **Persist outcomes.** When the step ends, its node status records, for each
   item, the identity, the status, and the `collect` values. The record is
   bounded by the existing limit of 1,000 items.
3. **Reuse on retry.** When the step runs again in the same run, an item whose
   identity has a recorded `Succeeded` outcome is not run. Its recorded
   `collect` values go into the aggregate output, marked as reused in the
   status details. Every other item runs.
4. **Opt out.** A new field, `foreach.retry: all`, reruns every item, as
   today. The default is `failed`.

```yaml
- id: evaluate
  foreach:
    items: ${steps.list_checkpoints.outputs.checkpoints}
    key: ${foreach.item.name}
    retry: failed       # default; "all" reruns every item
    steps:
      - id: score
        run: python eval.py ${foreach.item.path}
```

### Part 3: the `matrix` item source

The object form of `parallel`, and `foreach`, accept `matrix` in place of
`items`:

```yaml
parallel:
  matrix:
    model: [small, base]
    lr: ["0.1", "0.01"]
  exclude:
    - { model: small, lr: "0.01" }
  max_concurrent: 2
```

Rules:

- `matrix` maps each name to a non-empty list of scalar values. Values are used
  as strings, as parameter values are.
- The items are the Cartesian product, in the order the names are written,
  with the last name varying fastest. The example yields `model=small,lr=0.1`,
  `model=base,lr=0.1`, `model=base,lr=0.01`.
- `exclude` removes every combination that matches all the name and value
  pairs of one entry. An entry naming a key or value that is not in the matrix
  is a validation error, so a typo cannot silently exclude nothing.
- For `parallel`, each combination becomes one child run with each name passed
  as a parameter. For `foreach`, each combination is an object item, so
  `${foreach.item.lr}` works in the body.
- The total number of combinations counts against the existing limit of 1,000
  items, checked after `exclude`.
- `matrix` and `items` cannot both be set.
- Names follow the parameter name rules.

Matrix items are ordinary items, so Part 2 applies to them: retrying a sweep
reruns the failed combinations only.

## Security

- The `slurm.run` executor builds every command as an argument list. Values
  from YAML and parameters never pass through a shell, except the job body,
  which is the step's own command or script, as for any `run:` step.
- `extra_args` cannot override the options Dagu depends on for output, working
  directory, and tracking.
- Jobs run as the user that runs Dagu, which is the user Slurm accounts them
  to. Dagu adds no privilege.
- Job output is written to the step's log directory, with the permissions of
  other step logs.

## Failure behavior

| Event | Behavior |
| --- | --- |
| `sbatch` fails, for example an invalid partition | The step fails with `sbatch`'s error, and no job exists |
| `squeue` or `sacct` fails temporarily | Retried at the next poll. After 10 consecutive failures the step fails, and the job is cancelled |
| The output files are not readable from the Dagu host | The step still finishes with the job's state and exit code, and the step log says the job output was not found and where it was expected |
| The step is aborted or times out | `scancel`, as in step 5 of the lifecycle |
| The Dagu process that submitted the job dies | The job keeps running on the cluster, and the run is marked failed or aborted the usual way. The job ID in the step log lets the user cancel or inspect it. Reattaching is a non-goal |
| A `foreach` item record from an earlier attempt is missing or unreadable | That item runs again |

## Compatibility and rollout

- `slurm.run`, `matrix`, `exclude`, and `foreach.retry` are additive.
- **Behavior change:** retrying a `foreach` step reruns only the items that did
  not succeed. A DAG that relied on every item running again sets
  `foreach.retry: all`. Release notes call this out.
- `parallel` keeps its current behavior, now specified.

Rollout:

1. **`matrix`** for `parallel` and `foreach`, with Spec 018 updated.
2. **`foreach` retry** and the Spec 018 rule for `parallel`, with conformance
   tests that fail one item and retry.
3. **`slurm.run`**, with a spec and tests that run against stub `sbatch`,
   `squeue`, `sacct`, and `scancel` commands on `PATH`.
4. **`pbs.run`, `lsf.run`, and `sge.run`**, in the order users ask for them.
5. **Documentation**: a guide for running parameter sweeps on a cluster.

## Verification and acceptance criteria

1. A `slurm.run` step submits one job with exactly the declared `sbatch`
   options, streams its output into the step log while it runs, and finishes
   with the job's exit code.
2. Each Slurm state in the table produces the stated step result and exit code.
3. Aborting the step, or reaching its timeout, calls `scancel` with the job ID.
4. `extra_args` containing a managed option fails validation.
5. `retry_policy` with `exit_code: [137]` retries a step whose job ran out of
   memory.
6. A `parallel` step with 10 items, of which 2 fail, reruns exactly those 2 on
   `dagu retry`, and its aggregate output lists all 10.
7. The same holds for a `foreach` step, and with `foreach.retry: all` all 10
   run again.
8. Inserting an item into a keyed `foreach` list does not change which of the
   other items are reused.
9. A 3 × 3 matrix with one excluded combination yields 8 items in the stated
   order, and an `exclude` entry with an unknown value fails validation.
10. A matrix larger than 1,000 combinations after `exclude` fails validation.

## Alternatives considered

- **Keep using `sbatch --wait` in a `run:` step.** Works for one job today,
  but the output stays outside the step log, the job is not cancelled with the
  step, and the state is invisible. Documentation alone cannot fix those.
- **Submit through SSH only.** Fits sites where Dagu cannot run on a login
  node, but adds connection handling to every poll. The action is designed so
  that an SSH transport can be added under it later.
- **Run a Dagu worker inside an allocation.** Suits long interactive
  allocations, but ties a worker's lifetime to the allocation and leaves
  queueing to the user.
- **A generic `batch.run` action with a `scheduler` field.** Smaller surface,
  but the schedulers differ in states, exit codes, and options enough that one
  schema would be loose. Separate actions sharing code keep each schema
  closed.
- **Match items across runs by fingerprint.** Would let a new run reuse an
  older run's successes, but needs a definition of "the same job" that holds
  across edits to scripts and data. Retry within one run covers the common
  case without it.

## Consequences

- HPC users can run sweeps from Dagu with the run history, logs, artifacts,
  retries, and agent access it already has.
- The Dagu process must run where the scheduler's client commands work, which
  some sites restrict on login nodes. The SSH transport left for later would
  remove that limit.
- Retrying a sweep costs only the failed items, in compute and in queue time.
- `foreach` retry changes default behavior, with an explicit way back.

## Open questions

1. Should items of a `parallel` or `foreach` step that all submit to Slurm be
   grouped into one native array job, to reduce scheduler load for large
   sweeps?
2. Should the SSH transport for scheduler steps come with the first release,
   for sites where Dagu cannot run on a login node?
3. Should `TIMEOUT` and `OUT_OF_MEMORY` get their own exit codes as proposed,
   or a status detail only, keeping the job's own exit code?
4. Should a `foreach` item with no `key` be identified by its value hash, as
   proposed, or should `key` be required for retry reuse?
5. Should the step show the cluster's queue position or estimated start time
   while a job is pending?
