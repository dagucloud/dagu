# Spec: Value Resolution Consts

## Scope

This spec defines root `consts` and `${consts.name}` references.

Common reference syntax, supported fields, and string insertion are defined by [Spec 003: Value Resolution](003-value-resolution.md).

Resolution timing is defined by [Spec 003: Value Resolution](003-value-resolution.md).

This spec does not define `params`, `env`, or `steps` references.

## Goal

Workflows can define immutable values once and reference them from supported value-resolution fields.

## Motivation

Workflow authors need a deterministic way to name reusable literal values. Mapping order is not part of the contract, so ordered `consts` need a list form when one constant depends on an earlier constant.

This spec keeps `consts` immutable and load-time resolvable so later value resolution can use them without runtime side effects.

## Behavior

- Root `consts` may be an ordered list of single-entry mappings or a mapping.

- Ordered list form is the canonical form. Examples must use list form by default because mapping order is not part of the contract.

- `consts` keys must match `^[A-Za-z][A-Za-z0-9_]*$`.

- `consts` values must be literal strings, numbers, or booleans.

- Numeric `consts` values must be finite.

- `consts` values are immutable for one DAG run.

- Mapping-form `consts` values must not contain Dagu references.

- List-form `consts` values are resolved in order.

- String values in list-form `consts` may reference earlier `consts` entries with `${consts.name}`.

- List-form `consts` values must not reference runtime `env`, `params`, `steps`, later `consts`, or themselves.

- `${consts.name}` reads `name` from resolved root `consts`.

- A resolved `consts` value is inserted into string fields according to Spec 003 string insertion rules.

- Invalid `consts` shape must fail during workflow validation.

- Invalid `consts` key names must fail during workflow validation.

- Invalid `consts` value types must fail during workflow validation.

- Non-finite numeric `consts` values must fail during workflow validation.

- Mapping-form `consts` values that contain Dagu references must fail during workflow validation.

- List-form `consts` references to runtime `env`, `params`, `steps`, later `consts`, or themselves must fail during workflow validation.

- An unknown `consts` reference in a value-resolution field must fail during workflow validation.

## Examples

Canonical list-form `consts`:

```yaml
consts:
  - service: api
  - deploy_script: ./scripts/deploy.sh
  - enabled: true
```

Ordered `consts` with const-to-const references:

```yaml
consts:
  - service: api
  - endpoint: http://localhost/${consts.service}
steps:
  - name: print_endpoint
    run: echo ${consts.endpoint}
```

Invalid `consts` reference:

```yaml
steps:
  - name: bad
    run: echo ${consts.missing}
```

Mapping form for independent literal constants:

```yaml
consts:
  service: api
  deploy_script: ./scripts/deploy.sh
```
