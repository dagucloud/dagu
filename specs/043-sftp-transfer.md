# Spec: SFTP Transfer Actions

## Status

Partially implemented.

Conformance covers action configuration without an SFTP server. Transfer
integrity, atomic replacement, permissions, and protocol behavior belong in
executor unit and integration tests.

## Scope

This spec defines configuration for `sftp.upload` and `sftp.download`.
It does not define SSH authentication or remote filesystem semantics.

## Goal

Workflow authors can configure file transfers and receive useful diagnostics
for invalid transfer inputs.

## Behavior

Both actions accept `with.source` and `with.destination` paths and SSH
connection fields under `with`.
When `with` contains only transfer options (`source`, `destination`, and
`direction`), the action inherits the DAG-level `ssh` configuration. Explicit
step-level SSH configuration takes precedence and is not merged with DAG-level
connection settings.
`dagu validate` accepts configured transfers without contacting the host.
The action name determines the transfer direction. An optional
`with.direction` must agree with the action name.

## Errors

A conflicting `with.direction` fails validation. Missing source or destination
paths fail execution before connecting, with a diagnostic identifying the
missing path.

## Examples

```yaml
steps:
  - action: sftp.upload
    with:
      host: example.invalid
      user: deploy
      source: source.txt
      destination: target.txt
```
