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
`with.format` selects how string input is interpreted: `text` (default) binds
the string as-is, `json` parses it first. The option applies to `input_file`
contents and to a string `input`. `input` and `input_file` are mutually
exclusive. When neither is set, `input` is `undefined`.

The returned value is written to stdout. `undefined` writes nothing. A string
is written as-is followed by a newline. Any other value is written as JSON
with two-space indentation followed by a newline, using JavaScript
serialization semantics (`toJSON`, `Date`, dropped function properties).

The sandbox exposes the ECMAScript builtins, `console` (whose methods write to
the step's stderr), `URL`, and `URLSearchParams`. It has no `require`,
`fetch`, filesystem access, `process`, timers, or event loop.

`with.timeout` bounds script execution and defaults to `60s`. Expiry, step
timeout, and a stop request interrupt the engine and fail the step.

## Errors

`dagu validate` rejects a missing `with.script` and configurations that set
both `with.input` and `with.input_file`. It exits nonzero with an error
identifying the invalid configuration.

An invalid `with.format`, an invalid `with.timeout`, a missing or unreadable
`input_file`, invalid JSON under `format: json`, and a syntax error in the
script fail executor setup.

A thrown value fails the step. The error names the exception, and the
JavaScript stack trace, which includes the script line number, is written to
stderr. A script that exceeds `with.timeout` fails the step.

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
