# Workflow page demo

Demo workflows that use the features of the Workflow page in the Web UI: the canvas, step outputs, runs of selected steps, repeats, schedules, groups, and webhooks. See the [Web UI documentation](https://docs.dagu.sh/overview/web-ui#try-the-demo-workflows).

## Set up

Copy the workflows into a new directory and start Dagu on it:

```sh
./setup.sh ~/dagu-demo
DAGU_HOME=~/dagu-demo dagu start-all
```

Then open `http://localhost:8080`.

Use a new directory, not the `DAGU_HOME` of a Dagu you use. `setup.sh` overwrites files with the same names, and running it again overwrites the copies. The build workflows work in `demo-build` next to the `dags` directory, which is `<data-dir>/demo-build`.

## Workflows

| Workflow | Shows |
| --- | --- |
| `graph-etl` | Graph steps that pass outputs to later steps. |
| `graph-router` | A router step and lifecycle handlers. |
| `reuse-costly` | Reuse of earlier outputs: run it once, edit `middle`, then run the last step alone. |
| `chain-report` | Chain steps that run in order. |
| `chain-backup` | Two schedules: the list shows the soonest and a +1 badge. |
| `always-running` | A step that repeats every ten seconds with no limit. Stop the run by hand. |
| `repeat-until` | A step that repeats until a check passes, at most six times. |
| `build-site`, `build-docs` | Build workflows that skip steps whose inputs did not change. |
| `webhook-intake` | The Webhook launcher, and a run that reads the webhook payload. |
| `cleanup-logs`, `disk-check`, `rotate-backups`, `daily-report`, `kpi-export`, `weekly-digest` | The `maintenance` and `reports` groups and the schedule column. Their steps only print the workflow name. |

## Schedules

These workflows run on their schedules once the scheduler loads them:

- `chain-backup`: every five minutes and daily at 03:30.
- `disk-check`: every 30 minutes.
- `cleanup-logs`: daily at 02:30.
- `daily-report`: daily at 07:00.
- `rotate-backups`: Sundays at 03:00.
- `weekly-digest`: Mondays at 09:00.

## Webhook

To start `webhook-intake`, create its webhook in the Webhook tab and send `{"payload": {"order_id": "A-1001"}}`. The `record` step prints `recorded order A-1001`.

## Agent tools

`agent-tools-support` is a support agent. It is a `chat.completion` step. Its key `dags` lists the DAGs that the model can call as tools. The step also lists two skills. It calls a model on `http://172.17.0.1:8099/v1`. Start the offline mock model with `python3 mock-llm/server.py 8099` (it needs no key), or change `base_url` and `model` in the workflow.

## Skills

A skill is a folder `dags/skills/<name>/SKILL.md`. The step of `agent-tools-support` offers the skills `tone-of-voice` and `refund-policy`. The system message lists each skill, and the model reads one with the tool `load_skill`. The mock model calls `load_skill` one time with the first skill name before it answers.
