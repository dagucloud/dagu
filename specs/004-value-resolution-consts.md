# Spec: Value Resolution Consts

## 1. Scope

1-1. This spec defines root `consts` and `${consts.name}` references.

1-2. Common reference syntax, supported fields, string insertion, and shell preservation are defined by [Spec 003: Value Resolution](003-value-resolution.md).

1-3. Resolution timing is defined by [Spec 008: Value Resolution Order](008-value-resolution-order.md).

1-4. This spec does not define `params`, `env`, or `steps` references.

## 2. Goal

2-1. Workflows can define immutable values once and reference them from supported value-resolution fields.

## 3. Inputs

3-1. The input is the root `consts` field in a workflow YAML file.

3-2. Ordered list form is the canonical input form:

```yaml
consts:
  - service: api
  - deploy_script: ./scripts/deploy.sh
  - enabled: true
```

3-3. Mapping form is accepted only for independent literal constants:

```yaml
consts:
  service: api
  deploy_script: ./scripts/deploy.sh
```

## 4. Behavior

4-1. Root `consts` may be an ordered list of single-entry mappings or a mapping.

4-2. Ordered list form is the canonical form. Examples must use list form by default because mapping order is not part of the contract.

4-3. `consts` keys must match `^[A-Za-z][A-Za-z0-9_]*$`.

4-4. `consts` values must be literal strings, numbers, or booleans.

4-5. Numeric `consts` values must be finite.

4-6. `consts` values are immutable for one DAG run.

4-7. Mapping-form `consts` values must not contain Dagu references.

4-8. List-form `consts` values are resolved in order.

4-9. String values in list-form `consts` may reference earlier `consts` entries with `${consts.name}`.

4-10. List-form `consts` values must not reference runtime `env`, `params`, `steps`, later `consts`, or themselves.

4-11. `${consts.name}` reads `name` from resolved root `consts`.

4-12. A resolved `consts` value is inserted into string fields according to Spec 003 string insertion rules.

## 5. Outputs

5-1. Resolving `consts` produces an immutable value map for the DAG run.

5-2. Resolving `consts` does not write workflow result events, run logs, or artifacts.

## 6. Errors

6-1. Invalid `consts` shape must fail during workflow validation.

6-2. Invalid `consts` key names must fail during workflow validation.

6-3. Invalid `consts` value types must fail during workflow validation.

6-4. Non-finite numeric `consts` values must fail during workflow validation.

6-5. Mapping-form `consts` values that contain Dagu references must fail during workflow validation.

6-6. List-form `consts` references to runtime `env`, `params`, `steps`, later `consts`, or themselves must fail during workflow validation.

6-7. An unknown `consts` reference in a value-resolution field must fail during workflow validation.

## 7. Examples

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

## 8. Acceptance Criteria

8-1. Black-box tests must cover 4-1 through 4-12 and 6-1 through 6-7.

8-2. Tests must cover both list-form and mapping-form `consts`.

8-3. Tests must prove list-form `consts` resolve top to bottom.

8-4. Tests must prove mapping-form `consts` do not accept Dagu references.
