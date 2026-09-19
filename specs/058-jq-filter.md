# Spec: JQ Filter Action

## Status

Partially implemented.

## Scope

This spec covers `jq.filter` input selection, named arguments, scalar output,
and configuration errors. The jq language, other value-resolution permutations,
object formatting, and per-value error iteration belong to executor tests.

## Goal

Workflow authors can filter inline or file-backed JSON without installing
an external jq command.

## Behavior

`with.filter` supplies the query. `with.data` accepts inline data;
`with.input` names a JSON file. A `with.data` string beginning with
`file://` reads the named file. The `file://` shortcut applies only to
`with.data`; `with.input` is a filesystem path.

A scalar result is written as JSON followed by a newline. With
`with.raw: true`, strings are written without JSON quotes and a null result
writes exactly one newline.

### Named Arguments

`with.args` is an optional object whose keys bind jq variables as `$name`.
One leading `$` on a key is optional. Supplying both `name` and `$name` is
an error because they bind the same variable.

Argument values preserve YAML scalar and container types. String values,
including nested strings, follow ordinary executor-config reference resolution
once. Escaped dollar references remain literal. String references do not gain
numeric types automatically; numeric comparisons use `tonumber` when needed.

The presence of `args`, including an empty object, makes the entire filter
literal jq source. Dagu does not interpolate or interpret workflow references
in that filter, including reference-looking text in jq strings. Multiline
filters remain query text. Workflow references in `args` retain their normal
validation and dependency semantics. Omitting `args` retains the existing
filter interpolation behavior.

The same rule applies to accepted `type: jq` steps with `config.args` or
`with.args`.

## Errors

`dagu validate` rejects a missing `with.filter` and configurations that set
both `with.data` and `with.input`. It exits nonzero with an error identifying
the invalid configuration.

Duplicate normalized argument names fail executor setup. Invalid variable names
and undeclared jq variables fail filter compilation at step execution.

Other filter-language errors, missing files, timeout, and abort behavior are
outside this conformance scope.

## Example

```yaml
steps:
  - action: jq.filter
    with:
      filter: .name
      data:
        name: World
      raw: true
```
