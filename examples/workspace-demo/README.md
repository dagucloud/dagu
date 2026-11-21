# Workspace demo workflows

Workflows that show the workflow workspace: a graph with outputs, a router, a slow step to test the reuse of earlier outputs, chains (one scheduled with two schedules), repeating steps, two build workflows, and a chain that a webhook starts. Six more chains fill the `maintenance` and `reports` groups and the schedule column; their steps only print the workflow name.

Run `./setup.sh <data-dir>` to copy them into a Dagu data directory. Running it again overwrites the copies. Start Dagu with `DAGU_HOME=<data-dir>`. The build workflows work in `demo-build` next to the `dags` directory, which is `<data-dir>/demo-build`.

These workflows run on their schedules once the scheduler loads them:

- `chain-backup`: every five minutes and daily at 03:30.
- `disk-check`: every 30 minutes.
- `cleanup-logs`: daily at 02:30.
- `daily-report`: daily at 07:00.
- `rotate-backups`: Sundays at 03:00.
- `weekly-digest`: Mondays at 09:00.

To start `webhook-intake`, create its webhook in the Webhook tab and send `{"payload": {"order_id": "A-1001"}}`. The `record` step prints `recorded order A-1001`.
