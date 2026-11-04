// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';

import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';

/**
 * Labels a parameter control. When the label differs from the name, the
 * name is shown beside it and joins the control's accessible name, so the
 * identifier used in commands and references stays visible and parameters
 * that share a title stay distinguishable. A long name moves to its own
 * line instead of squeezing the label.
 */
export function ParamLabel({
  id,
  htmlFor,
  name,
  label,
  className,
  children,
}: {
  id?: string;
  htmlFor: string;
  name?: string;
  label: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <Label
      id={id}
      htmlFor={htmlFor}
      className={cn(
        'min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1',
        className
      )}
    >
      <span className="min-w-0 whitespace-normal break-words">{children}</span>
      {name && name !== label && (
        <>
          {' '}
          {/* Radix Label cancels double-click mousedown, which would block
              selecting the name for copying. */}
          <code
            onMouseDown={(event) => event.stopPropagation()}
            className="min-w-0 whitespace-normal break-words font-mono text-xs font-normal text-muted-foreground select-text"
          >
            {name}
          </code>
        </>
      )}
    </Label>
  );
}
