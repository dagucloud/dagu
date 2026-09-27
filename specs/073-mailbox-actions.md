# Spec: Mailbox Actions

## Status

Partially implemented.

Conformance covers mail account configuration errors, `mail.search`,
`mail.organize`, attachments, and `mail.send` and replies through password accounts against
in-process TLS servers. OAuth token exchange with Google and Microsoft token
endpoints belongs in unit tests.

## Scope

This spec defines DAG-level mail accounts, the `mail.search` and `mail.organize`
actions, and sending through a mail account with `mail.send`. It covers
configuration, validation, action inputs, published outputs, and the observable
effects on a mailbox.

IMAP and SMTP protocol details, MIME decoding, and provider token endpoints belong
to executor unit and integration tests.

Out of scope: obtaining an OAuth grant interactively, storing refresh tokens that a
provider returns, per-email triggers, and provider APIs other than IMAP and SMTP.

## Goal

Workflow authors can find, organize, and send email in an existing mailbox, and
build workflows that process each incoming email once.

## Behavior

### Mail accounts

`mail_accounts` is a DAG-level map keyed by email address. A base config may define
it. A DAG entry replaces the base entry with the same address, compared
case-insensitively; other base entries remain.

| Field | Required | Meaning |
| --- | --- | --- |
| `provider` | No | `google`, `microsoft`, or `imap` (default). Sets the default servers. |
| `imap` | For an `imap` account | `host`, `port`, `security` (`tls` or `starttls`), and `skip_tls_verify`. Overrides the provider default. |
| `smtp` | For `mail.send` through an `imap` account | Same fields as `imap`. |
| `username` | No | Login name for password authentication and the user in `XOAUTH2`. Defaults to the address. |
| `password` | Exactly one of `password` or `oauth` | Password or app password. |
| `oauth` | Exactly one of `password` or `oauth` | OAuth credentials, below. |

Provider defaults:

| Provider | IMAP | SMTP |
| --- | --- | --- |
| `google` | `imap.gmail.com:993`, `tls` | `smtp.gmail.com:465`, `tls` |
| `microsoft` | `outlook.office365.com:993`, `tls` | `smtp.office365.com:587`, `starttls` |

A server block overrides the provider default field by field. `security`
defaults to `tls`. `port` defaults to the standard port for the protocol and
security mode: 993 for IMAP or 465 for SMTP with `tls`, and 143 or 587 with
`starttls`; a block that keeps the provider's security mode keeps its port.

Account fields resolve at run start like other DAG fields. Values that come from
secrets or profile secrets are masked as [Spec 069](069-secrets-providers.md) and
[Spec 070](070-runtime-profiles.md) define.

Every connection uses TLS. `tls` connects with TLS; `starttls` upgrades the
connection and fails when the server does not offer STARTTLS. The server
certificate is verified unless the server block sets `skip_tls_verify: true`,
which accepts any certificate, such as a self-signed one.

### Authentication

`password` accounts authenticate with `username` and `password`.

`oauth` has the shape of `smtp.oauth`. Mail accounts accept two providers:

| `oauth.provider` | Fields |
| --- | --- |
| `google_refresh` | `client_id`, `client_secret`, `refresh_token` |
| `microsoft_refresh` | `client_id`, `refresh_token`; optional `tenant_id` (default `common`), `client_secret`, and `scopes` |

`microsoft_refresh` refreshes for `https://outlook.office.com/.default`, the mail
permissions the person granted, unless `scopes` lists the scopes to request
instead; `offline_access` is always requested. `google_refresh` does not accept
`scopes`.

Before each IMAP or SMTP connection, the action exchanges the refresh token for an
access token and authenticates with SASL `XOAUTH2`. Access tokens never appear in
logs, outputs, or run status. The action does not store a refresh token that the
provider returns; the configured token stays in effect.

### `mail.search`

| `with` field | Required | Meaning |
| --- | --- | --- |
| `mailbox` | Yes | Address of a mail account |
| `folder` | No | Folder to search; default `INBOX` |
| `unread` | No | `true` returns only unread email |
| `from` | No | Substring of the sender |
| `subject` | No | Substring of the subject |
| `within` | No | Duration such as `30m`, `24h`, or `7d`; returns email received within it |
| `has_attachments` | No | `true` returns only email with at least one attachment |
| `save_attachments` | No | `true` saves attachments (below) |
| `limit` | No | 1 to 50; default 20 |

An email matches when it satisfies every given filter. Substring matching is
case-insensitive. Results are the oldest matching emails first, up to `limit`.
Searching never changes an email's flags; in particular, it does not mark email
read.

Published outputs, following [Spec 012](012-step-outputs.md):

- `messages`: a JSON array in result order. Each element is an object with these
  top-level fields: `id`, `message_id`, `folder`, `from_name`, `from_address`, `to` (array of
  addresses), `cc` (array of addresses), `subject`, `date` (RFC 3339), `unread`,
  `flagged`, `text`, and `attachments` (array of `{name, content_type, size,
  path}`).
- `count`: the number of elements in `messages`.
- `truncated`: `true` when any `text` was shortened, or any email left out, to fit
  the output limit.

`text` is the plain-text body, or the HTML body converted to text when the email
has no plain-text part, at most 10,000 characters. When the encoded outputs would
exceed the output budget, `text` values are shortened further and `truncated` is
`true`. If the outputs still exceed it, the newest emails are left out of
`messages`, so the oldest stay first in line. The budget is 900 KiB, or the DAG's
`max_output_size` less 64 KiB when that is smaller.

A `mail.search` step whose `save_attachments` is written as `true` enables
artifact storage for the run, as a reference to `context.paths.artifacts_dir`
does ([Spec 017](017-built-in-run-context.md)). With `save_attachments: true`, the
step writes each attachment under `mail/<step name>/` in the run's artifact
directory. File names are made safe for the filesystem and prefixed with a number
that keeps them unique within the step. `path` is the file's absolute path.
Without `save_attachments`, `path` is absent. A step that saves attachments while
artifact storage is off fails with `save_attachments requires artifact storage`.

### `mail.organize`

| `with` field | Required | Meaning |
| --- | --- | --- |
| `mailbox` | Yes | Address of a mail account |
| `emails` | Yes | One item or an array of items |
| `mark` | One of `mark` or `move` | `read`, `unread`, `flagged`, or `unflagged` |
| `move` | One of `mark` or `move` | `folder`, `archive`, or `trash` |
| `folder` | For `move: folder`, unless every item has `move_to` | Destination folder |
| `dry_run` | No | `true` changes nothing |

An item is an email ID string, or an object with an `id` field, such as an element
of `mail.search`'s `messages`. An item object may also have a `move_to` field, which
overrides `with.folder` for that email when `move` is `folder`. An element of
`messages` carries `folder`, which names where the email is, not where to move it. A string value of
`emails` that parses as a JSON object or array is decoded, so a whole
`${foreach.<as>}` item can be passed; any other string is one ID.

For each item, the action applies `mark`, then `move`:

- `folder` moves the email to the destination folder, creating the folder when it
  does not exist.
- `archive` moves the email to the account's archive folder: the folder with IMAP
  special-use `\Archive`, or `\All` when no folder has `\Archive`.
- `trash` moves the email to the folder with special-use `\Trash`.

No action deletes email permanently. An item whose email is no longer in its folder
(moved, deleted, the folder deleted, or the folder's UIDVALIDITY changed) is skipped
and listed in `missing`; it does not fail the step. A folder counts as deleted only
when the server says it does not exist (`NONEXISTENT`); any other refusal to open
it fails the step with the server's reason.

Published outputs:

- `changed`: the number of emails changed, or with `dry_run`, the number that would
  change.
- `missing`: a JSON array of the IDs that were skipped.

### Sending through a mail account

`mail.send` with `with.mailbox` sends through that account's SMTP server and
authentication instead of the DAG-level `smtp` configuration. `from` is optional
and defaults to the mailbox address. Every other field and behavior follows
[Spec 044](044-mail-send.md).

`with.in_reply_to` makes the message a reply to one email of that mailbox. It
takes an email ID or an email object with an `id`, as `mail.organize` items do,
and requires `with.mailbox`. Before sending, the action reads the email over
IMAP without changing it:

- `to` defaults to the email's Reply-To address, or its sender when it has none.
- `subject` defaults to `Re: ` followed by the email's subject, unless that
  subject already starts with `Re:`, compared case-insensitively.
- The message carries `In-Reply-To` with the email's Message-ID and `References`
  with the email's References followed by its Message-ID, so mail clients show
  it in the same thread. An email without a Message-ID gets a reply without
  these headers.

Explicit `to` and `subject` values replace the defaults.

### Email IDs

An email ID is an opaque string. It stays valid for the same account while the
email stays in its folder and the folder's UIDVALIDITY is unchanged.

`message_id` is the email's Message-ID header without angle brackets, or empty
when the email has none. It does not change when the email moves, so a workflow
can use it to recognize an email it already handled.

## Errors

Configuration errors fail `dagu validate` when the value is static, and otherwise
fail the step when it starts, before connecting:

- An account with both `password` and `oauth`, or neither:
  `mail account "<address>": set exactly one of password or oauth`.
- An `imap` account without `imap.host`:
  `mail account "<address>": imap.host is required`.
- An `oauth.provider` other than `google_refresh` or `microsoft_refresh`, or a
  missing OAuth field: the error names the account and the field.
- `security` other than `tls` or `starttls`.
- `security` given as a value reference without `port`:
  `mail account "<address>": imap.port is required when imap.security is a value reference`
  (or `smtp.` for the SMTP block).
- `limit` outside 1 to 50, `within` that is not a duration, or an unknown `mark` or
  `move` value: the error names the field.
- `mail.organize` with neither `mark` nor `move`.

At step start, before connecting:

- `mailbox` names no configured account:
  `mail account "<address>" is not configured`.
- `mail.send` through an `imap` account without `smtp.host`.
- `in_reply_to` without `mailbox`: `in_reply_to requires mailbox`.
- `in_reply_to` with a malformed email ID, or naming more than one email.
- `move: folder` with no `with.folder` while an item has no `move_to`.
- A malformed email ID.

At run time:

- An authentication failure fails the step with an error naming the account and
  the server's or token endpoint's reason, for example
  `mail account "<address>": sign-in is no longer valid (invalid_grant)`.
- `in_reply_to` naming an email that is no longer in its folder fails the step
  before sending, with an error containing
  `in_reply_to: the email is no longer in its folder`.
- A connection failure or timeout fails the step. An IMAP connection that
  transfers nothing for two minutes counts as failed. Changes that
  `mail.organize` already applied stay applied.
- `mail.search` with `save_attachments` while artifact storage is off:
  `save_attachments requires artifact storage`.

## Examples

Process each incoming email once, marking each email read after its work succeeds:

```yaml
secrets:
  - name: SUPPORT_MAIL_TOKEN
    ref: mail/support
mail_accounts:
  support@example.com:
    provider: microsoft
    oauth:
      provider: microsoft_refresh
      client_id: 00000000-0000-0000-0000-000000000000
      refresh_token: ${SUPPORT_MAIL_TOKEN}
schedule: "*/5 * * * *"
steps:
  - id: find
    action: mail.search
    with:
      mailbox: support@example.com
      unread: true

  - id: each
    depends: find
    foreach:
      items: ${steps.find.outputs.messages}
      as: email
      key: ${foreach.email.id}
      steps:
        - id: ticket
          run: ./create-ticket.sh "${foreach.email.subject}"
        - id: done
          depends: ticket
          action: mail.organize
          with:
            mailbox: support@example.com
            emails: ${foreach.email.id}
            mark: read
```

An app-password account that files invoices and reports by email:

```yaml
secrets:
  - name: BILLING_MAIL_PASSWORD
    ref: mail/billing
mail_accounts:
  billing@example.com:
    imap: {host: imap.example.com, port: 993, security: tls}
    smtp: {host: smtp.example.com, port: 587, security: starttls}
    password: ${BILLING_MAIL_PASSWORD}
steps:
  - id: find
    action: mail.search
    with:
      mailbox: billing@example.com
      subject: invoice
      has_attachments: true
      save_attachments: true

  - id: file
    depends: find
    action: mail.organize
    with:
      mailbox: billing@example.com
      emails: ${steps.find.outputs.messages}
      mark: read
      move: folder
      folder: Invoices

  - id: notify
    depends: file
    action: mail.send
    with:
      mailbox: billing@example.com
      to: team@example.com
      subject: Invoices filed
      message: ${steps.find.outputs.count} invoices filed.
```
