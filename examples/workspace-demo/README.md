# Workspace demo workflows

Workflows that show the workflow workspace: a graph with outputs, a router, a slow step to test the reuse of earlier outputs, chains (one scheduled with two schedules), repeating steps, and two build workflows.

Run `./setup.sh <data-dir>` to copy them into a Dagu data directory. Running it again overwrites the copies. Start Dagu with `DAGU_HOME=<data-dir>`. The build workflows work in `demo-build` next to the `dags` directory, which is `<data-dir>/demo-build`.

`chain-backup` runs on its schedules once the scheduler loads it: every five minutes and daily at 03:30.

## Agent tools

`agent-tools-support` is a support agent. It is a `chat.completion` step. Its key `dags` lists the DAGs that the model can call as tools. The step also lists two skills. It calls a model on `http://172.17.0.1:8099/v1`. Start the offline mock model with `python3 mock-llm/server.py 8099` (it needs no key), or change `base_url` and `model` in the workflow.

## Skills

A skill is a folder `dags/skills/<name>/SKILL.md`. The step of `agent-tools-support` offers the skills `tone-of-voice` and `refund-policy`. The system message lists each skill, and the model reads one with the tool `load_skill`. The mock model calls `load_skill` one time with the first skill name before it answers.
