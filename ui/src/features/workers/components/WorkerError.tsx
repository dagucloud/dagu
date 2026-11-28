// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cn } from '@/lib/utils';
import type { WorkerErrorAdvice } from '../hooks/useWorkerAdmin';

/**
 * WorkerError shows why a request failed and what to change. A failed action
 * is announced to screen readers and uses the error color; a list that could
 * not be read is a muted state of the page.
 */
export function WorkerError({
  advice,
  action,
  className,
}: {
  advice: WorkerErrorAdvice;
  action: boolean;
  className?: string;
}) {
  return (
    <div
      role={action ? 'alert' : undefined}
      className={cn(
        'text-sm',
        action ? 'text-destructive' : 'text-muted-foreground',
        className
      )}
    >
      <p>{advice.cause}</p>
      <p className={cn(action && 'text-foreground')}>{advice.change}</p>
    </div>
  );
}
