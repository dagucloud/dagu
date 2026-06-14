# Spec: Value Resolution Env

## 1. Scope

1-1. This spec defines root, step, and container `env` value resolution and `${env.NAME}` references.

1-2. Common reference syntax, supported fields, string insertion, and shell preservation are defined by [Spec 003: Value Resolution](003-value-resolution.md).

1-3. Resolution timing is defined by [Spec 008: Value Resolution Order](008-value-resolution-order.md).

1-4. This spec does not define operating-system environment inheritance outside values visible to Dagu value resolution.

## 2. Goal

2-1. Workflows can build environment values from other value-resolution namespaces and can reference environment values from supported fields.

## 3. Inputs

3-1. Inputs are environment values visible to the owning field, including root `env`, step `env`, container `env`, dotenv-loaded values, and runtime environment values made available to the DAG run.

3-2. Root `env` may use map form:

```yaml
env:
  API_HOST: api.${consts.url}
```

3-3. Root, step, and container `env` may use list form:

```yaml
env:
  - SERVICE=api
  - API_HOST=${env.SERVICE}.internal
```

## 4. Behavior

4-1. `${env.NAME}` reads `NAME` from the environment scope available to the owning field.

4-2. `NAME` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

4-3. `dagu validate` must not require `${env.NAME}` to exist at validation time.

4-4. `dagu validate` must validate `${env.NAME}` syntax and statically knowable field restrictions.

4-5. Root `env` may reference `consts`, `params`, and earlier available `env`.

4-6. Root `env` must not reference `steps`.

4-7. Step `env` may reference `consts`, `params`, `env`, and step outputs that are available before the owning step starts.

4-8. Container `env` follows the same namespace visibility rules as the root or step container that owns it.

4-9. In map-form `env`, entries are unordered.

4-10. A map-form `env` entry must not reference another key declared in the same map.

4-11. In list-form `env`, entries are evaluated from top to bottom.

4-12. A list-form `env` entry may reference earlier entries from the same list.

4-13. A list-form `env` entry must not reference itself or a later entry from the same list.

4-14. Environment values are inserted into string fields according to Spec 003 string insertion rules.

## 5. Outputs

5-1. Resolving `env` provides the referenced environment value to the owning field.

5-2. Resolving `env` does not write workflow result events, run logs, or artifacts.

## 6. Errors

6-1. Invalid `env` reference shape must fail during workflow validation.

6-2. A `steps` reference in root `env` must fail during workflow validation.

6-3. A map-form `env` entry that references another key declared in the same map must fail during workflow validation.

6-4. A list-form `env` entry that references itself or a later entry in the same list must fail during workflow validation.

6-5. A missing `env` value must fail before the owning field is used.

## 7. Examples

Root environment from `consts`:

```yaml
consts:
  - url: internal
env:
  API_HOST: api.${consts.url}
steps:
  - name: print_api_host
    run: echo ${env.API_HOST}
```

Ordered environment entries:

```yaml
env:
  - SERVICE=api
  - API_HOST=${env.SERVICE}.internal
steps:
  - name: print_api_host
    run: echo ${env.API_HOST}
```

Step environment scope:

```yaml
steps:
  - name: deploy
    env:
      - SERVICE=api
    run: echo ${env.SERVICE}
```

## 8. Acceptance Criteria

8-1. Black-box tests must cover 4-1 through 4-14 and 6-1 through 6-5.

8-2. Tests must prove `dagu validate` does not reject well-formed `${env.NAME}` only because `NAME` is absent at validation time.

8-3. Tests must prove root `env` rejects `steps` references.

8-4. Tests must prove map-form and list-form `env` ordering rules.
