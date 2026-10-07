# Spec 078: JS Run Action

## Status

Implemented.

## Scope

This spec covers `js.run` input binding, output publication, sandbox
boundaries, timeout, and configuration errors. ECMAScript language semantics
and host-object details belong to executor tests.

## Goal

Workflow authors can transform data between steps with a short JavaScript
function without installing Node.js or another interpreter on the host.

## Behavior

`with.script` is a JavaScript function body. Dagu passes the text to the
engine as written: workflow references such as `${steps.a.outputs.x}` are not
resolved inside it, so JavaScript template literals keep their meaning.
Workflow values reach the script through `with.input`.

`with.input` is any YAML value bound to the function's `input` parameter.
Objects and lists arrive as native JavaScript objects and arrays. String values
follow ordinary executor-config reference resolution and contribute inferred
dependencies.

`with.input_file` names a file whose contents are bound to `input`.
`with.format` selects how string input is interpreted. `auto` (default)
parses a string that is a JSON object or array and binds any other string
as-is, `text` always binds the string as-is, and `json` always parses and
rejects invalid JSON. The option applies to `input_file` contents and to a
string `input`. `input` and `input_file` are mutually exclusive. When neither
is set, `input` is `undefined`.

The script body may use `await`. Promises settle through the microtask queue
drained after the body returns; there is no event loop or timer, so a promise
that is still pending afterwards fails the step.

The returned value is written to stdout. `undefined` writes nothing. A string
is written as-is followed by a newline. Any other value is written as JSON
with two-space indentation followed by a newline, using JavaScript
serialization semantics (`toJSON`, `Date`, dropped function properties).

The sandbox exposes the ECMAScript builtins, `console` (whose methods write to
the step's stderr), `URL`, and `URLSearchParams`. It has no `require`,
`fetch`, filesystem access, `process`, or timers.

`with.timeout` bounds script execution, as integer seconds or a duration
string. When it is unset, a step `timeout` governs alone; without either,
`60s` applies. Expiry, step timeout, and a stop request interrupt the engine
and fail the step.

## Errors

`dagu validate` rejects a missing `with.script`, configurations that set
both `with.input` and `with.input_file`, and a script that does not compile.
It exits nonzero with an error identifying the invalid configuration; a
compile error names the script line.

An invalid `with.format`, an invalid `with.timeout`, a missing or unreadable
`input_file`, and invalid JSON under `format: json` fail executor setup.

A thrown value or rejected promise fails the step. The error names the
exception and the script line, and the JavaScript stack trace, with lines and
columns matching the script text, is written to stderr. A script that
returns `undefined` succeeds with empty stdout and a notice on stderr. A
script that exceeds `with.timeout` fails the step.

Memory use is not bounded, and the interrupt takes effect between JavaScript
instructions only, so a single long-running builtin call cannot be stopped.

## Examples

```yaml
steps:
  - id: links
    action: js.run
    with:
      input:
        html: '<a href="/a">x</a>'
      script: |
        const urls = [];
        for (const m of input.html.matchAll(/href="([^"]+)"/g)) {
          urls.push(new URL(m[1], "https://example.com").href);
        }
        return urls;
    output: LINKS
```

## Conformance

`conformance/spec078_js_run/` runs fixtures with `dagu start` and checks the
written stdout for each output shape, inline and file input, template
literals, console output, and failures from thrown errors and timeouts. It
checks `dagu validate` for the configuration errors above.
