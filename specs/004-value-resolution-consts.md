# Spec: Value Resolution Consts

## Implementation Status

Implemented.

Covered behavior:

- root `consts` list form;
- ordered `${consts.name}` lookup;
- validation for invalid const declarations;
- rejection of unavailable runtime namespaces while loading `consts`;
- preservation of const-looking text that is not a supported reference form.

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

- List-form `consts` values must not reference themselves or later `consts` entries.

- List-form `consts` values must not reference runtime `env`, `params`, or `steps`.

### References

- `${consts.name}` reads `name` from resolved root `consts`.

- `$consts.name` is not Dagu-owned `consts` reference syntax.
  It is preserved as ordinary string content according to Spec 003.

- A resolved `consts` value is inserted into string fields according to Spec 003 string insertion rules.

### Validation

- Invalid `consts` shape must fail during workflow validation.

- Invalid `consts` key names must fail during workflow validation.

- Invalid `consts` value types must fail during workflow validation.

- Non-finite numeric `consts` values must fail during workflow validation.

- Mapping-form `consts` must fail during workflow validation.

- `consts` references must fail during workflow validation if they target unavailable values.
  Unavailable values include runtime `env`, `params`, `steps`, later `consts`, and the same `consts` entry.

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

Invalid mapping form:

```yaml
consts:
  service: api
  deploy_script: ./scripts/deploy.sh
```
