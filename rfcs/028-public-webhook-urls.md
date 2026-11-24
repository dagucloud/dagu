# RFC 028: Public URLs for Per-DAG Webhooks

- **Status:** Implemented in v2.20. Documented at
  https://docs.dagu.sh/server-admin/authentication/public-webhook-urls.
- **Date:** 2026-10-14, split out of RFC 004 on 2026-11-24
- **Affected areas:** `auth` (webhooks), `publicwebhook`,
  `service/scheduler`, `service/frontend` (API, UI), `tunnel`, `audit`, and
  `docs`

## Summary

Give each per-DAG webhook an optional public URL through Dagu Cloud. The URL
reaches a server behind a firewall, verifies GitHub, Stripe, Slack, and
Standard Webhooks signatures, takes only the events it lists, and hands large
bodies over as a file. Deliveries reach the server over the connection of
[RFC 002](002-dagu-cloud-connector.md).

## Turning it on

- Off by default. An admin turns it on per webhook, and the audit log records
  it. The server must be connected to Dagu Cloud.
- Turning it on makes a 32-byte token. The webhook store keeps it encrypted,
  like the HMAC secret, so the page can show the URL again. Dagu Cloud gets
  only its SHA-256. **New URL** replaces it; requests Dagu Cloud already
  took through the old URL are still delivered. Turning the URL off drops
  them.
- The hook ID is `webhook/<webhook ID>`, so it stays the same when the DAG
  is renamed: the webhook moves with the DAG, and its URL and anything
  waiting for it survive the rename.
- The local endpoint keeps its token and `X-Dagu-Signature` checks.

## Sender and events

- **Sender:** anyone with the URL (the default), GitHub, Stripe, Slack, or
  Standard Webhooks. The sender's secret is encrypted at rest. Timestamps are
  checked against the time Dagu Cloud received the request, within 5
  minutes, since a delivery can wait days. For GitHub, Dagu makes the secret.
- **Slack** has Dagu Cloud answer its URL check, which starts no run.
- **Events:** an optional list of event names. A name also takes the events
  under it, so `issues` takes `issues.opened`; an empty list takes every
  event. The name is GitHub's `X-GitHub-Event` and `.action` joined by a dot,
  the body's `type` for Stripe and Standard Webhooks, and the wrapped
  event's type for Slack.
- GitHub's `ping`, sent when a webhook is created, never starts a run.

## Connection

- The scheduler that holds the scheduler lock holds the connection, so one
  process per server connects. It connects only while the server is
  connected to Dagu Cloud and some webhook is public, or Dagu Cloud still
  holds URLs since turned off, which the server then drops.
- It asks Dagu Cloud for a five-minute ticket with the license's
  credentials (`POST /api/v1/servers/relay`), which also names the most
  public URLs the plan allows, and connects to `wss://relay.dagu.sh/server`.
  Every connection uses a fresh ticket.
- Reconnects back off from 1 second to 1 minute. A connection closed with
  reason `dropped` means Dagu Cloud disconnected the server; a refused ticket,
  or one that allows none of the public URLs, waits 10 minutes before the
  next try.

## Hook set

- The scheduler sends the whole set on every new connection. It then
  rereads the webhook store every 10 seconds and sends `hooks` when the set
  changes, so turning a URL off takes effect within 10 seconds.
- A new set makes Dagu Cloud delete every waiting delivery whose hook it
  leaves out. So the set is sent only from a complete read: never before the
  first read succeeds, and never from a read that failed or could not decode
  a record. On such a read the last set stays.
- It holds at most as many URLs as the ticket allows, the oldest first; the
  page shows the others as over the plan's limit.

## A delivery

| Situation | Answer | Logged as |
| --- | --- | --- |
| Hook unknown, webhook removed, disabled, or not public | `ignored` | `ignored` |
| Sender chosen and signature missing or invalid | `ignored` | `rejected`, with the reason |
| GitHub's `ping`, or an event the list leaves out | `ignored` | `skipped` |
| Body larger than `webhooks.max_payload_size` | `failed` | `failed` |
| 100 runs already queued on the DAG's queue | `retry` after 60 seconds | `waiting` |
| DAG fails to load or validate | `failed` | `failed`, with the error |
| Storage error while enqueueing | `retry` after 30 seconds | `waiting` |
| Enqueued, or a run with the delivery ID already exists | `started` | `started`, with the run |

- The run ID is the delivery ID, so a delivery Dagu Cloud sends again starts
  one run; a request the sender sends again is a new delivery.
- The run's trigger type is `webhook`, and it uses the DAG's default
  profile.

## What the run receives

- `WEBHOOK_PAYLOAD` is the body as it came, whatever its content type. It is
  not read as the local endpoint's JSON request.
- `WEBHOOK_HEADERS` holds the headers named in `webhook.forward_headers`, as
  for local requests.
- `WEBHOOK_PAYLOAD_FILE` is the path of a file holding the body, written by
  the process that runs the DAG, so it works on workers too.
  `WEBHOOK_PAYLOAD` stays set only while the body fits in one environment
  variable where the run executes (128 KiB on Linux and macOS, 32,767
  characters on Windows). Local requests get the same.

## Deliveries and data

- The scheduler keeps each public webhook's latest 20 deliveries: arrival,
  size, event, how it was logged, and the run or the error. A delivery Dagu
  Cloud sends again replaces its earlier entry. Bodies are not kept there.
- It also keeps the counts Dagu Cloud reports of deliveries that waited longer
  than 7 days or were refused while 1,000 waited, until an admin dismisses
  them.
- Webhook bodies wait at Dagu Cloud for up to 7 days, less when the server
  is disconnected there.

## Disconnecting in Dagu Cloud

- Disconnecting the server, or revoking its activation or license, in Dagu
  Cloud deletes its public URLs and the deliveries waiting for them. They
  are not counted as missed. The URLs answer `404` from then on, so senders
  see failures rather than a `202` that is never delivered.
- The server keeps its webhooks, tokens, sender secrets, and delivery log,
  and its local endpoint keeps working. Its tickets are refused, and once its
  license heartbeat is refused too, it stops connecting.
- Reconnecting the same server to the same workspace brings back the same
  URLs at its next connection, since the relay names the server by workspace
  and server ID. Deleted deliveries do not come back; GitHub needs a manual
  redelivery, and Stripe's own retries reach the server if it returns within
  3 days.
- A suspended license or a lapsed plan deletes nothing.
