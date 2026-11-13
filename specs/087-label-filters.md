# Spec: Label Filters

## Status

Implemented. Conformance covers filters through `dagu history --labels`.

## Scope

This spec defines the label filter that selects DAGs and DAG runs by their
labels: `dagu history --labels`, and the `labels` parameter of the DAG list,
DAG search, DAG-run list, and DAG-run activity APIs. It does not define how
labels are declared or validated on a DAG.

## Goal

A filter selects exactly the DAGs or runs it describes, and a mistyped filter
fails instead of silently selecting nothing or everything.

## Behavior

A filter is a list of terms. On the command line and in the APIs the terms are
separated by commas. A DAG or run is selected when it matches every term.
Blank terms are ignored.

| Term | Selects labels sets that |
| --- | --- |
| `key` | Have a label with key `key` |
| `key=value` | Have the label `key=value` |
| `key=` | Have a label with key `key` and no value |
| `*` and `?` in a key or value | Match as globs: `*` matches any run of characters, including none and `/`, and `?` matches one character |
| `!` before any term above | Do not match that term |

- Keys and values match without case.
- Spaces around a term, around its `!`, and around its `=` are ignored.
- A key is a letter or digit followed by letters, digits, `-`, `_`, or `.`.
  A value is a letter or digit followed by letters, digits, `-`, `_`, `.`, or
  `/`. Either may also contain `*` and `?` anywhere, including first.

## Errors

A term that is not one of the forms above, such as `env==prod`, `=prod`, a
lone `!`, or a term with an inner space, fails the request with the message
`invalid label filter "<term>"`. `dagu history` exits non-zero. The APIs answer
`400 Bad Request`. Nothing is selected.

## Examples

```sh
dagu history --labels 'env=prod'          # runs labeled env=prod
dagu history --labels 'critical,!env=dev' # critical runs not labeled env=dev
dagu history --labels '!env'              # runs with no env label
dagu history --labels 'region=us-*'       # runs in any us- region
```
