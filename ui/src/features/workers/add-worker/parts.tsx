// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type * as React from 'react';
import { CopyButton } from '@/components/ui/copy-button';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

/** A request that failed: what went wrong, and the server's own words. */
export interface RequestProblem {
  text: string;
  detail?: string;
}

// The statuses the invite and join readiness endpoints answer with.
const STATUS_TEXT: Record<number, string> = {
  403: 'Only admins can add workers.',
  404: 'This server is from an earlier Dagu release. Upgrade it to add workers.',
  503: 'The server cannot reach a coordinator.',
};

/**
 * Names the cause of a failed call. A 400 and a 409 carry the cause in the
 * server's message, so the message stands on its own.
 */
export function requestProblem(
  status: number | undefined,
  message: string | undefined,
  conflict: string
): RequestProblem {
  if (status === 400 && message) return { text: message };
  if (status === 409) return { text: conflict, detail: message };
  const text = (status && STATUS_TEXT[status]) || 'The request failed.';
  return { text, detail: status === 403 ? undefined : message };
}

export function ProblemText({ problem }: { problem: RequestProblem }) {
  return (
    <div role="alert" className="space-y-0.5 text-sm text-destructive">
      <p>
        <I18nText text={problem.text} />
      </p>
      {problem.detail && (
        <p className="text-muted-foreground">{problem.detail}</p>
      )}
    </div>
  );
}

/** One numbered step: a heading row with its state, then its body. */
export function Step({
  number,
  title,
  state,
  children,
}: {
  number: number;
  title: string;
  state?: string;
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
        {state && (
          <span className="shrink-0 text-xs text-muted-foreground">
            <I18nText text={state} />
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
  className,
}: {
  text: string;
  label: string;
  className?: string;
}) {
  const { ts } = useI18n();
  return (
    <div className={cn('relative', className)}>
      <pre className="max-h-72 overflow-auto rounded-md border bg-muted px-3 py-2 pr-10 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">
        {text}
      </pre>
      <CopyButton
        text={text}
        label={ts(label)}
        size="sm"
        className="absolute right-1.5 top-1.5"
      />
    </div>
  );
}
