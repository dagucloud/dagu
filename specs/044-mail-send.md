# Spec: Mail Send Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `mail.send` action.

## Scope

This spec defines `mail.send`, which sends an email over SMTP.

This spec covers:

- the message fields `with.from`, `with.to`, `with.subject`,
  `with.message`, and `with.attachments`
- `with.to` accepting either a single address (a string) or multiple
  addresses (an array of strings)
- the DAG-level `smtp.host`, `smtp.port`, `smtp.username`, and
  `smtp.password` fields that configure the SMTP connection and
  authentication `mail.send` uses
- that an SMTP connection authenticates only when `smtp.username` or
  `smtp.password` is set, otherwise it sends unauthenticated
- that plain-text `with.message` content has its newlines converted to
  `<br />`, while a message that is already an HTML document (detected by
  a leading `<!DOCTYPE html>`) is sent unmodified
- the DAG-level `smtp.oauth` field's build-time validation against
  `smtp.password` and `smtp.username`
- validation and runtime errors

This spec does not define:

- the SMTP OAuth (XOAUTH2) authentication flow itself, or any behavior
  specific to a particular OAuth provider -- only the build-time
  validation described above
- STARTTLS negotiation
- the `error_mail`/`info_mail` DAG lifecycle notification emails, which
  reuse this action's SMTP configuration and message composition but are
  triggered by DAG lifecycle events, not by a `mail.send` step
- SMTP protocol details beyond what a step author can observe (exact
  MIME structure, header ordering, and so on)

## Goal

Workflow authors send notification emails as a DAG step, using the same
SMTP server configuration for every `mail.send` step in a DAG.

## Behavior

### Message fields

`with.from`, `with.subject`, and `with.message` are plain strings.
`with.to` accepts a single address as a string, or multiple addresses as
an array of strings; at least one non-empty address must result, or the
step fails (see "Errors"). `with.attachments` is an optional list of local
file paths attached to the message.

A `with.message` value is sent as-is when it is an HTML document (its
trimmed content starts with `<!DOCTYPE html>`, case-insensitively);
otherwise every newline in it is converted to `<br />` before sending.

### SMTP connection

The SMTP server is configured once per DAG, at the DAG level:
`smtp.host`, `smtp.port`, `smtp.username`, `smtp.password`. Every
`mail.send` step in the DAG uses this same configuration; it is not
something an individual step's `with:` fields set.

The connection authenticates only when `smtp.username` or
`smtp.password` is set on the DAG's `smtp:` block; with neither set, the
step connects and sends without authenticating.

## Errors

### Validation

- `smtp.oauth` set together with a non-empty `smtp.password`: rejected at
  DAG-build-time validation, with an error containing `"mutually
  exclusive"`.
- `smtp.oauth` set without `smtp.username`: rejected at DAG-build-time
  validation, with an error containing `"username is required with
  oauth"`.
- `with.to` resolving to no valid address (for example, an empty
  string): a runtime error, surfaced only when the step actually runs,
  containing `"no valid recipients specified"` -- this is not checked at
  DAG-build time.

### Runtime

- No DAG-level `smtp:` block is configured (or the configured host/port
  cannot be reached): a connection-time runtime error, containing
  `"connection refused"` when nothing is listening on the target
  host/port.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Send a plain-text notification to one recipient:

```yaml
smtp:
  host: smtp.example.com
  port: 587
  username: notifier@example.com
  password: ${SMTP_PASSWORD}
steps:
  - action: mail.send
    with:
      from: notifier@example.com
      to: oncall@example.com
      subject: Deployment finished
      message: "The deployment completed successfully."
```

Send to multiple recipients with an attachment:

```yaml
smtp:
  host: smtp.example.com
  port: 587
steps:
  - action: mail.send
    with:
      from: reports@example.com
      to:
        - team-a@example.com
        - team-b@example.com
      subject: Weekly report
      message: "See the attached report."
      attachments:
        - ./report.csv
```
