# Spec: Value Resolution Params

## 1. Scope

1-1. This spec defines `${params.name}` references.

1-2. Common reference syntax, supported fields, string insertion, and shell preservation are defined by [Spec 003: Value Resolution](003-value-resolution.md).

1-3. Resolution timing is defined by [Spec 008: Value Resolution Order](008-value-resolution-order.md).

1-4. This spec does not define parameter declaration schema beyond the rules needed for value resolution.

## 2. Goal

2-1. Supported workflow fields can reference named runtime parameters.

## 3. Inputs

3-1. Inputs are the workflow `params` declarations and the runtime parameter values supplied for one DAG run.

3-2. A named runtime parameter is declared with a `name` field:

```yaml
params:
  - name: environment
    type: string
    required: true
```

## 4. Behavior

4-1. `${params.name}` reads a declared named runtime parameter.

4-2. `name` must match `^[A-Za-z][A-Za-z0-9_]*$`.

4-3. Positional parameters are not addressable through the `params` namespace.

4-4. `params` declarations do not support Dagu references.

4-5. Runtime `params` are available after Dagu builds the run input.

4-6. A resolved parameter value is inserted into string fields according to Spec 003 string insertion rules.

## 5. Outputs

5-1. Resolving `params` provides the referenced runtime value to the owning field.

5-2. Resolving `params` does not write workflow result events, run logs, or artifacts.

## 6. Errors

6-1. An undeclared `params` reference in a value-resolution field must fail during workflow validation.

6-2. A missing runtime value for a declared `params` reference must fail before the owning field is used.

6-3. A positional parameter reference through `${params.name}` must fail during workflow validation because it has no named declaration.

## 7. Examples

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

## 8. Acceptance Criteria

8-1. Black-box tests must cover 4-1 through 4-6 and 6-1 through 6-3.

8-2. Tests must prove `dagu validate` rejects undeclared named parameters.

8-3. Tests must prove runtime resolution fails when a declared referenced parameter has no runtime value.
