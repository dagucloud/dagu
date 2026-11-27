# Dagu References

Each file lists a contents block at its top when it has more than 100 lines. Open only the file that the task needs.

- `authoring.md`: Rules for writing DAG YAML. Read it before you write or review a DAG. It holds the defaults, the forms of `output:`, params, artifacts, tools and human input, with examples.
- `steptypes.md`: Every step type and action with its keys. Read it to choose an action or to check one. It includes `chat.completion`, `agent.run` and the section `Skills` (folder layout, front matter, tools, limits, API).
- `cli.md`: Every `dagu` command and flag. Read it to run, inspect, enqueue, stop or clean DAGs and runs, and to work a page in a browser session.
- `server-operations.md`: Operating a running Dagu server through MCP, REST or a remote CLI context. Read it when the task targets a server: reading or changing DAGs, starting, stopping or retrying runs, or checking run status and logs.
- `context.md`: The `${context.*}` references, scoped values and declared step `outputs:`. Read it when a step needs run metadata or publishes values.
- `dagu-action.md`: Remote action packages with `dagu-action.yaml`. Read it to make a reusable action with its own DAG and input and output schemas.
- `build.md`: Workflows of `type: build`, path references, reuse and `--no-reuse`. Read it for local file pipelines.
- `file-dependencies.md`: The step-level `dependencies` key. Read it when a DAG needs files from its working directory.
- `harnesses.md`: External coding-agent CLIs with `harness.run`. Read it only when a DAG calls such a CLI.
- `coming-from-rundeck.md`: Moving from Rundeck. Read it to move Rundeck nodes, node filters or jobs that dispatch to nodes to Dagu inventories and steps with `nodes`.
- `coming-from-ansible.md`: Moving from Ansible. Read it to move Ansible inventories, plays, `serial`, `forks` or `--limit` to Dagu inventories and steps with `nodes`.
