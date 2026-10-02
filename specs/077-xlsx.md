# Spec: XLSX Actions

## Status

Partially implemented.

Conformance covers writing and reading a workbook through the CLI: typing,
range and header options, table detection, limits, append, update_rows with
its shape checks, dry run, sheet preservation, type errors, validation
errors, the unsupported `.xls` format, and the lock file. Formula
evaluation, merged cells, named ranges, tables, custom number formats, the
1904 date system, Windows sharing violations, and `wait_for_unlock` timing
belong to unit tests of `internal/cmn/workbook`.

Not implemented: `xlsx.validate`, `xlsx.write_cells`, `xlsx.convert`,
`xlsx.sheet`, streaming writers, and `dagu dry` checks for xlsx steps.

## Scope

This spec defines the `xlsx.read`, `xlsx.info`, `xlsx.list_sheets`,
`xlsx.write`, `xlsx.append`, and `xlsx.update_rows` actions, the
`dagu xlsx inspect` and `dagu xlsx read` commands, and the MCP `workbook`
read target. It covers configuration, validation, how cells become values,
how values become cells, published outputs, change summaries, atomic saves,
and locked workbooks.

Out of scope: driving a spreadsheet application, the `.xls` and `.ods`
formats, macros, charts, images, pivot tables, conditional formatting, data
validation rules, Google Sheets, and file locking on synced folders. Charts,
images, and formatting the actions do not touch are preserved on save.

## Goal

A workflow can take a workbook as input, act on each row, and write results
back into the same workbook, without a spreadsheet application and without a
script, and a rerun is safe: rows already done are skipped and a sheet that
changed shape under the workflow is refused rather than written to.

## Behavior

### Operations

| Action | Purpose | Outputs |
| --- | --- | --- |
| `xlsx.read` | Rows of a sheet, range, named range, or table. | `rows`, `count`, `headers`, `sheet`, `range`, `warnings`, `truncated` |
| `xlsx.info` | Sheets with used range, detected table, headers, column types, row count, and tables; named ranges; date system. | `path`, `date_system`, `sheets`, `named_ranges`, `warnings` |
| `xlsx.list_sheets` | Sheet names in order. | `sheets`, `count` |
| `xlsx.write` | Create a workbook or write a sheet from rows. | `path`, `sheet`, `changes`, `dry_run`, `warnings`, `artifact` |
| `xlsx.append` | Add rows below the last used row. | Same as `xlsx.write` |
| `xlsx.update_rows` | Write columns back to rows selected by key or `_row`. | Same as `xlsx.write` |

Outputs are fixed: declaring `output`, `outputs`, or `stdout.outputs` on an
xlsx step is rejected at validation. The executor writes one line to stdout
and one `warning:` line per warning to stderr.

Only `.xlsx` and `.xlsm` paths are accepted. `path` resolves like the file
actions: absolute and `~` paths as written, relative paths against the step
working directory. A `password` opens a protected workbook.

### Addressing

- `sheet` names the sheet; the first sheet by default. Names match exactly,
  then case-insensitively when that is unique. A miss lists the sheets present.
- `range` is `A2:F200`, `A2:F` or `A:F` (open end at the last used row), a
  single cell, `Sheet1!A2:F` or `'My Sheet'!A2:F` (the sheet in the range
  wins over `sheet`), a defined name (sheet scope preferred over workbook
  scope), or a table name.
- Without `range`, the data block is detected: leading empty rows and
  columns are skipped, the header is the first row with two or more non-empty
  cells, and the block extends to the last non-empty row and column. The
  `range` output reports the choice as `Sheet!A1:F120`.
- `header` is `true` (default: the first row of the range names the
  columns), `false` (columns are named `A`, `B`, `C`), a row number, or a
  list of row numbers whose cells are joined with a space, so `Amount` merged
  over `Net` and `Tax` yields `Amount Net` and `Amount Tax`. Header text is
  trimmed, line breaks become spaces, an empty header becomes the column
  letter with a warning, and a duplicate gets a `_2` suffix with a warning.
- `columns` keeps and orders columns: names, `Invoice No: invoice_no` to
  rename, or `{Invoice No: invoice_no}` entries. A name that matches no header
  fails the step listing the headers present.
- `merged` is `fill` (default: a merged value is read in every cell it
  covers) or `first` (only the top-left cell).

### Typing

| Cell | Value |
| --- | --- |
| Number | JSON number. Integral values within 2^53 are integers. |
| Number with a date, time, or date-time format | ISO 8601 text: `2026-10-01`, `15:04:05`, or `2026-10-01T14:30:00`. A serial with a fraction under a date-only format is a date-time. Built-in formats 14 to 22, 27 to 36, 45 to 47, and 50 to 58 count, and so does a custom format with date or time tokens outside quotes, such as `yyyy"年"m"月"d"日"`. `[h]:mm` counts elapsed time and stays a number. The 1900 and 1904 date systems are honored. |
| Text, including text-formatted numbers | String, so leading zeros survive. `trim: true` removes surrounding white space, including the full-width space U+3000. |
| Boolean | `true` or `false`. |
| Formula | Its cached value. `formulas: text` yields `=` and the formula; `formulas: calculate` evaluates it. A formula without a cached value is evaluated with a warning; one that cannot be evaluated is null with a warning. |
| Error such as `#N/A` | Null, with a warning naming the cell. |
| Empty | Null. Trailing rows whose cells are all null are dropped unless `keep_empty_rows: true`. |

Each row carries `_row`, its 1-based sheet row number. `types` pins columns
to `string`, `number`, `integer`, `boolean`, `date`, or `datetime`; a cell
that cannot convert fails the step naming the cell, or with
`on_type_error: warn` (also spelled `null`) becomes null with a warning.

### Reading

`where` keeps rows: a scalar matches equal values, `""` matches empty
cells, `{ne: v}` excludes a value, `{in: [a, b]}` matches a list. Numbers
compare numerically and everything else as trimmed text. Keys may use the
original header, a loose match, or a `columns` alias.

`stop_at_blank: true` stops at the first row whose resolved values are all
null. `max_rows` (default 5000) counts rows that hold a value; blank rows
between them are kept as null rows without counting, unless
`keep_empty_rows` is set, in which case every row counts. Reaching the cap
stops reading; `truncated` is set, with a warning, only when more countable
rows remain below the cap, so a sheet holding exactly `max_rows` rows is not
truncated. Rows that exceed the step output budget, 900 KiB or
`max_output_size` less 64 KiB, are left out from the end and `truncated` is
true with a warning.

### Writing

`xlsx.write` takes `rows` or `input`, not both. `rows` is a list of objects
or arrays, usually `${steps.<id>.outputs.rows}`; the key order of JSON text
is kept, while objects from YAML or a decoded map are written in sorted key
order unless `columns` orders them. `input` is a `.json` array, a `.jsonl`
file, or a `.csv` with a header line; `format` overrides the extension. A
`_row` field is never written.

A missing workbook is created. A `sheet` that does not exist is created; an
existing sheet is replaced (`mode: replace`, the default) or extended
(`mode: append`). A replaced sheet is cleared in place: its merged regions
and tables are removed, and every cell holding a value or formula is
emptied of its value, style, and hyperlink. Cells that carry only a style
are cleared as well when the sheet's stored dimension spans at most 2^20
cells; past that, a sweep of the whole rectangle is skipped so a large
sparse sheet stays cheap to replace, and such style-only cells keep their
style. A hyperlink on a cell the clear does not visit, one that is empty
and outside both the stored dimension and the cells holding values, is
kept for the same reason. The sheet itself, its position, the defined names scoped to it, and
formulas on other sheets that refer to it stay valid. Other sheets, column
widths, styles, and defined names are untouched. An AutoFilter on the
replaced sheet stays in place, as the underlying library offers no way to
remove one.

Values are written by type: numbers as numbers, booleans as booleans,
`2026-10-01` and `2026-10-01T14:30:00` strings as dates, other strings as
text. `types` pins a column: `number` and `date` convert strings, `string`
keeps ISO-looking text as text.

`style: table` (default) makes a new or replaced sheet look finished: bold
header on a light fill, frozen below the header, column widths fitted to
content between 8 and 60 characters with East Asian characters counting
double, and number formats by column kind: integers plain, decimals with two
places, dates `yyyy-mm-dd`, date-times `yyyy-mm-dd hh:mm:ss`, text `@`. A
column mixing dates and date-times is formatted as date-time. `style: none`
writes bare cells.

`xlsx.append`, and `mode: append`, write below the last non-empty row with
no header, and each new cell copies the style of the cell above it, so a date
column stays a date column. An append that starts an empty sheet writes the
header so the first run creates a table.

### Updating rows

`xlsx.update_rows` takes `rows` (objects, each with the `key` column and
optionally `_row`), `key` (a column name, or `_row` to address rows by
number alone), and `set`:

```yaml
set:
  Status: status            # a field of each row
  Reviewed: {value: "yes"}  # one literal for every row
```

Without `set`, every field other than the key and `_row` goes to the
column of the same name. A field in `set` that no row carries is an error. A
column that is not in the header row is added at the right, its header cell
copying the style of the last header cell.

Rows are matched by `_row` when present, else by the key column, with keys
compared as trimmed text so `7` and `"7"` match. A key found at two rows is an
error. A key not found does what `missing` says: `fail` (default), `skip`
with a warning, or `append` below the last used row, copying the styles of
the row above. With `key: _row`, `missing` must be `fail`.

`rows` may also be the aggregate output of a `foreach` step, an object with
`summary`, `items`, and `outputs`; its `outputs` list, the collected objects
of the item bodies that succeeded, is used. Two input rows that address the
same sheet row are an error.

Only the columns in `set` change. A row that does not carry a mapped field
leaves that cell as it is; an explicit null empties it. A cell whose value
already matches, compared with its type so the number 7 and the text `7`
differ, is not counted as changed. A date written into a date column keeps
the column's format; a date written elsewhere gets a date format. The key
column cannot be in `set`.

### Shape checks

Two checks run before any cell is written, and either failure aborts the
step with the workbook untouched. They cannot be turned off.

1. The key column and every column in `set` that already exists must still be
   in the header row by exact name. A header that matches only
   case-insensitively or after trimming is reported with the name found.
2. Every row carrying `_row` must still hold its key at that row.

### Change summary

Every writer publishes `changes`:

```json
{"sheet": "Orders", "range": "Orders!A2:F148", "rows_updated": 147,
 "rows_appended": 0, "columns_added": 1, "cells_changed": 294}
```

`dry_run: true` computes the same summary and saves nothing.

### Atomic save and locks

A save writes a short-named temporary file beside the workbook, gives it the
target's permission bits, and renames it over the target, so a crash never
leaves a half-written workbook; a symbolic link is followed so the workbook
it points to is replaced. `atomic: false` saves in place. Excel's
`~$name.xlsx` lock file is checked before a write: a lock file another
process still holds open, which on Windows means Excel has the workbook,
fails the step; a lock file nobody holds is a leftover of a crash, so the
step warns and continues. A Windows sharing violation on open, save, or
rename fails the same way. `wait_for_unlock: 5m` retries a locked workbook,
waiting two seconds and doubling to one minute, and logs each wait; the
whole open-modify-save sequence runs again on each try.

### Artifacts

`artifact: true` on a writer copies the saved workbook under
`xlsx/<step>/` in the run's artifacts directory and publishes its relative
path as `artifact`. The option enables artifact storage for the DAG, as
does a value reference such as `${params.KEEP}` whose value is only known
at run time. A dry run copies nothing. A copy that fails after the workbook
was saved is reported as a warning, not as a failed step, so a retry does
not repeat a write that already happened.

### CLI

`dagu xlsx inspect <path> [--rows N] [--sheet NAME] [--format text|json]`
prints what `xlsx.info` publishes plus up to N typed sample rows per sheet.
`dagu xlsx read <path> [--sheet] [--range] [--header] [--columns]
[--max-rows] [--format]` prints what `xlsx.read` publishes. Both read the
file directly, create no run, and exit non-zero with the error on stderr.

### MCP

The `dagu_read` target `workbook` takes `path`, any workbook the server
process can read, and returns the `inspect` description with five sample
rows per sheet. The path is recorded in the audit log as `workbook_path`.
Spec 021 defines the target's fields and errors.

## Errors

### Validation

Every one of these is rejected by `dagu validate`:

- Any xlsx action without `with.path`: `path is required for <operation>`.
- `xlsx.write` or `xlsx.append` without `rows` or `input`:
  `write requires with.rows or with.input`; with both:
  `accepts with.rows or with.input, not both`.
- `xlsx.update_rows` without `key`: `key is required for update_rows`;
  without `rows`: `update_rows requires with.rows`.
- A field of another operation, such as `range` on `xlsx.info`:
  `with.range is not valid for xlsx.info`.
- An unknown field, or a value outside its enum (`merged`, `formulas`,
  `on_type_error`, `mode`, `style`, `format`, `missing`, a type in `types`).
- `output`, `outputs`, or `stdout.outputs` on an xlsx step:
  `xlsx actions have fixed outputs`.
- `header` that is not `true`, `false`, a row number, or a list of row
  numbers; `max_rows` below 1; a `where` operator other than `eq`, `ne`, or
  `in`; `set` values that are neither a field name nor `{value: literal}`;
  `missing: skip` or `append` with `key: _row`; `wait_for_unlock` that is
  not a duration.

### Runtime

- A path that is not `.xlsx` or `.xlsm`: an error containing
  `only .xlsx and .xlsm workbooks are supported; save as .xlsx`.
- A missing workbook: `<name>: workbook not found`.
- A missing sheet: `sheet "Order" not found; sheets present: Orders, Summary`.
- A range that is none of the accepted forms:
  `range "Totals" is not a cell range, named range, or table`.
- A `columns`, `types`, or `where` name that matches no header:
  `column "Nope" not found; headers present: ...`.
- A cell that fails a pinned type:
  `orders.xlsx Orders!D17: expected number, found "N/A"`.
- A key column missing from the header row:
  `key column "Invoice" not found in header row 1; headers present: ...`, or
  with a loose match, `did you mean "Invoice No"?`.
- A `set` column with only a loose match:
  `column "status" not found in header row 1; did you mean "Status"?`.
- A row that moved: `orders.xlsx Orders!A17: expected key "INV-17", found
  "INV-18"; the sheet changed since it was read`.
- A key not in the sheet with `missing: fail`: `key "INV-99" not found`.
- A key at two rows: `key "INV-1" appears at rows 5 and 9`.
- A workbook another program holds, on Windows:
  `orders.xlsx is open in another program; close it and retry`.
- `artifact: true` in a DAG whose artifacts are disabled:
  `artifact requires artifact storage`.

Errors that concern a cell name the workbook, sheet, and cell as
`<workbook> <sheet>!<cell>: <message>`.

## Related Specs

- Step outputs and the output budget: [Spec 012](012-step-outputs.md)
- Built-in run context and the artifacts directory: [Spec 017](017-built-in-run-context.md)
- MCP read tool and the `workbook` target: [Spec 021](021-mcp-read-tool.md)
- Artifacts: [Spec 051](051-artifact.md)
- File actions, whose path handling the xlsx actions share: [Spec 052](052-file.md)
- Mailbox actions, whose output budget and attachment storage the xlsx actions mirror: [Spec 073](073-mailbox-actions.md)

## Examples

Read rows still to do, act on each, and write the result back:

```yaml
steps:
  - id: read
    action: xlsx.read
    with:
      path: ~/Inbox/orders.xlsx
      sheet: Orders
      where: {Status: ""}
  - id: each
    depends: read
    foreach:
      items: ${steps.read.outputs.rows}
      key: ${foreach.item.order_id}
      steps:
        - id: submit
          action: http.request
          with:
            method: POST
            url: https://erp.example.com/orders
            body: ${foreach.item}
            format: json
      collect:
        order_id: ${foreach.item.order_id}
        status: ${steps.submit.outputs.status_code}
    output: RESULTS
    continue_on:
      failure: true
  - id: mark
    depends: each
    action: xlsx.update_rows
    with:
      path: ~/Inbox/orders.xlsx
      sheet: Orders
      key: order_id
      rows: ${steps.each.outputs.RESULTS}
      set:
        Status: status
      wait_for_unlock: 5m
```

`collect` gives each successful item one object with the key and the result
fields, and `rows` takes the foreach aggregate directly, using its `outputs`
list. `continue_on.failure` on the loop lets the write-back run when some
rows failed, so the rows that succeeded are marked rather than submitted
again on the next run.

Build a report from a query and keep it with the run:

```yaml
steps:
  - id: totals
    action: postgres.query
    with:
      dsn: ${env.REPORTING_DSN}
      query: select customer, sum(amount) as total from orders group by customer
  - id: report
    depends: totals
    action: xlsx.write
    with:
      path: reports/${context.attempt.started_at}.xlsx
      sheet: Totals
      rows: ${steps.totals.outputs.rows}
      columns: [customer, total]
      types: {total: number}
      artifact: true
```

Preview what an append would change, then do it:

```yaml
steps:
  - id: preview
    action: xlsx.append
    with:
      path: log.xlsx
      rows: ${params.ROWS}
      dry_run: true
  - id: append
    depends: preview
    action: xlsx.append
    with:
      path: log.xlsx
      rows: ${params.ROWS}
```
