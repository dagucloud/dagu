// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { cn } from '@/lib/utils';
import type { ValidationIssue } from '../contracts';
import { plainIssueMessage } from './issues';

const severityClass: Record<string, string> = {
  error: 'text-destructive',
  warning: 'text-warning',
  notice: 'text-muted-foreground',
};

/**
 * Messages of the issues of one field, shown under the field. Errors have
 * the `alert` role. Renders nothing when the list is empty.
 */
export function FieldIssues({
  issues,
  className,
}: {
  issues?: ValidationIssue[];
  className?: string;
}): ReactElement | null {
  if (!issues || issues.length === 0) {
    return null;
  }
  return (
    <ul
      className={cn('mt-0.5 space-y-0.5 text-xs', className)}
      data-testid="ve-field-issues"
    >
      {issues.map((issue, index) => (
        <li
          key={`${index}-${issue.message}`}
          className={cn(
            'whitespace-normal break-words',
            severityClass[issue.severity]
          )}
        >
          <span role={issue.severity === 'error' ? 'alert' : undefined}>
            {plainIssueMessage(issue.message)}
          </span>
        </li>
      ))}
    </ul>
  );
}
