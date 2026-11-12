// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement, ReactNode } from 'react';
import { cn } from '@/lib/utils';

export interface PanelMessageProps {
  /** 'error' is a load error: destructive token and role alert. */
  tone: 'muted' | 'error';
  align: 'center' | 'start';
  children: ReactNode;
  className?: string;
}

/**
 * A short message in a panel: an empty state, a wait or a load error. The
 * panels of the workspace all use this one text size and these tokens.
 */
export function PanelMessage({
  tone,
  align,
  children,
  className,
}: PanelMessageProps): ReactElement {
  return (
    <p
      role={tone === 'error' ? 'alert' : undefined}
      data-slot="panel-message"
      className={cn(
        'p-3 text-xs',
        tone === 'error' ? 'text-destructive' : 'text-muted-foreground',
        align === 'center' ? 'text-center' : 'text-start',
        className
      )}
    >
      {children}
    </p>
  );
}
