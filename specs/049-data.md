# Spec: Data Convert and Pick Actions

## Status

Partially implemented.

Conformance covers JSON-to-YAML conversion, raw selection, a text round trip,
and representative errors. Delimited formats and option permutations belong in
data executor unit tests.

## Scope

This spec defines the action boundary for `data.convert`, `data.pick`, and
`data.filter`.
It does not define every serializer option or the selection language itself.

## Goal

Workflow authors can convert data and select values without an external tool.

## Behavior

`with.from` and `with.to` accept `json`, `yaml`, `csv`, `tsv`, and `text`.
`data.convert` decodes its input and serializes the resulting value in the
requested output format. JSON string input must be decoded as JSON.

For `text`, strings are preserved, null becomes an empty string, and other values
are represented as JSON text. Text output adds no newline.

`data.pick` evaluates `with.select` against the decoded input. With `with.raw`,
a string result is written without quotes and followed by a newline. A null
selection succeeds and writes exactly one newline.

`data.filter` keeps the rows of a list that meet `with.where`, a map from a
field of each row to its condition, all of which must hold. `with.select`
picks the list from the decoded input first. A field takes one kind of
condition: `min` and `max` read the value as the first number it holds, with
万 and 億 applied, commas dropped, and full-width digits folded; `from`, `to`,
and `within` read it as the first date it holds, written as `2026/11/12`,
`2026-11-12`, `1983年5月`, a Japanese era date such as `昭和58年5月`, or a
year, with `from` and `to` inclusive at the precision they are written in and
`within` counting days back from the day the step runs; `is`, `any`, and `none`
compare text with full-width letters and digits folded. A row whose value does
not read as its condition's kind is left out, and stderr reports how many
such rows each field left out with one value. `with.sort` orders the kept
rows by fields, descending for a field written with `-` before it, comparing
each as its condition reads it, else as a date, a number, or text;
`with.limit` keeps at most that many. The kept rows are written as JSON, or
in `with.to`, and stderr reports how many of the rows were kept.

## Errors

A missing input format or selection expression fails validation with a diagnostic
identifying the field. A `data.filter` with no `where`, an unknown condition, a
field given conditions of more than one kind, or a bound that does not read as
its kind fails validation and names the field. Malformed JSON input fails execution with a JSON decode
diagnostic.

## Examples

```yaml
steps:
  - action: data.convert
    with:
      from: json
      to: yaml
      data: '{"name":"alice","age":30}'
```
