# Spec: Value Resolution Params

## Scope

This spec defines `${params.name}` references.

Common reference syntax, supported fields, and string insertion are defined by [Spec 003: Value Resolution](003-value-resolution.md).

Resolution timing is defined by [Spec 003: Value Resolution](003-value-resolution.md).

This spec does not define parameter declaration schema beyond the rules needed for value resolution.

## Goal

Supported workflow fields can reference named runtime parameters.

## Motivation

Runtime parameters let a workflow caller provide values without editing the workflow file. Value resolution needs a named lookup rule so supported fields can use those values predictably.

This spec separates declaration validation from runtime value availability. `dagu validate` can reject unknown parameter names, while missing runtime values fail only when the run input does not provide them.

## Behavior

- `${params.name}` reads a declared named runtime parameter.

- `name` must match `^[A-Za-z][A-Za-z0-9_]*$`.

- Positional parameters are not addressable through the `params` namespace.

- `params` declarations do not support Dagu references.

- Runtime `params` are available after Dagu builds the run input.

- A resolved parameter value is inserted into string fields according to Spec 003 string insertion rules.

- An undeclared `params` reference in a value-resolution field must fail during workflow validation.

- A missing runtime value for a declared `params` reference must fail before the owning field is used.

- A positional parameter reference through `${params.name}` must fail during workflow validation because it has no named declaration.

## Examples

Valid `params` reference:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: deploy
    run: ./deploy.sh ${params.environment}
```

Invalid `params` reference:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: bad
    run: echo ${params.region}
```
