# Workspace demo workflows

Workflows that show the workflow workspace: a graph with outputs, a router, a slow step to test the reuse of earlier outputs, chains (one scheduled with two schedules), repeating steps, and two build workflows.

Run `./setup.sh <data-dir>` to copy them into a Dagu data directory. Running it again overwrites the copies. Start Dagu with `DAGU_HOME=<data-dir>`. The build workflows work in `demo-build` next to the `dags` directory, which is `<data-dir>/demo-build`.

`chain-backup` runs on its schedules once the scheduler loads it: every five minutes and daily at 03:30.
