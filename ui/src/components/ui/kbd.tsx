// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ComponentProps, ReactElement } from 'react';
import { cn } from '@/lib/utils';

/**
 * The text of a key, in a small box. Menus and the key help use this one
 * look for every key hint.
 */
export function Kbd({
  className,
  ...props
}: ComponentProps<'kbd'>): ReactElement {
  return (
    <kbd
      {...props}
      className={cn(
        'inline-flex items-center rounded border bg-muted px-1.5 font-sans text-xs leading-4 text-muted-foreground',
        className
      )}
    />
  );
}
