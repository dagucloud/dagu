# Spec: Value Resolution Env

## Scope

This spec defines the Dagu environment scope for workflows, steps, and containers.

It also defines `${env.NAME}`, `$NAME`, and `${NAME}` references when a field supports environment expansion.

Common reference syntax, supported fields, and string insertion are defined by [Spec 003: Value Resolution](003-value-resolution.md).

Resolution timing is defined by [Spec 003: Value Resolution](003-value-resolution.md).

This spec does not define operating-system environment inheritance outside values visible to Dagu value resolution.

## Goal

Workflows can build environment values from other value-resolution namespaces and can reference environment values from supported fields.

## Motivation

Environment values can come from workflow YAML, dotenv files, the process environment, containers, and step-local settings. Dagu knows which environment scope applies to each workflow field and step field. Some values are still unavailable during validation.

This spec defines what Dagu can check statically and what must wait until runtime. It also makes map-form and list-form ordering rules explicit so environment-to-environment references are deterministic.

## Behavior

- `${env.NAME}` reads `NAME` from the Dagu environment scope for the field being resolved.

- `$NAME` and `${NAME}` read from the same Dagu environment scope when the field supports environment expansion.

- `NAME` must match `^[A-Za-z_][A-Za-z0-9_]*$`.

- `dagu validate` must not require `${env.NAME}` to exist at validation time.

- `dagu validate` must validate `${env.NAME}` syntax and statically knowable field restrictions.

- Root `env` may reference `consts`, `params`, and earlier available `env`.

- Root `env` must not reference `steps`.

- Step `env` may reference `consts`, `params`, `env`, and step outputs that are available before the owning step starts.

- Container `env` follows the same namespace visibility rules as the root or step container that owns it.

- In map-form `env`, entries are unordered.

- A map-form `env` entry must not reference another key declared in the same map.

- In list-form `env`, entries are evaluated from top to bottom.

- A list-form `env` entry may reference earlier entries from the same list.

- A list-form `env` entry must not reference itself or a later entry from the same list.

- Environment values are inserted into string fields according to Spec 003 string insertion rules.

- Invalid `env` reference shape must fail during workflow validation.

- A `steps` reference in root `env` must fail during workflow validation.

- A map-form `env` entry that references another key declared in the same map must fail during workflow validation.

- A list-form `env` entry that references itself or a later entry in the same list must fail during workflow validation.

- A missing `env` value must fail before the owning field is used.

## Examples

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
