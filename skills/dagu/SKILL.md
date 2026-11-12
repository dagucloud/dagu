---
name: dagu
description: Authors, validates and troubleshoots Dagu workflow DAGs written in YAML, and operates Dagu through its CLI. Use when you create or debug a DAG file, choose a step action, write an agent or chat step with tools and skills, build a remote action package, run or inspect DAGs and runs with the dagu command, or set up a build workflow or an external agent harness.
---

# Dagu

Dagu runs workflows that are YAML DAGs. This skill gives short defaults and routes each task to one reference file.

## Procedure

1. Read the defaults below.
2. Open the reference file that matches the task (see the routes below).
3. Write or change the DAG.
4. Run `dagu validate <file>` and fix each error. Use `dagu schema dag` to read the fields and their shapes. Do not guess a field name.
5. Run `dagu example` to see DAG patterns.

## Defaults

- Use `type: graph` for a new DAG. It supports sequential flow with `depends:` and parallel flow.
- Use `type: agent` only when an LLM must choose the step order at run time. Use `type: build` only for local file pipelines with reuse.
- Give every step an `id`. Add `name` only when the display label must differ from the `id`.
- Prefer an action to a shell command. Examples: `file.*`, `js.run`, `template.render`, `state.*`, `artifact.*`, `xlsx.*`.
- Use scoped references for named values: `${params.NAME}`, `${env.NAME}`, `${consts.NAME}` and `${context.*}`.
- Treat `${step_id.stdout}` as a log file path, not as the content of stdout.
- Do not assume that `bash` exists. Set `with.shell` or a shebang only after you check the target host.

## Routes

- Rules, defaults, output forms and worked examples: `references/authoring.md`.
- Choose a step action or check its keys, such as `run`, `dag.run`, `parallel`, `human.task`, `chat.completion`, `agent.run`, `browser.*`: `references/steptypes.md`.
- Agent skills for `chat.completion` and `agent.run` (folder layout, front matter, `load_skill`, `read_skill_file`, the skills API): the section `Skills` of `references/steptypes.md`.
- Run, inspect, enqueue and clean DAGs from the command line: `references/cli.md`.
- Run metadata with `${context.*}` and declared step `outputs:`: `references/context.md`.
- A reusable `dagu-action.yaml` package: `references/dagu-action.md`.
- A `type: build` workflow, path references and reuse: `references/build.md`.
- Files of the working directory in a step: `references/file-dependencies.md`.
- External CLI agents with `harness.run`: `references/harnesses.md`.

`references/INDEX.md` lists every reference file with its use. Load only the file that the task needs.
