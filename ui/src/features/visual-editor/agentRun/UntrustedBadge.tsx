// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ShieldAlert, ShieldCheck } from 'lucide-react';
import type { ReactElement } from 'react';
import { Badge } from '@/components/ui/badge';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { TrustSource } from './agentRun';

/**
 * The tooltip line that tells which key of the step sets its trust. Undefined
 * for any other source: the marks show only what the YAML writes.
 */
export function trustNote(
  ts: ReturnType<typeof useI18n>['ts'],
  source: TrustSource | undefined
): string | undefined {
  switch (source) {
    case 'marked':
      return ts('Untrusted: the key untrusted is true.');
    case 'opted-out':
      return ts('Trusted: the key untrusted is false.');
    default:
      return undefined;
  }
}

/**
 * The mark of an untrusted step: it gets no secret and no DAGU_ variable.
 * Every surface that shows the mark renders this badge. `compact` keeps the
 * icon alone, for a card that has no room for text. `source` adds the reason
 * to the tooltip. The mark is neutral: the trust of a step is a property, not a
 * status, so it takes no amber and no green.
 */
export function UntrustedBadge({
  compact = false,
  source,
  className,
}: {
  compact?: boolean;
  source?: TrustSource | undefined;
  className?: string;
}): ReactElement {
  const { ts } = useI18n();
  const title = [
    ts('Untrusted step: it gets no secret and no DAGU_ variable.'),
    trustNote(ts, source),
  ]
    .filter(Boolean)
    .join('\n');
  return (
    <Badge
      size="sm"
      role="img"
      title={title}
      aria-label={title}
      data-testid="untrusted-badge"
      className={cn('shrink-0', className)}
    >
      <ShieldAlert className="size-3" aria-hidden="true" />
      {compact ? null : ts('Untrusted')}
    </Badge>
  );
}

/**
 * The mark of a step that says `untrusted: false`.
 */
export function TrustedBadge({
  source,
  compact = false,
  className,
}: {
  source: TrustSource | undefined;
  compact?: boolean;
  className?: string;
}): ReactElement | null {
  const { ts } = useI18n();
  const title = trustNote(ts, source);
  if (title === undefined) return null;
  return (
    <Badge
      size="sm"
      role="img"
      title={title}
      aria-label={title}
      data-testid="trusted-badge"
      className={cn('shrink-0', className)}
    >
      <ShieldCheck className="size-3" aria-hidden="true" />
      {compact ? null : ts('Trusted')}
    </Badge>
  );
}
