---
name: dagu
description: Writes, validates, and debugs DAG workflow definitions in YAML. Use when creating, editing, or troubleshooting DAGs.
---

# DAG Authoring

Load only the reference file that matches the task.

## Default Approach

- Prefer `type: graph` for new DAGs. It supports both sequential flow via `depends:` and parallel flow.
- Prefer `id` on every step. Omit `name` unless the display label must differ from the step ID.
- Prefer `dagu enqueue` over `dagu start` for agent-run workflows.
- Prefer `dagu schema ...` and `dagu validate ...` over guessing field names or shapes.
- Prefer `action: template.render` when generating text files, prompts, or artifacts instead of assembling them with shell `echo` or heredocs.
- Prefer `file.*` actions for local file operations such as stat, read, write, copy, move, delete, mkdir, and list instead of shelling out to `cp`, `mv`, `rm`, or `mkdir`.
- Prefer `stdout.artifact` / `stderr.artifact` when a command stream should become a DAG-run artifact, especially for large reports, JSON, Markdown, logs, or generated files.
- Prefer `artifact.*` actions for explicit artifact reads/writes/lists. Use `DAG_RUN_ARTIFACTS_DIR` only when a tool truly needs a filesystem path inside the step.
- Prefer string-form `output: VAR_NAME` for capturing small stdout values into flat variables.
- Prefer object-form `output:` when downstream steps need structured values via `${step_id.output.*}`.
- Prefer `stdout.outputs` or `action: outputs.write` when a DAG or remote action needs to return caller-visible values via `${step_id.outputs.*}`.
- Prefer temporary files in the artifacts dir only when downstream steps need file paths; otherwise let commands write large artifact content to stdout and attach it with `stdout.artifact`.
- Declare portable external CLI dependencies in top-level `tools` using aqua shorthand when the binary version affects reproducibility, for example `tools: ["jqlang/jq@jq-1.7.1"]`.
- For remote actions, put `tools` in the referenced action DAG file, not in `dagu-action.yaml`; caller DAG tools are not inherited across the action boundary.
- Do not add `tools` for CLIs that intentionally depend on user or worker preconfiguration, login state, local profiles, plugins, or credentials, such as `gcloud` and AI agent CLIs.
- Use remote action packages (`dagu-action.yaml`) when reusable logic needs helper files, its own DAG, versioning, or an input/output schema contract.

## High-Signal Rules

- `output:` has two modes:
  - string form captures trimmed stdout into a flat variable such as `${VERSION}`
  - object form publishes structured step-scoped output for `${step_id.output.*}` access
- `stdout.artifact` / `stderr.artifact` store command stdout/stderr directly as relative artifact paths, for example `stdout: {artifact: reports/report.md}`. Artifact outputs auto-enable artifacts unless `artifacts.enabled: false` is explicitly set, which is invalid.
- `${step_id.stdout}` is a log file path, not stdout content.
- `env:` should use list-of-maps when values depend on earlier env vars.
- `params:` values arrive as strings. The `params:` field supports JSON schema-like types and validation, check for schema to see how to specify types and validation rules.
- Do not assume `bash` for `run:` steps. If a script depends on a specific interpreter, add a shebang such as `#!/bin/sh` or `#!/usr/bin/env bash` only after checking that shell exists on the target host or container. Otherwise keep the script portable or set `with.shell:` explicitly.
- `parallel:` currently requires `action: dag.run` to a child DAG.
- Sub-DAGs do not inherit parent env vars; pass what you need via `params:`.
- For arbitrary text inside shell steps, prefer `printenv VAR_NAME` or `action: template.render` over `${VAR}` interpolation.
- DAG/action outputs are collected from string-form `output: VAR_NAME`, `stdout.outputs`, and `action: outputs.write`. Object-form `output:` stays step-scoped for `${step_id.output.*}` unless the workflow explicitly republishes values through `stdout.outputs` or `outputs.write`.
- Remote action packages define `dagu-action.yaml` with `apiVersion: v1alpha1`, `name`, `dag`, and optional `inputs`/`outputs` JSON Schemas. `inputs` validates caller `with:` before the action DAG starts; `outputs` validates the final action output object after the action DAG returns.
- Remote action manifests do not support `tools`. Declare external CLI tools in the action DAG itself so local and distributed workers prepare the right binaries for that action run.
- Object-form `output:` with `decode: json` or `decode: yaml` can act as lightweight runtime validation. Malformed data or an unresolved `select:` path fails the step, so normal `retry_policy` applies.
- Use `dagu schema dag` to check the full list of available fields and their shapes.
- Use `dagu example` to see different DAG patterns and how to express them in YAML.

## Example of Params, template step, and artifacts

```yaml
params:
  type: object
  properties:
    name:
      type: string
      maxLength: 50
    age:
      type: integer
      minimum: 0
      maximum: 120
    favorite_color:
      type: string
  required: [name, age]

steps:
  - id: render
    action: template.render
    with:
      data:
        name: ${name}
        age: ${age}
        favorite_color: ${favorite_color}
      template: |
        Hello, {{ .name }}!
        You are {{ .age }} years old.
        {{- if .favorite_color }}
        Your favorite color is {{ .favorite_color }}.
        {{- end }}
    stdout:
      artifact: greeting.txt
```

## Example of Large Command Output as Artifact

```yaml
steps:
  - id: report
    run: ./generate-report --format markdown
    stdout:
      artifact: reports/report.md
```

## Example of Reproducible External CLI

```yaml
tools:
  - jqlang/jq@jq-1.7.1

steps:
  - id: inspect
    run: jq --version
```

## Example of Object-Form Output

```yaml
steps:
  - id: inspect_build
    run: echo '{"version":"v1.2.3","artifact":{"url":"https://example.test/app.tgz"}}'
    output:
      # decode + select act as a lightweight contract check:
      # malformed JSON or a missing selected field fails the step.
      version:
        from: stdout
        decode: json
        select: .version
      artifact:
        from: stdout
        decode: json
        select: .artifact

  - id: publish
    depends: [inspect_build]
    output:
      versionLabel: "ver - ${inspect_build.output.version}"
      artifactUrl: "${inspect_build.output.artifact.url}"
```

## Example of Action Outputs

```yaml
steps:
  - id: classify
    run: ./classify.sh "${INPUT}"
    stdout:
      outputs:
        fields:
          label:
            decode: json
            select: .label
          confidence:
            decode: json
            select: .confidence

  - id: publish
    depends: [classify]
    action: outputs.write
    with:
      values:
        label: ${classify.outputs.label}
        reviewed: false
```

## Reference Guide

Load only the file you need:

- `references/steptypes.md` when choosing an action or checking executor-specific caveats such as `dag.run`, `parallel`, `jq.filter`, `file.*`, or `template.render`
- `references/dagu-action.md` when creating a reusable `dagu-action.yaml` package or checking action input/output schema behavior
- `references/cli.md` when you need command flags or lookup commands such as `dagu schema`, `dagu config`, or `dagu history`
- `references/env.md` when execution environment variables, `DAGU_*` config vars, or `params:`/`env:` resolution order matters
- `references/codingagent.md` only when the DAG itself runs AI coding agents as steps
