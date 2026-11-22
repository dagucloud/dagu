// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { FastForward, GitBranch } from 'lucide-react';
import type { ReactElement } from 'react';
import { Badge } from '@/components/ui/badge';
import { useI18n } from '@/i18n/I18nProvider';
import type { CanvasNode, ContinueOnView } from '../../contracts';
import { continueLabel } from '../conditions';

type Translate = ReturnType<typeof useI18n>['ts'];

function continueTitle(ts: Translate, view: ContinueOnView): string {
  return [
    continueLabel(ts, view),
    ts('The run goes on with the steps after this one.'),
    view.markSuccess ? ts('The step then counts as a success.') : '',
  ]
    .filter(Boolean)
    .join('\n');
}

function preconditionTitle(ts: Translate, lines: string[]): string {
  return [ts('Runs only if:'), ...lines.map((line) => `• ${line}`)].join('\n');
}

/**
 * The marks of `continue_on` and of `preconditions` of a step: two small
 * badges with an icon and a tooltip.
 * They show that a step does not follow the plain rule "runs after the steps
 * that it needs, and stops the run when it fails". Nothing for a step that
 * has neither key.
 */
export function ConditionMarks({
  node,
}: {
  node: Pick<CanvasNode, 'continueOn' | 'preconditions'>;
}): ReactElement | null {
  const { ts } = useI18n();
  const { continueOn, preconditions } = node;
  if (!continueOn && !preconditions) return null;
  return (
    <>
      {preconditions && (
        <Badge
          size="sm"
          role="img"
          data-testid="ve-node-preconditions"
          title={preconditionTitle(ts, preconditions)}
          aria-label={preconditionTitle(ts, preconditions)}
          className="shrink-0"
        >
          <GitBranch className="size-3" aria-hidden="true" />
        </Badge>
      )}
      {continueOn && (
        <Badge
          size="sm"
          role="img"
          data-testid="ve-node-continue-on"
          title={continueTitle(ts, continueOn)}
          aria-label={continueTitle(ts, continueOn)}
          className="shrink-0"
        >
          <FastForward className="size-3" aria-hidden="true" />
        </Badge>
      )}
    </>
  );
}
