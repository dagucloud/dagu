# Spec: Value Resolution Consts

## Implementation Status

Implemented.

## Scope

This spec defines root `consts` and `${consts.name}` references.

Common reference syntax is defined by [Spec 003: Value Resolution](003-value-resolution.md).
Spec 003 also defines supported fields and string insertion.

Resolution timing is defined by [Spec 003: Value Resolution](003-value-resolution.md).

This spec does not define `params`, `env`, or `steps` references.

## Goal

Workflows can define immutable values once and reference them from supported value-resolution fields.

## Motivation

Workflow authors need a deterministic way to name reusable literal values.
`consts` use list form so dependency order is explicit.

This spec keeps `consts` immutable and load-time resolvable.
Later value resolution can use them without runtime side effects.

## Behavior

### Form

- Root `consts` must use list form.

- Each list item must be a single-entry mapping.

- Mapping form is invalid.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment expansion.

### Keys and Values

- `consts` keys must match `^[A-Za-z][A-Za-z0-9_]*$`.

- `consts` values must be literal strings, numbers, or booleans.

- Numeric `consts` values must be finite.

- `consts` values are immutable for one DAG run.

### List Resolution

- List-form `consts` values are resolved in order.

- String values in list-form `consts` may reference earlier `consts` entries with `${consts.name}`.

- List-form `consts` values cannot resolve themselves or later `consts` entries.

- A self-reference or later-reference is preserved as literal text and emits a passive diagnostic.

- Runtime `env`, `params`, and `steps` are unavailable while loading `consts`.

- A runtime namespace reference in `consts` is preserved as literal text and emits a passive diagnostic.

### References

- `${consts.name}` reads `name` from resolved root `consts`.

- `$consts.name` is not Dagu-owned `consts` reference syntax.
- Dagu preserves `$consts.name` as ordinary string content according to Spec 003.

- A resolved `consts` value is inserted into string fields according to Spec 003 string insertion rules.

### Validation

- Invalid `consts` shape must fail during workflow validation.

- Invalid `consts` key names must fail during workflow validation.

- Invalid `consts` value types must fail during workflow validation.

- Non-finite numeric `consts` values must fail during workflow validation.

- Mapping-form `consts` must fail during workflow validation.

- `consts` references that target unavailable values must emit a passive diagnostic and preserve the original text.

- Unavailable values include runtime `env`, `params`, `steps`, later `consts`, the same `consts` entry, and unknown `consts` names.

- An unknown `consts` reference in a value-resolution field must emit a passive diagnostic and preserve the original text.

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

Unknown `consts` reference:

```yaml
steps:
  - name: preserved
    run: echo ${consts.missing}
```

Invalid mapping form:

```yaml
consts:
  service: api
  deploy_script: ./scripts/deploy.sh
```
