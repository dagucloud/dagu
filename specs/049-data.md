# Spec: Data Convert and Pick Actions

## Status

Implemented.

This spec defines conformance behavior for the built-in `data.convert`
and `data.pick` actions.

## Scope

This spec defines `data.convert`, which re-encodes structured data from
one format to another, and `data.pick`, which selects a value out of
structured data and encodes just that value.

This spec covers:

- the required `with.from` field and its supported formats (`json`,
  `yaml`, `csv`, `tsv`, `text`)
- `with.data` and `with.input`, mutually exclusive and jointly required
  as the action's data source, with `with.input` naming a file resolved
  relative to the step's working directory
- `data.convert`'s required `with.to` field
- `data.pick`'s required `with.select` (a jq-style path) and its
  `with.raw` field, mutually exclusive with `with.to`
- `with.has_header`, `with.headers`, `with.delimiter`, and `with.columns`
  for CSV/TSV input and output
- that every configuration error this spec documents is rejected at
  DAG-build-time validation, not only at runtime
- `data.pick`'s distinction between a select path that legitimately
  resolves to `null` (succeeds) and one that is malformed or invalid for
  the data's shape (fails)
- validation and runtime errors

This spec does not define:

- the jq path language itself beyond what `data.pick`'s examples use --
  see the built-in `jq.filter` action's own spec for that
- direct `type: data` authoring
- CSV/TSV quoting and escaping edge cases beyond the default behavior the
  examples show

## Goal

Workflow authors reshape data between steps -- converting a file's
format, or pulling one field out of a larger structure -- without a
separate `jq`, general-purpose scripting, or command executor for what is
otherwise a small, declarative transformation.

## Behavior

### Data source and format

`with.from` names the input's format; `with.data` supplies it inline (as
a string to parse, or, for `json`/`yaml`, already as a native
object/array, in which case parsing is skipped and the value is used
as-is) or `with.input` names a file to read it from, resolved relative to
the step's working directory when not absolute. Exactly one of
`with.data` or `with.input` must be set.

CSV and TSV input is parsed with the standard-header convention
(`with.has_header` defaults to `true`): with a header row, each record
becomes an object keyed by column name; with `with.has_header: false` and
`with.columns` set, columns take their names from `with.columns` instead;
with neither, each record becomes a plain array of fields. Every CSV/TSV
field value decodes as a string -- there is no numeric or boolean type
inference.

### Convert

`data.convert` re-encodes the parsed value into `with.to`'s format and
writes it to stdout. CSV/TSV output writes a header row unless
`with.headers: false`; `with.columns`, when set, fixes the column order
(otherwise columns come from the union of all rows' keys, sorted).

### Pick

`data.pick` evaluates `with.select`, a jq-style path, against the parsed
value. `with.raw: true` writes the selected value as plain text (with no
JSON/YAML quoting): a string as itself, a number or boolean in its plain
form. Without `with.raw`, the selected value is encoded the same way
`data.convert` would (`with.to`, defaulting to `json`); `with.raw` and
`with.to` cannot be set together.

A select path that legitimately evaluates to `null` (for example,
indexing into a key that is absent) succeeds, the same way a real `jq`
invocation would. A select path invalid for the data's shape (for
example, array-indexing a number), or one that is not valid jq syntax at
all, is a runtime error (see "Errors").

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- `with.from` missing, or not one of `json`/`yaml`/`csv`/`tsv`/`text`.
- `with.data` and `with.input` both set, or neither set.
- `data.convert`'s `with.to` missing, or not a supported format.
- `data.pick`'s `with.select` missing.
- `data.pick`'s `with.raw: true` set together with `with.to`.
- `with.delimiter` longer than one character.

### Runtime

- `with.from: json` (or `yaml`) with string data that fails to parse:
  an error containing `"failed to decode JSON"` (or `"failed to decode
  YAML"`).
- `data.pick`'s `with.select` is malformed jq syntax, or invalid for the
  data's shape: an error containing `"failed to resolve select path"`.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Convert a CSV file to JSON:

```yaml
steps:
  - action: data.convert
    with:
      from: csv
      to: json
      input: ./people.csv
```

Pick one field out of a larger JSON structure:

```yaml
steps:
  - action: data.pick
    with:
      from: json
      input: ./config.json
      select: ".database.host"
      raw: true
```
