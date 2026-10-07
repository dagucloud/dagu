# Spec: Template Action

## Status

Partially implemented.

Conformance covers inline and referenced templates, data resolution, stdout
and file output, and representative validation/render errors. Function
permutations and filesystem failure cases belong in executor tests.

This spec defines conformance behavior for the built-in `template.render`
action.

## Scope

This spec covers:

- `with.template` and `with.template_ref`: exactly one is required;
  `with.template` supplies the template text directly, `with.template_ref`
  names a complete scoped value reference (such as `${env.NAME}`) whose
  resolved value is used as the template text instead
- that the template text itself -- whether from `with.template` or
  resolved from `with.template_ref` -- is never run through Dagu's own
  `${...}` value substitution, unlike an ordinary `run:` script body;
  only Go's `{{ }}` template syntax is evaluated against it
- `with.data`: an object of pipeline data, resolved the same way any
  other `with:` field is (env var substitution, and so on) before being
  passed to the template as `.`
- `with.output`: a file path to write the rendered result to, instead of
  stdout, creating missing parent directories
- deterministic template functions and pipeline-friendly argument ordering
- `missingkey=error` behavior: referencing an undeclared data key fails
  the step
- build-time parsing of `with.template` text, including the hint given
  when a `with.data` key is written without its leading dot
- validation and runtime errors

This spec does not define:

- the full template function catalog
- the value-reference syntax itself (`${env.NAME}`, `${steps.*.outputs.*}`,
  and so on) -- see [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- direct `type: template` authoring

## Goal

Workflow authors render text (configuration files, messages, generated
scripts) from structured data as a DAG step, using a template language,
without shelling out to a separate templating tool.

## Behavior

### Template source

Exactly one of `with.template` or `with.template_ref` is required.
`with.template` is the template text itself. `with.template_ref` is a
value reference that must be the entire field value (for example,
`${env.NAME}`); its resolved value becomes the template text. A
reference embedded in surrounding text (for example, `"prefix
${env.NAME} suffix"`) is rejected -- the field must be exactly one
reference.

### No double substitution

The template text is handed to Go's `text/template` engine unmodified;
Dagu does not apply its own `${...}` substitution to it first. This
differs from an ordinary `run:` script body, which does substitute
`${...}` references before executing. As a result, a template can
freely emit literal `${...}`-shaped text (for example, to generate
another workflow's YAML) without it being consumed by Dagu first.

`with.data`, by contrast, is resolved the same way any other `with:`
field is -- a bare `$VAR` or `${VAR}` inside a `with.data` value
resolves normally -- before being passed to the template as `.`.

### Parsing

Inline `with.template` text is parsed when the DAG is built, so a `{{ }}`
syntax error is reported by `dagu validate` and rejects the DAG before any
step runs. `with.template_ref` text is only known at run time and is parsed
when the step runs.

Template data is addressed through the dot: `{{ .key }}`. A bare
`{{ key }}` is a function call in Go templates. When the parse error names
an undefined function whose name is a `with.data` key, the error adds a
hint that the key needs a leading dot.

### Rendering

The template is executed with `missingkey=error`: referencing
a data key that is not present in `with.data` fails the step, rather
than rendering an empty value. The rendered result is written to stdout,
or, when `with.output` is set, to that file path (resolved relative to
the step's working directory when relative), creating any missing
parent directories first.

### Template functions

Template functions support deterministic rendering from template text and
`with.data`. Pipeline functions take the pipeline value as their last
argument. The full function catalog is outside this conformance scope.

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- Neither `with.template` nor `with.template_ref` set, or both set: an
  error containing `"requires exactly one of with.template or
  with.template_ref"`.
- `with.template_ref` set to a value that is not exactly one complete
  scoped reference: an error containing `"must be one complete scoped
  value reference"`.
- `with.template` text with invalid `{{ }}` syntax: an error containing
  `"template: parse error"`. When the undefined function name is a
  `with.data` key, the error also contains `is a with.data key and needs a
  leading dot`.

### Runtime

- A template referencing a data key that is not present in `with.data`:
  an error containing `"map has no entry for key"`.
- A `with.template_ref` template with invalid `{{ }}` syntax: an error
  containing `"template: parse error"`, with the same hint as above.

### Lifecycle and cleanup

Timeout and abort are owned by the step-run lifecycle. This spec adds no
stronger interruption or rollback guarantee. Parsing and data evaluation
complete before rendered output is emitted. A parse or missing-key error
therefore produces no rendered output. Output-directory or file-write
failures fail the step.

## Related Specs

- Value resolution and reference syntax: [Spec 007: Value Resolution
  Steps](007-value-resolution-steps.md)
- Step run scripts, for contrast with template's no-substitution
  behavior: [Spec 015: Step Run Script](015-step-run-script.md)

## Examples

Render a config file from structured data:

```yaml
steps:
  - action: template.render
    with:
      template: |
        name: {{ .service }}
        replicas: {{ .replicas }}
      data:
        service: checkout
        replicas: 3
      output: ./build/config.yaml
```

Render a template whose text comes from an environment variable:

```yaml
env:
  - MESSAGE_TEMPLATE: "Deploy {{ .version }} complete"
steps:
  - action: template.render
    with:
      template_ref: "${env.MESSAGE_TEMPLATE}"
      data:
        version: "1.4.0"
```
