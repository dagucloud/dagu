// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type * as React from 'react';
import { WorkerAccessReason } from '@/api/v1/schema';
import { CopyButton } from '@/components/ui/copy-button';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

const NO_COORDINATOR =
  'No coordinator is running or reachable. Start one, such as with dagu start-all, then check again.';

const TRANSPORT_DOCS =
  'https://docs.dagu.sh/server-admin/distributed/transport-security';

/** A request that failed, as the dialog's own words: the cause and the change. */
export interface RequestProblem {
  text: string;
  reason?: string;
}

// The dialog's words for each reason the server gives in details.reason or
// a coordinator's errorReason. A coordinator's own message is never shown:
// it may name where a credential is kept.
const REASON_TEXT: Partial<Record<WorkerAccessReason, string>> = {
  [WorkerAccessReason.own_certificates]:
    'This coordinator uses its own certificates, peer.cert_file and peer.key_file, so it has no built-in CA for invites. Add the worker with your own certificates and no invite, as server TLS describes, and mutual TLS when the coordinator sets peer.client_ca_file. Or move every Dagu process to built-in TLS together.',
  [WorkerAccessReason.ca_unavailable]:
    'Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause.',
  [WorkerAccessReason.peer_token_missing]:
    "This server has no peer token for the coordinator. Set peer.token on this server to the coordinator's, then restart it.",
  [WorkerAccessReason.peer_token_mismatch]:
    "This server's peer.token is not the coordinator's. Set the same peer.token on both, then restart them.",
  [WorkerAccessReason.require_tls]:
    "The coordinator requires TLS. On this server, clear any peer.insecure: true or peer.skip_tls_verify: true, whether a flag, a variable, or the config file sets it. If this server has no usable CA certificate for the coordinator, switch it to built-in TLS: clear peer.cert_file, peer.key_file, and peer.client_ca_file, then set peer.ca_cert_file to the coordinator's CA certificate. Then restart it. If these settings are in order, check that the network path to the coordinator passes TLS.",
  [WorkerAccessReason.coordinator_refused]:
    'The coordinator refused this server. Its log names the reason.',
  [WorkerAccessReason.coordinator_conflict]:
    'The coordinator cannot do this in its present state. Its log names the reason; fix it, then try again.',
  [WorkerAccessReason.retry_later]:
    'The coordinator cannot do this yet. Try again in a moment.',
  [WorkerAccessReason.older_coordinator]:
    'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.',
  [WorkerAccessReason.no_coordinator]: NO_COORDINATOR,
};

// Docs that carry the steps a reason's words leave out.
const REASON_LINKS: Partial<Record<WorkerAccessReason, [string, string][]>> = {
  [WorkerAccessReason.own_certificates]: [
    ['Server TLS', `${TRANSPORT_DOCS}#server-tls`],
    ['Mutual TLS', `${TRANSPORT_DOCS}#mutual-tls`],
    ['Move to built-in TLS', `${TRANSPORT_DOCS}#processes-on-another-host`],
  ],
};

/** The docs links for a reason, if it has any. */
export function ReasonLinks({ reason }: { reason?: string }) {
  const links = reason ? REASON_LINKS[reason as WorkerAccessReason] : undefined;
  return links?.map(([text, href]) => (
    <a
      key={href}
      href={href}
      target="_blank"
      rel="noreferrer"
      className="ml-2 underline underline-offset-2"
    >
      <I18nText text={text} />
    </a>
  ));
}

/** The dialog's words for a reason, or undefined for one it does not know. */
export function reasonText(reason: string | undefined): string | undefined {
  return reason ? REASON_TEXT[reason as WorkerAccessReason] : undefined;
}

/**
 * Names the cause of a failed call by its status and details.reason. A 409
 * or 503 without a reason comes from a server that predates reasons.
 */
export function requestProblem(
  status: number | undefined,
  error: unknown,
  conflict: string
): RequestProblem {
  const reason = (error as { details?: { reason?: string } } | undefined)
    ?.details?.reason;
  // A non-admin gets 403 for any request, so the reason is not the cause.
  if (status === 403) return { text: 'Only admins can add workers.' };
  const text = reasonText(reason);
  if (text) return { text, reason };
  switch (status) {
    case 400:
      return {
        text: 'The server refused the address. Check it and try again.',
      };
    case 409:
      return { text: conflict };
    case 503:
      return { text: NO_COORDINATOR };
    default:
      return { text: 'The request failed.' };
  }
}

export function ProblemText({ problem }: { problem: RequestProblem }) {
  return (
    <p role="alert" className="text-sm text-destructive">
      <I18nText text={problem.text} />
      <ReasonLinks reason={problem.reason} />
    </p>
  );
}

/** One numbered step: a heading row with its state, then its body. */
export function Step({
  number,
  title,
  state,
  live,
  children,
}: {
  number: number;
  title: string;
  state?: string;
  /** Announces each change of the state, as step 1's readiness. */
  live?: boolean;
  children?: React.ReactNode;
}) {
  const { ts } = useI18n();
  return (
    <section
      aria-label={ts(title)}
      className="grid grid-cols-[1.5rem_1fr] gap-x-2"
    >
      <span className="text-sm font-medium tabular-nums text-muted-foreground">
        {number}
      </span>
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <h3 className="text-sm font-medium">
          <I18nText text={title} />
        </h3>
        {/* A live region stays mounted so each change is announced. */}
        {(state || live) && (
          <span
            role={live ? 'status' : undefined}
            className="shrink-0 text-xs text-muted-foreground"
          >
            {state && <I18nText text={state} />}
          </span>
        )}
      </div>
      {children && (
        <div className="col-start-2 mt-2 min-w-0 space-y-3">{children}</div>
      )}
    </section>
  );
}

/**
 * A block of shell or config text with a button that copies exactly it. Long
 * lines wrap on screen only.
 */
export function CommandBlock({
  text,
  label,
  copiedLabel,
  autoFocus,
  className,
}: {
  text: string;
  label: string;
  /** The button's name for a moment after it copied; else "Copied". */
  copiedLabel?: string;
  /** Focuses the copy button when it mounts. */
  autoFocus?: boolean;
  className?: string;
}) {
  const { ts } = useI18n();
  return (
    <div className={cn('relative', className)}>
      <pre className="max-h-72 overflow-auto rounded-md border bg-muted px-3 py-2 pr-10 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">
        {text}
      </pre>
      {/* A new text gets a new button, so "Copied" never names text it
          did not copy. */}
      <CopyButton
        key={text}
        text={text}
        label={ts(label)}
        copiedLabel={copiedLabel && ts(copiedLabel)}
        autoFocus={autoFocus}
        size="sm"
        className="absolute right-1.5 top-1.5"
      />
    </div>
  );
}
