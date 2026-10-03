# Spec: XLSX Actions

## Status

Implemented.

Conformance covers every statement below that a run of the `dagu` binary
can observe: the stdout line and the `warning:` and `problem:` streams,
path forms, every `range` and `header` form with the header warnings,
table detection, typing, formulas and error cells, empty rows, `where`,
`max_rows` with its default and the output budget, `rows` and every
`input` file kind with its encodings and the byte order mark, write modes
and `types` on write, sheet preservation, append, update_rows with its
matching rules, modes, shape checks, the foreach aggregate, and every
error, the lock file, artifacts including a DAG with artifacts disabled,
validate with every problem kind, `max_problems`, the stderr lines, and
`on_problem: fail`, the human task that follows a validation, write_cells
with its value forms, `merge`, into a copy, in a foreach, and every error,
merged cells met by appends, updates, and write_cells, sheet
operations with every mode and error, convert to csv, json, and jsonl
with every encoding and error, every `dagu dry` check, every validation
message in Errors, the `dagu xlsx` commands with their flags and text
formats, and extract with a scripted model: the cells the model names are
read with their types, including a merged region and Japanese text under
a pinned type, a repeated layout with the same instruction and schema
makes no model request, a form of the same template with a box left
blank is read from the cache, a changed layout, a changed schema, or a
label renamed in place asks again, the cell cap and the listing size cap
stop the step before any request, `send_values: false` keeps values out
of the request, a secret in the instruction stops the step before any
request, and `dagu xlsx cache clear` and `dagu rm
--history` drop the cached cells. Every workflow in Examples has a
fixture.

Conformance exceptions, behavior that a black-box run cannot observe or
set up and that unit tests of `internal/cmn/workbook` cover instead:
`password` (nothing in Dagu writes a protected workbook); the styles,
hyperlinks, merged regions, and tables that `mode: replace` clears,
`style: table` formatting, style copying, and column widths (not readable
through `xlsx.read`; that a replace empties the values is covered); the
temporary file of an
atomic save and symbolic links; a lock file another process holds,
`wait_for_unlock` timing, and Windows sharing violations; the consequences
of a rename or delete for formulas and defined names and what a copy
leaves behind (library behavior); the dry check leaving an unopenable
workbook to the run; `.xls` and `.ods` beyond the extension check. Which
cell a real model names is not a conformance matter: the scripted model answers
deterministically, and the suite proves what the engine does with an
answer. The MCP `workbook` target is covered by the Spec 021 suite.

## Scope

This spec defines the `xlsx.read`, `xlsx.info`, `xlsx.list_sheets`,
`xlsx.write`, `xlsx.append`, `xlsx.update_rows`, `xlsx.validate`,
`xlsx.write_cells`, `xlsx.sheet`, `xlsx.convert`, and `xlsx.extract`
actions, the `dagu xlsx inspect`, `dagu xlsx read`, and `dagu xlsx cache
clear` commands, the MCP `workbook` read target, and the checks `dagu dry`
runs for xlsx steps. It covers configuration, validation, how cells become
values, how values become cells, published outputs, change summaries,
atomic saves, locked workbooks, and what a model is shown and asked when a
step locates fields on a form.

Out of scope: driving a spreadsheet application, the `.xls` and `.ods`
formats, macros, charts, images, pivot tables, conditional formatting, the
workbook's own data validation rules, Google Sheets, file locking on synced
folders, and streaming writes for sheets beyond what memory holds. Charts,
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
| `xlsx.validate` | Check rows against rules and publish every problem. | `ok`, `problems`, `count`, `rows`, `headers`, `sheet`, `range`, `warnings`, `truncated` |
| `xlsx.write_cells` | Fill named cells of a workbook or of a copy. | Same as `xlsx.write` |
| `xlsx.sheet` | Add, copy, rename, or delete a sheet. | Same as `xlsx.write`, plus `sheets` |
| `xlsx.convert` | Export a sheet to csv, json, or jsonl. | `path`, `format`, `count`, `sheet`, `range`, `warnings`, `artifact` |
| `xlsx.extract` | Fields of a form-like sheet, located by a model and read from the cells it names. | One output per schema property, plus `cells`, `sheet`, `warnings`, `source` |

Outputs are fixed: declaring `output`, `outputs`, or `stdout.outputs` on an
xlsx step is rejected at validation. The executor writes one line to stdout
and one `warning:` line per warning to stderr; `xlsx.validate` also writes
one `problem:` line per problem. These are the step's own streams, so a
step's `stdout:` and `stderr:` file redirects (Spec 013) capture them.

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
  `range` output reports the choice as `Sheet!A1:F120`, the sheet name
  unquoted even when it holds a space; the `range` of a read, validate, or
  convert covers the header row when there is one.
- `header` is `true` (default: the first row of the range names the
  columns), `false` (columns are named `A`, `B`, `C`), a row number, or a
  list of row numbers whose cells are joined with a space, so `Amount` merged
  over `Net` and `Tax` yields `Amount Net` and `Amount Tax`. Header text is
  trimmed, line breaks become spaces, an empty header becomes the column
  letter with the warning `Sheet!C1: header is empty; column named C`, and
  a duplicate gets a `_2` suffix with the warning `Sheet: duplicate header
  "Amount" renamed Amount_2`.
- `columns` keeps and orders columns: names, `Invoice No: invoice_no` to
  rename, or `{Invoice No: invoice_no}` entries. A name that matches no header
  fails the step with `columns: column "Nope" not found; headers present:
  ...`; the same form with the `types:` and `where:` prefixes reports a
  bad name in those fields.
- `merged` is `fill` (default: a merged value is read in every cell it
  covers) or `first` (only the top-left cell).

### Typing

| Cell | Value |
| --- | --- |
| Number | JSON number. Integral values within 2^53 are integers. |
| Number with a date, time, or date-time format | ISO 8601 text: `2026-10-01`, `15:04:05`, or `2026-10-01T14:30:00`. A serial with a fraction under a date-only format is a date-time. Built-in formats 14 to 22, 27 to 36, 45 to 47, and 50 to 58 count, and so does a custom format with date or time tokens outside quotes, such as `yyyy"年"m"月"d"日"`. `[h]:mm` counts elapsed time and stays a number. The 1900 and 1904 date systems are honored. |
| Text, including text-formatted numbers | String, so leading zeros survive. `trim: true` removes surrounding white space, including the full-width space U+3000. |
| Text under a pinned `number`, `integer`, `date`, or `datetime` | Full-width digits and punctuation are read as their ASCII forms, so `１２３，４５６` is 123456 and `２０２６／１０／０３` is `2026-10-03`. A number may carry a thousands separator, a `¥` or `￥` before it, or `円` after it: `￥123,000` and `123,000円` are 123000. A date may be written `2026年10月3日`, `2026.10.3`, or in an era, long or short: `令和8年10月3日`, `令和元年5月1日`, `R8.10.3`, `H31/4/30`, with 明治 (M, 1868), 大正 (T, 1912), 昭和 (S, 1926), 平成 (H, 1989), and 令和 (R, 2019), each from the day it began, so `令和元年4月30日`, before 令和 began on 2019-05-01, is refused; a time of day may follow a kanji or era date, as `令和8年10月3日 14:30`. A weekday in parentheses, such as `（金）`, is not read. The failure message quotes the text as the cell holds it. |
| Boolean | `true` or `false`. |
| Formula | Its cached value. `formulas: text` yields `=` and the formula; `formulas: calculate` evaluates it. A formula without a cached value is evaluated with the warning `Sheet!B2: formula had no cached value; evaluated`; one that cannot be evaluated is null with `Sheet!B3: formula NA() could not be evaluated: #N/A`. |
| Error such as `#N/A` | Null, with the warning `Sheet!B3: error cell #N/A`. |
| Empty | Null. Trailing rows whose cells are all null are dropped unless `keep_empty_rows: true`. |

Each row carries `_row`, its 1-based sheet row number. `types` pins columns
to `string`, `number`, `integer`, `boolean`, `date`, or `datetime`; a cell
that cannot convert fails the step naming the cell, or with
`on_type_error: warn` (also spelled `"null"`, quoted so YAML keeps the
word) becomes null with a warning.

### Reading

`where` keeps rows: a scalar matches equal values, `""` matches empty
cells, `{ne: v}` excludes a value, `{in: [a, b]}` matches a list. Two
values that both read as numbers, the text `"7"` included, compare
numerically and everything else as trimmed text. Keys may use the
original header, a loose match, or a `columns` alias.

`stop_at_blank: true` stops at the first row whose resolved values are all
null. `max_rows` (default 5000) counts rows that hold a value; blank rows
between them are kept as null rows without counting, unless
`keep_empty_rows` is set, in which case every row counts. Reaching the cap
stops reading; `truncated` is set, with the warning `stopped after 5000
rows; set max_rows to read more`, only when more countable rows remain
below the cap, so a sheet holding exactly `max_rows` rows is not truncated.
Rows that exceed the step output budget, 900 KiB or `max_output_size` less
64 KiB (half of `max_output_size` when that is 64 KiB or less), are left
out from the end and `truncated` is true with the warning `output
truncated to N of M rows; narrow the range or columns, or filter with
where`.

### Writing

`xlsx.write` takes `rows` or `input`, not both. `rows` is a list of objects
or arrays, usually `${steps.<id>.outputs.rows}`; the key order of JSON text
is kept, while objects from YAML or a decoded map are written in sorted key
order unless `columns` orders them. `input` is a `.json` array, a `.jsonl`
file, or a `.csv` with a header line whose cells are text unless `types`
converts them; `format` overrides the extension. A `_row` field is never
written. A replace, or a write that creates the sheet, writes the table's
own order as the header; an append onto a sheet that already has rows
matches each field to the header row by name (see below), so a named row's
key order does not matter there.

A missing workbook is created in a directory that must exist; no writer
creates directories. A `sheet` that does not exist is created; an
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
header so the first run creates a table. A cell to append that lies in a
merged cell, such as an empty notes box below the rows, fails the step with
`Sheet1!A3: cannot append into merged cell A3:B4; unmerge it to write this
cell`, since every value written inside it would land in its top-left
cell.

Fields are placed by name. Object rows, rows from a CSV header, and rows
given `columns` are matched to the sheet's header row, its first row, by
exact name: a name the header has only loosely fails with `column "amount"
not found in header row 1; did you mean "Amount"?`; a name the header lacks
adds a column at the right, its header cell copying the style of the last
header cell, counted in `columns_added`; header columns no field carries
stay empty. A new header cell inside a merged cell would land in the merged
cell's top-left cell, which may hold another column's header, so it fails
with `Sheet1!C1: merged cell B1:C1 covers the header cell of new column
"note"; unmerge it to add the column`. Duplicate header names read as `Amount` and `Amount_2`. Array
rows have no names and are written by position. `header: false` says the
sheet has no header row: rows are written by position, and an empty sheet
gets no header. A sheet with rows but no header row fails with `no header
row found; use header: false to append rows by position`.

### Updating rows

`xlsx.update_rows` takes `rows` (objects, each with the `key` column and
optionally `_row`), `key` (a column name, or `_row` to address rows by
number alone), and `set`:

```yaml
set:
  Status: status            # a field of each row
  Reviewed: {value: "yes"}  # one literal for every row
```

A literal that is text in the canonical form of a number, `100` or `-12.5`,
is written as a number, since a reference interpolated into `with` arrives
as text; `{value: "007", type: string}` pins text, and `type` takes any
column type. Without `set`, every field other than the key and `_row` goes
to the column of the same name. A field in `set` that no row carries is an
error:
`set.Status: field "state" is not in any row`. A column that is not in the
header row is added at the right, its header cell copying the style of the
last header cell; a new header cell inside a merged cell fails as it does
for an append.

Rows are matched by `_row` when present, else by the key column, with keys
compared as trimmed text so `7` and `"7"` match. A key found at two rows is
an error: `key "INV-1" appears at rows 5 and 9`. A key not found does what
`missing` says: `fail` (default) with `key "INV-99" not found`, `skip` with
the warning `Orders: key "INV-99" not found; row skipped`, or `append`
below the last used row, copying the styles of the row above. With
`key: _row`, `missing` must be `fail`.

`rows` may also be the aggregate output of a `foreach` step, an object with
`summary`, `items`, and `outputs`; its `outputs` list, the collected
objects of the item bodies, is used. Two input rows that address the same
sheet row are an error: `rows[0] and rows[2] both address row 17`.

Only the columns in `set` change. A row that does not carry a mapped field
leaves that cell as it is; an explicit null empties it. A cell whose value
already matches, compared with its type so the number 7 and the text `7`
differ and text exactly, surrounding space included, is not counted as
changed. A date written into a date column keeps
the column's format; a date written elsewhere gets a date format. The key
column cannot be in `set`: `set.Invoice No: the key column cannot be
updated`.

A cell inside a merged cell is the merged cell: it is compared, written,
and styled at its top-left cell, the one Excel shows. Rows that share a
merged cell, such as the lines of one order under a merged Status, may
write it the same value, which is written once and counted once in
`cells_changed`; different values fail the step with `rows[0] and rows[1]
write different values to merged cell Sheet1!C2:C3`, a row that leaves the
cell as it already is included.

### Shape checks

Three checks run before anything is saved, and any failure aborts the step
with the workbook untouched. They cannot be turned off.

1. The key column and every column in `set` that already exists must still be
   in the header row by exact name. A header that matches only
   ignoring case or spacing is reported with the name found.
2. Every row carrying `_row` must still hold its key at that row.
3. A merged cell to write, including one an appended row would land in,
   must lie within one column of the data rows; one reaching into another
   column such as the key, into the header, or below the data rows would
   carry the write there: `orders.xlsx Sheet1!C4: merged cell A4:C4
   reaches outside column "Status" of the data rows; unmerge it to write
   this cell`.

### Change summary

Every writer publishes `changes`:

```json
{"sheet": "Orders", "range": "Orders!A2:F148", "rows_updated": 147,
 "rows_appended": 0, "columns_added": 1, "cells_changed": 294}
```

`range` is the bounding box of the cells written, the header row included
when the writer wrote it. `dry_run: true` computes the same summary and
saves nothing.

### Atomic save and locks

A save writes a short-named temporary file beside the workbook, gives it the
target's permission bits, and renames it over the target, so a crash never
leaves a half-written workbook; a symbolic link is followed so the workbook
it points to is replaced. `atomic: false` saves in place. Excel's
`~$name.xlsx` lock file is checked before a write: a lock file another
process still holds open, which on Windows means Excel has the workbook,
fails the step; a lock file nobody holds is a leftover of a crash, so the
step warns and continues: on Windows, where the hold can be probed,
`~$orders.xlsx exists but no program holds it; the workbook may have been
closed without cleanup`, elsewhere `~$orders.xlsx exists; the workbook may
be open in another program, or the lock file may be a leftover`. A Windows
sharing violation on open, save, or
rename fails the same way. `wait_for_unlock: 5m` retries a locked workbook,
waiting two seconds and doubling to one minute, and logs each wait; the
whole open-modify-save sequence runs again on each try.

### Artifacts

`artifact: true` on a writer copies the saved workbook under
`xlsx/<step>/` in the run's artifacts directory and publishes its relative
path as `artifact`. The option enables artifact storage for the DAG, as
does a value reference such as `${params.KEEP}` whose value is only known
at run time. A dry run copies nothing and publishes no `artifact`. A copy
that fails after the workbook
was saved is reported as a warning, not as a failed step, so a retry does
not repeat a write that already happened.

### CLI

`dagu xlsx inspect <path> [--rows N] [--sheet NAME] [--format text|json]`
prints what `xlsx.info` publishes plus up to N typed sample rows per sheet;
`--sheet` keeps one sheet, and a name that is not in the workbook fails
with `sheet "Nope" not found; sheets present: ...`. `dagu xlsx read <path>
[--sheet] [--range] [--header] [--columns] [--max-rows] [--format]` prints
what `xlsx.read` publishes, the flags taking the values the fields take.
Both read the file directly, create no run, and exit non-zero with the
error on stderr. The `text` format of `inspect` prints a heading,
`orders.xlsx: 2 sheets, 1900 date system`, then one line per sheet,
`Sheet "Orders": used A1:C3, table Orders!A1:C3, header row 1, 2 rows`,
followed by the columns with their kinds and the sample rows; the `text`
format of `read` prints a tab-separated header line starting with `_row`
and one line per row.

`dagu xlsx cache clear <DAG> [--step ID]` removes the cached cell
addresses of a DAG's `xlsx.extract` steps on this host, printing `Removed
xlsx replay cache for step "fields" of DAG "quotes"` for each step removed
or `No xlsx replay cache for DAG "quotes"`; `dagu rm --history` removes
them together with the run history. A `dagu xlsx extract` command that
runs one extraction outside a run needs a model configured outside a DAG
and is a later addition.

### MCP

The `dagu_read` target `workbook` takes `path`, any workbook the server
process can read, and returns the `inspect` description with five sample
rows per sheet. The path is recorded in the audit log as `workbook_path`.
Spec 021 defines the target's fields and errors.

### Validating

`xlsx.validate` reads a sheet the way `xlsx.read` does (`sheet`, `range`,
`header`, `columns`, `merged`, `trim`, `formulas`) and checks every
non-empty row against rules: `required` lists columns the header row must
have, `not_blank` columns no row may leave empty, `unique` columns whose
values may not repeat, `types` columns whose cells must convert, and
`allowed` the values a column's cells may hold. Names in the rules match a
header exactly, loosely (ignoring case and spacing), or through a
`columns` alias. `unique` and `allowed` skip empty cells; `not_blank` is
the rule for those. At least one rule is required.

Every failed check is one problem with a `code`, the `sheet`, the `cell`
and `row` (absent for a missing column), the `column` as the header reads,
and a `message`:

| Code | Problem |
| --- | --- |
| `missing_column` | A rule names a column the header row does not have; that rule is skipped. |
| `blank` | A `not_blank` column is empty: `Status is blank`. |
| `type` | A cell cannot convert to its column's type: `expected number, found "N/A"`. |
| `duplicate` | A `unique` value seen before: `duplicate value "INV-1"; first at row 5`. |
| `not_allowed` | A value outside the allowed list: `value "Pending" is not one of Done, Open`. |

`count` is every problem found, `rows` the non-empty rows checked, and `ok`
is true when `count` is zero. Problems are listed with the missing columns
first, then row by row, and within a row in rule order: `not_blank`,
`types`, `unique`, `allowed`. `problems` keeps at most `max_problems`
(default 1000) and is fitted to the output budget like `rows`; `truncated`
says when either cut it, and the budget cut adds the warning `problems
truncated to 2 of 4; narrow the range or set max_problems`. The stdout
line is `Validated 147 rows in orders.xlsx Orders!A1:F148: 3 problems`,
`1 problem`, or `no problems`. Each kept problem also goes to stderr as
`problem: Sheet!Cell: message`, or `problem: Sheet: message` for a missing
column, which has no cell.

With `on_problem: warn`, the default, the step succeeds with the problems
published. With `on_problem: fail` the step fails after listing them, with
`N problems found in <workbook> <sheet>`; a failed step publishes no
outputs (Spec 012), so a workflow that must act on the problems keeps the
default.

To stop and ask someone when a workbook has problems, follow the
validation with a `human.task` step whose precondition is the count, and
let the rest of the workflow continue past a skipped task:

```yaml
  - id: check
    action: xlsx.validate
    with:
      path: orders.xlsx
      required: [Invoice No, Amount]
      allowed: {Status: [Open, Done]}
  - id: review
    depends: check
    action: human.task
    preconditions:
      - condition: "${steps.check.outputs.count}"
        expected: "num:>0"
    continue_on:
      skipped: true
    with:
      prompt: Fix the problems in orders.xlsx and continue
```

With no problems the task is skipped and the run goes on; with problems
the run waits for the task to be completed (Spec 031) and continues from
there.

### Extracting fields from a form

`xlsx.extract` reads a sheet laid out as a form rather than a table: a
quote, an order, or an application whose layout differs by sender.
`instruction` says what to find, and `schema` (`type: object`) names the
fields as properties. A model is asked which cell holds each field; the
step then reads the typed value from that cell. The model never returns a
value, so every non-null output has a cell behind it and no number is
invented.

The model is sent the sheet's non-empty cells in its used range, or in
`range`, one per line as `B3 [text,bold]: 見積番号`: the address, the kind
the cell's type and number format give (`text`, `number`, `date`,
`datetime`, `time`, `bool`), `bold` and `fill` when the cell has them, and
the text with line breaks as spaces, cut to 200 characters; a merged
region is listed once as `A1:D1 [text,bold]: 御見積書`. `send_values:
false` lists a non-text cell as its kind only, `B7 [number]`, so amounts
and dates stay on the host. More than 2000 listed cells fails with
`quote.xlsx Sheet1: 2415 cells in Sheet1!A1:H600 is more than 2000; set
range to the part of the sheet that holds the fields`. A listing longer
than 200 KB fails the same way before any request, so a sheet that could
not fit a model context is refused on the host: `quote.xlsx Sheet1: the
listing of Sheet1!A1:H600 is 412 KB, more than 200 KB; set range to the
part of the sheet that holds the fields`. Nothing else is sent: no
variables and no screenshots. An `instruction` or a property
`description` that holds the value of a declared secret of four or more
characters fails before any model call, and secret values are masked in
the text sent.

The model answers through one `respond` tool whose parameters list each
property as an A1 address, with or without a sheet, or `null` for a field
the sheet lacks, and `labels`, the addresses of the cells that are labels
or headings rather than values, which the cache watches; a property named
`labels` is refused at validation. An answer that is not an object, that
names something other than one cell within the listed sheet and range, or
whose `labels` is not a list of addresses, is unusable: the
next configured model is asked, and when every model's answer is unusable
the step fails with `model request failed: <provider>/<model>: ...` naming
each answer's fault. An empty cell within the range is a null value, since
a form may leave a box blank. `trim` and `formulas` read the cell as `xlsx.read`
would. A property's `type` pins the cell: `number`, `integer`, `boolean`,
`string`, and `string` with `format: date` or `date-time`; a property
without a type gets the cell's own typed value. A cell that fails its type
fails the step with the usual `quote.xlsx Sheet1!B7: expected number,
found "n/a"`; Japanese text such as `￥123,000` or `令和8年10月15日` is read
as Typing describes. An empty cell or an absent field is null.

The step takes the DAG-level `llm` block, or `with.llm`, which replaces it,
as browser steps do. Several models are tried in order, each set up when
its turn comes, and an unusable answer or a model that cannot be set up
counts as a failed request. The outputs are
each property, `cells` (property to the `Sheet1!B7` it was read from,
empty when absent), `sheet`, `warnings`, and `source`, `model` or `cache`.
The stdout line is `Extracted 4 fields from quote.xlsx Sheet1 (model, 1234
tokens)` or `Extracted 4 fields from quote.xlsx Sheet1 (cache)`.

With `cache: true`, the default, the answered addresses are kept by sheet
layout under `<data dir>/xlsx/cache/<dag>/<step>.json`. The layout is the
sheet's shape: which cells hold something, their kinds and emphasis, and
the merged regions, so two forms from one sender in the same template
share it while their values differ. The entry also keeps a digest of the
text of every label the model named, and of the label beside each
answered cell, the nearest text cell to its left or above, and the
addresses of the cells that were listed. A later run whose layout,
instruction, and schema (the property names, types, formats, and
descriptions) match, and whose labels all still read the same, reads the
cached cells without a model call (`source: cache`). A run of another
layout is served by an entry of the same instruction and schema whose
labels all hold and whose listed cells include every cell of this run,
so a form of the same template with a box left blank is read from the
cache, with the blank field null, while a form that lists a cell no
entry has seen asks the model, so a blank box recorded earlier never
hides a value. A layout no entry serves asks the model; an entry whose
instruction or schema changed, whose label moved or was renamed, or
whose address no longer names one cell is replaced after the model
answers. A label renamed in place is noticed wherever the model named a
label, including the label of a field the sheet lacked; a text cell the
model did not name is not watched. An entry is kept only when the step
succeeds; a failed step changes nothing. `cache: false` asks the model
every run.

`dagu dry` checks that the workbook and the sheet exist, nothing about the
model.

### Writing cells

`xlsx.write_cells` fills named cells of an existing workbook, the way a
template is filled. `cells` maps addresses to what they receive: a cell
such as `B2`, `Sheet1!B2`, or `'My Sheet'!B2`, or a defined name that
refers to one cell; an address without a sheet uses `sheet`, the first
sheet by default. A range, a named range, or a table is refused with
`"A1:B2" is not a single cell`, and two addresses that name the same cell,
such as `B3` and `$B$3`, with `"B3" and "$B$3" name the same cell Sheet1!B3`.
A merged cell is one cell: an address inside it, or a range or defined name
covering exactly its area, as Excel names a merged cell, writes its
top-left cell, and two addresses inside one merged cell fail with `"B10"
and "C10" name the same merged cell Sheet1!B10:D10`.
`merge` lists ranges to merge before the cells are written, such as
`[A1:D1, B10:D10]`, so a filled template can carry a title across its
width; an address inside a merged range then writes its top-left cell.
A range that is already merged exactly is left as it is. A range that
overlaps another merged region fails with `template.xlsx: merge A1:D1
overlaps merged cell Sheet1!B1:E1`, and one covering a cell other than
its top-left that holds a value or formula fails with `template.xlsx:
merge B10:D10 would discard the value of Sheet1!C10; clear it first`,
since Excel keeps only the top-left value of a merged cell; clear the
cell in an earlier step.
A value is written as a cell value, with an
ISO date or date-time string becoming a date and text in the canonical
form of a number becoming a number: an optional leading `-`, digits with
no leading zero, an optional fraction, so `100` and `-12.5` are numbers
while `007`, `1,234`, `1e3`, and padded or full-width digits stay text.
This is what lets a reference such as `${foreach.item.amount}`, which
arrives as text, land as a number. `{value: v, type: t}` pins the type, so
`{value: "007", type: string}` and `{value: "100", type: string}` stay
text; `{formula: text}`
writes a formula, with or without a leading `=`, replacing the value the
cell held, so a read of the cell evaluates the formula; `null` empties the
cell and removes its formula. Every cell keeps its style, and a date written
into a cell with no date format gains one. A cell that already holds the
value, or the formula, is not a change; text and a date that read the
same, such as the text `2026-10-01` and that date, are told apart by what
the cell stores, so a date written over text is a change.

The workbook must exist: a template fill needs a template, and `xlsx.write`
creates workbooks. With `output`, the result is written there and the
workbook at `path` is left as it was, so one template serves many fills;
`output` must be a different file from `path`, a hard link included, else
`output must be a different file from path; leave output out to fill the
workbook in place`; the output path takes the saved file's place in `path`
and `artifact`.
`changes` reports `cells_changed`, `rows_updated` as the distinct rows a
changed cell was on, `sheet` and `range` as the default sheet and the
bounding box of the cells changed on it, and `merged`, the ranges newly
merged, when there are any; the stdout line then ends with `(2 ranges
merged)`. `dry_run`, `atomic`,
`wait_for_unlock`, and `artifact` apply as for `xlsx.write`.

### Sheets

`xlsx.sheet` runs one `operation` on a workbook that must exist: `add`
creates the sheet named `sheet`; `copy` duplicates `sheet` as `to`;
`rename` gives `sheet` the name `to`; `delete` removes `sheet`. `position`
places a sheet `add` or `copy` creates, 1-based; by default an added sheet
goes last and a copy right after its source.

A rerun must be safe, so the modes say what happens when the workbook is
not as expected. `if_exists` applies to the sheet `add`, `copy`, and
`rename` would create: `fail` (default) with `sheet "October" already
exists`, `skip` with the warning `sheet "October" already exists; nothing
added` (or `copied`, `renamed`) and nothing changed, or `replace`, which
empties an added sheet in place, copies over the existing sheet keeping its
position, or drops the sheet in a rename's way. `missing` applies to the
sheet `copy`, `rename`, and `delete` start from: `fail` (default) or `skip`
with the warning `sheet "Template" not found; nothing copied` (or
`renamed`, `deleted`). Deleting the only sheet fails with `cannot delete
the only sheet "Sheet1"`. A rename to the name the sheet already has is a
skip with the warning `sheet "October" already has that name; nothing
renamed`; a rename that only changes case is applied.

The outputs are those of a writer plus `sheets`, the sheet names
afterwards; `sheet` is the sheet the operation produced or removed. The
underlying library sets three limits, stated here so a workflow can plan
for them: a rename does not rewrite formulas on other sheets that name the
sheet, though defined names follow it; a delete leaves such references
dangling and drops the names scoped to the sheet; and a copy carries
cells, styles, widths, merged regions, and validations but not tables,
images, charts, or page setup.

### Converting

`xlsx.convert` writes the rows of a sheet to the file named by `output`,
as `csv`, `json`, or `jsonl` by the file's extension (`.ndjson` counts as
jsonl) or by `format`. It reads the way `xlsx.read` does (`sheet`,
`range`, `header`, `columns`, `types`, `trim`, `merged`, `formulas`) but
with every row and no output budget, since the rows go to a file, and a
cell that fails a pinned type fails the step. Columns follow the header
order, or `columns`, and the `_row` field is not written: the file is a
table. CSV cells are text as a reader would show them, with dates in ISO
form and booleans as `true` and `false`; JSON keeps the typed values. The
file is written through a temporary file in its directory and renamed into
place unless `atomic: false`.

CSV takes `encoding`: `utf-8` (default, no byte order mark), `utf-8-bom`
for Excel to open the file as UTF-8 by double click, or `shift_jis` as
Windows uses it (code page 932, Windows-31J; `cp932`, `windows-31j`,
`sjis`, and `ms932` are accepted spellings), and `delimiter`, one
character, a comma by default. The outputs are `path`, `format`, `count`,
`sheet`, `range`, `warnings`, and with `artifact: true` the file's copy.
The other direction, a csv, json, or jsonl file into a workbook, is
`xlsx.write` with `input`.

### Input encoding

The `input` file of `xlsx.write` and `xlsx.append` takes the same
`encoding` and `delimiter` when it is csv; a byte order mark is dropped
either way. Both fields require `input`.

### Dry-run checks

`dagu dry` (Spec 064) runs a check for every xlsx step and warns, without
failing, when the step would fail on this host: the workbook of a reading
or updating operation, of `write_cells`, or of a `sheet` operation other
than `add` does not exist; the `sheet` named is not in it; a column named
in `columns`, `types`, `where`, or a validation rule is not in the header
row, where a `where` key or a rule name may be an alias given in `columns`
while the `types` of a read or convert name headers, as the run requires;
the key or a `set` column of `update_rows` is not in the header row
as written; or the `input` file of `write` or `append` does not exist. The
warning names the field, as `field 'with.sheet': orders.xlsx: sheet "Nope"
not found; sheets present: Orders`, and lists every problem found. A field
whose value still holds a reference to a step output is skipped, since no
step has run; params and environment resolve. A workbook the check cannot
open, such as one another program holds, is left to the run.

## Errors

### Validation

Every one of these is rejected by `dagu validate`:

- Any xlsx action without `with.path`: `path is required for <operation>`.
- `xlsx.write` or `xlsx.append` without `rows` or `input`:
  `<operation> requires with.rows or with.input`; with both:
  `<operation> accepts with.rows or with.input, not both`.
- `xlsx.update_rows` without `key`: `key is required for update_rows`;
  without `rows`: `update_rows requires with.rows`; with `header: false`:
  `update_rows needs a header row; header: false is not supported`.
- A field of another operation, such as `range` on `xlsx.info`:
  `with.range is not valid for xlsx.info`.
- An unknown field: `unexpected additional properties ["strip"]`. A value
  outside its enum is reported by the schema as `<value> does not equal any
  of: [<the accepted values>]`, so `merged: join` gives `join does not equal
  any of: [fill first]`; the enums are `merged` (`fill`, `first`),
  `formulas` (`cached`, `text`, `calculate`), `on_type_error` (`fail`,
  `warn`, `null`), `mode` (`replace`, `append`), `style` (`table`, `none`),
  `format` (`json`, `jsonl`, `csv`), `missing` (`fail`, `skip`, `append`),
  `on_problem` (`warn`, `fail`), `operation` (`add`, `copy`, `rename`,
  `delete`), `if_exists` (`fail`, `skip`, `replace`), `encoding` (`utf-8`,
  `utf-8-bom`, `shift_jis`, `cp932`, `windows-31j`, `sjis`, `ms932`), and a
  type in `types` (`string`, `number`, `integer`, `boolean`, `date`,
  `datetime`).
- `output`, `outputs`, or `stdout.outputs` on an xlsx step:
  `xlsx actions have fixed outputs`.
- `header` of a reader that is not `true`, `false`, a row number, or a
  list of row numbers: `header must be true, false, a row number, or a list
  of row numbers`; `header` of `xlsx.write` or `xlsx.append` that is a row
  number: `header must be true or false for <operation>`; `max_rows` below 1:
  `max_rows must be >= 1`; a `where` operator other than `eq`, `ne`, or
  `in`: `where: Status: unknown operator "like"; use eq, ne, or in`; `set`
  values that are neither a field name nor `{value: literal}`:
  `set.Status: use a field name or {value: literal}`; a `set` literal whose
  `type` is not a column type: `set.Status: type: unknown column type
  "money": use string, number, integer, boolean, date, or datetime`;
  `missing: skip` or
  `append` with `key: _row`: `missing: skip needs a key column; with key:
  _row nothing else identifies a row`; `wait_for_unlock` that is not a
  duration: `wait_for_unlock must be a duration such as 30s or 5m`.
- `xlsx.validate` with no rule: `validate requires at least one of
  with.required, with.not_blank, with.unique, with.types, or with.allowed`;
  `max_problems` below 1: `max_problems must be >= 1`; an `allowed` value
  that is not a list: `allowed.Status must be a list of values`.
- `xlsx.write_cells` without `cells`: `write_cells requires with.cells`;
  empty `cells`: `cells must not be empty`; a cell value of another shape:
  `cells.B2: use a scalar, null, {value: v, type: t}, or {formula: text}`;
  an empty formula: `cells.B2: formula must not be empty`; an `output`
  that is not a workbook: `output: out.csv: only .xlsx and .xlsm workbooks
  are supported; save as .xlsx`; a `merge` item that is not a range of
  cells with both corners, such as a single cell, an open end, or a
  defined name: `merge: "A1" is not a range`; an empty `merge`: `merge must
  not be empty`.
- `xlsx.sheet` without `operation` or `sheet`: `operation is required for
  sheet`, `sheet is required for sheet`; `copy` or `rename` without `to`:
  `to is required for copy`; `to`, `if_exists`, `missing`, or `position`
  on an operation that does not take it: `to is only valid for copy and
  rename`, `if_exists is only valid for add, copy, and rename`, `missing is
  only valid for copy, rename, and delete`, `position is only valid for add
  and copy`; `position` below 1: `position must be >= 1`.
- `xlsx.convert` without `output`: `convert requires with.output`; an
  output whose extension names no format and no `format`: `output
  extension ".txt" is not json, jsonl, or csv; set format`; `encoding` or
  `delimiter` with a format other than csv: `encoding applies to csv only`,
  `delimiter applies to csv only`.
- A `delimiter` that is not one character: `delimiter must be a single
  character`; `encoding` or `delimiter` on `xlsx.write` or `xlsx.append`
  without `input`: `encoding requires with.input`, `delimiter requires
  with.input`.
- `xlsx.extract` without `instruction`: `extract requires
  with.instruction`; without `schema`: `extract requires with.schema`; a
  schema that is not an object schema: `schema must have type: object`;
  `properties` that is not an object: `schema.properties must be an
  object`; a property that is not an object: `schema.properties.items must
  be an object`; a property of another type: `schema.properties.items:
  type must be string, number, integer, or boolean`; a property named like
  a fixed output:
  `schema property "sheet" collides with an output of xlsx.extract`;
  a property named `labels`: `schema property "labels" is reserved by
  xlsx.extract`;
  without a model: `xlsx.extract needs a model: set llm at the DAG level
  or with.llm on the step`; `llm` on another xlsx action: `with.llm is not
  valid for xlsx.read`.

### Runtime

- A path that is not `.xlsx` or `.xlsm`: an error containing
  `only .xlsx and .xlsm workbooks are supported; save as .xlsx`.
- A missing workbook: `<name>: workbook not found`.
- A missing sheet: `sheet "Order" not found; sheets present: Orders, Summary`.
- A range that is none of the accepted forms:
  `range "Totals" is not a cell range, named range, or table`.
- A `columns`, `types`, or `where` name that matches no header:
  `columns: column "Nope" not found; headers present: ...`, with the field
  as the prefix.
- A cell that fails a pinned type:
  `orders.xlsx Orders!D17: expected number, found "N/A"`.
- A key column missing from the header row:
  `key column "Invoice" not found in header row 1; headers present: ...`, or
  with a loose match, `did you mean "Invoice No"?`.
- A `set` column of `update_rows`, or an appended field, with only a loose
  match: `column "status" not found in header row 1; did you mean
  "Status"?`.
- An append onto a sheet with rows but no header row: `no header row found;
  use header: false to append rows by position`.
- A `set` field no row carries: `set.Status: field "state" is not in any
  row`; the key column in `set`: `set.Invoice No: the key column cannot be
  updated`.
- A row that moved: `orders.xlsx Orders!A17: expected key "INV-17", found
  "INV-18"; the sheet changed since it was read`.
- A key not in the sheet with `missing: fail`: `key "INV-99" not found`.
- A key at two rows: `key "INV-1" appears at rows 5 and 9`.
- Two input rows addressing one sheet row: `rows[0] and rows[2] both
  address row 17`.
- Two input rows writing different values to one merged cell: `rows[0] and
  rows[1] write different values to merged cell Sheet1!C2:C3`.
- A merged cell to update reaching outside its column's data rows:
  `orders.xlsx Sheet1!C4: merged cell A4:C4 reaches outside column "Status"
  of the data rows; unmerge it to write this cell`; a cell to append inside
  a merged cell: `Sheet1!A3: cannot append into merged cell A3:B4; unmerge
  it to write this cell`; a new column whose header cell is inside a merged
  cell: `Sheet1!C1: merged cell B1:C1 covers the header cell of new column
  "note"; unmerge it to add the column`.
- A workbook another program holds, on Windows:
  `orders.xlsx is open in another program; close it and retry`.
- `artifact: true` in a DAG whose artifacts are disabled:
  `artifact requires artifact storage`.
- `xlsx.validate` with `on_problem: fail` and any problem:
  `2 problems found in orders.xlsx Orders`.
- A `write_cells` address that is not one cell: `template.xlsx: "A1:B2" is
  not a single cell`; two addresses naming one cell: `template.xlsx: "B3"
  and "$B$3" name the same cell Sheet1!B3`, or one merged cell: `"B10" and
  "C10" name the same merged cell Sheet1!B10:D10`; a `merge` over another
  merged region: `template.xlsx: merge A1:D1 overlaps merged cell
  Sheet1!B1:E1`; a `merge` over a filled cell: `template.xlsx: merge
  B10:D10 would discard the value of Sheet1!C10; clear it first`; a
  workbook to fill that does
  not exist: `template.xlsx: workbook not found`; an `output` that is the
  workbook itself: `template.xlsx: output must be a different file from
  path; leave output out to fill the workbook in place`.
- A sheet to create that exists, with `if_exists: fail`:
  `report.xlsx: sheet "October" already exists`; a copy onto its own
  source: `sheet "Template" cannot be copied onto itself`; a `position`
  past the end: `position 5 is outside 1 to 4`, the upper bound being the
  sheet count plus one; deleting the last sheet: `report.xlsx: cannot
  delete the only sheet "Sheet1"`; a name Excel refuses: `invalid sheet
  name "Bad:Name"`; a source sheet that is not there with `missing: fail`:
  `sheet "Nope" not found; sheets present: Template`.
- A `convert` output that cannot be written, such as into a directory that
  does not exist: `out.csv: <system error>`.
- An `xlsx.extract` instruction holding a secret: `xlsx: with.instruction
  contains the value of secret SHOP_TOKEN, which would be sent to the
  model`, or a property description:
  `xlsx: with.schema.properties.total.description contains the value of
  secret SHOP_TOKEN, which would be sent to the model`; a sheet past the
  cell cap: `quote.xlsx Sheet1: 2415 cells in
  Sheet1!A1:H600 is more than 2000; set range to the part of the sheet that
  holds the fields`; a listing past the size cap: `quote.xlsx Sheet1:
  the listing of Sheet1!A1:H600 is 412 KB, more than 200 KB; set range to
  the part of the sheet that holds the fields`; a used range too large to
  scan, past a million cells:
  `quote.xlsx Sheet1: Sheet1!A1:XFD1048576 spans more than 1000000 cells;
  set range to the part of the sheet that holds the fields`; every model's
  answer unusable or every request failed: `model request failed:
  <provider>/<model>: <fault>` for each model, the fault being `xlsx: model
  answered field "total" with "123000", not a cell address`, `xlsx: model
  answered field "total" with "H40", which is outside Sheet1!A1:D20`,
  `xlsx: model answer is not an object: null`, or the provider's error.

Errors that concern a cell name the workbook, sheet, and cell as
`<workbook> <sheet>!<cell>: <message>`.

## Related Specs

- Step outputs and the output budget: [Spec 012](012-step-outputs.md)
- Built-in run context and the artifacts directory: [Spec 017](017-built-in-run-context.md)
- MCP read tool and the `workbook` target: [Spec 021](021-mcp-read-tool.md)
- Artifacts: [Spec 051](051-artifact.md)
- File actions, whose path handling the xlsx actions share: [Spec 052](052-file.md)
- Mailbox actions, whose output budget and attachment storage the xlsx actions mirror: [Spec 073](073-mailbox-actions.md)
- Preconditions, which gate the human task after a validation: [Spec 023](023-preconditions.md)
- Human tasks, which a validation hands its problems to: [Spec 031](031-human-task.md)
- Dry-run step checks, which the xlsx actions register with: [Spec 064](064-dry-run-step-checks.md)

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
  - id: mark
    depends: each
    action: xlsx.update_rows
    with:
      path: ~/Inbox/orders.xlsx
      sheet: Orders
      key: order_id
      rows: ${RESULTS}
      set:
        Status: status
      wait_for_unlock: 5m
```

`collect` gives each item one object with the key and the result fields,
and `rows` takes the foreach aggregate, published as the variable
`${RESULTS}` (Spec 012), directly, using its `outputs` list. `http.request`
fails on a response outside 2xx, so a rejected order fails its item body;
the loop is then partially succeeded (Spec 018), the aggregate lists the
failed items under `items` and holds only the successful bodies in
`outputs`, and the write-back marks the rows that succeeded. The rejected
rows keep an empty Status, so the next run submits only them; the run ends
partially succeeded, which the run history shows and `dagu status`
reports.

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

Fill an invoice template for each order and keep the copies with the run:

```yaml
steps:
  - id: orders
    action: xlsx.read
    with:
      path: orders.xlsx
      columns: [{Invoice No: invoice}, {Customer: customer}, {Amount: amount}]
  - id: each
    depends: orders
    foreach:
      items: ${steps.orders.outputs.rows}
      steps:
        - id: fill
          action: xlsx.write_cells
          with:
            path: templates/invoice.xlsx
            output: out/invoice-${foreach.item.invoice}.xlsx
            cells:
              Customer: ${foreach.item.customer}
              B7: ${foreach.item.amount}
              B9: {formula: "=B7*1.1"}
            artifact: true
```

Start a new month from the template sheet, safe to rerun:

```yaml
steps:
  - id: query
    action: postgres.query
    with:
      dsn: ${env.REPORTING_DSN}
      query: select item, amount from expenses where month = '${params.MONTH}'
  - id: month
    action: xlsx.sheet
    with:
      path: report.xlsx
      operation: copy
      sheet: Template
      to: ${params.MONTH}
      if_exists: skip
  - id: fill
    depends: [query, month]
    action: xlsx.write
    with:
      path: report.xlsx
      sheet: ${params.MONTH}
      mode: append
      rows: ${steps.query.outputs.rows}
```

Export a sheet for a system that reads Shift_JIS CSV:

```yaml
steps:
  - id: export
    action: xlsx.convert
    with:
      path: orders.xlsx
      sheet: Orders
      output: out/orders.csv
      encoding: shift_jis
      columns: [品名, 数量, 金額]
```

Read a supplier's quote, whatever its layout, into a list of quotes:

```yaml
llm:
  provider: anthropic
  model: claude-sonnet-5
  api_key_name: ANTHROPIC_API_KEY
params:
  - FILE: quote.xlsx
steps:
  - id: fields
    action: xlsx.extract
    with:
      path: inbox/${params.FILE}
      instruction: A supplier's quote. Find the quote number, the delivery date, and the total amount.
      schema:
        type: object
        properties:
          quote_no: {type: string, description: 見積番号}
          delivery: {type: string, format: date, description: 納期}
          total: {type: number, description: 合計金額}
  - id: record
    depends: fields
    action: xlsx.append
    with:
      path: quotes.xlsx
      sheet: Quotes
      rows: '[{"quote_no": "${steps.fields.outputs.quote_no}", "delivery": "${steps.fields.outputs.delivery}", "total": ${steps.fields.outputs.total}, "file": "${params.FILE}"}]'
```

A second quote from the same supplier shares the layout, so its fields are
read from the cached cells without a model call, and the typed values go
into the list the same way.
