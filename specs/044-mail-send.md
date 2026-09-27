# Spec: Mail Send Action

## Status

Partially implemented.

Conformance covers one local delivery and configuration errors. SMTP
authentication, MIME encoding, HTML conversion, and attachments belong in mailer
unit and integration tests.

## Scope

This spec defines the `mail.send` action boundary. It does not define SMTP
protocol internals or message encoding.

## Goal

Workflow authors can send a notification using the DAG's SMTP configuration.

## Behavior

`mail.send` uses the DAG-level `smtp` configuration. The action accepts sender,
recipient, subject, and message fields under `with`. Successful execution delivers
a message with the configured subject and recipient.

With `with.mailbox`, `mail.send` sends through that mail account instead, as
[Spec 073](073-mailbox-actions.md) defines.

## Errors

An empty recipient fails before connecting to SMTP. SMTP OAuth configuration
requires a username and cannot be combined with a password; invalid combinations
fail `dagu validate` with a diagnostic identifying the conflict.

## Examples

```yaml
smtp:
  host: localhost
  port: 2525
steps:
  - action: mail.send
    with:
      from: sender@example.com
      to: recipient@example.com
      subject: Test Subject
      message: hello world
```
