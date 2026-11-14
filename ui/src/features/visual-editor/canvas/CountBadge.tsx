// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ComponentPropsWithoutRef, ReactElement } from 'react';
import { cn } from '@/lib/utils';

/**
 * Colours of a count badge. 'error' uses the destructive token, the same
 * red as the trash button of a link.
 */
const TONE_CLASS = {
  primary: 'bg-primary text-primary-foreground',
  error: 'bg-destructive text-destructive-foreground',
  warning: 'bg-warning text-warning-foreground',
} as const;

// leading-none follows the font size: tailwind-merge drops a line height that
// comes before a font size.
const SIZE_CLASS = {
  default: 'h-5 min-w-5 text-[11px] leading-none',
  inline: 'h-4 min-w-4 text-[10px] leading-none',
} as const;

export interface CountBadgeProps
  extends Omit<ComponentPropsWithoutRef<'span'>, 'children'> {
  count: number;
  tone?: keyof typeof TONE_CLASS;
  /** 'inline' is the small badge of a tab or a list row. */
  size?: keyof typeof SIZE_CLASS;
  /**
   * Pins the badge on the top right corner of its parent, which must be
   * positioned, over the border. The count of the Cron launcher and the
   * counts of a step card use this one offset. Without it, the badge sits in
   * the flow of its parent.
   */
  corner?: boolean;
}

/**
 * Small round badge with a number. It is the one badge of the canvas: the
 * count of cron expressions of the Cron launcher, and the error and warning
 * counts of a step all have this size, this font and this shape.
 */
export function CountBadge({
  count,
  tone = 'primary',
  size = 'default',
  corner = false,
  className,
  ...props
}: CountBadgeProps): ReactElement {
  return (
    <span
      {...props}
      className={cn(
        'flex shrink-0 items-center justify-center rounded-full px-1 font-semibold shadow-sm',
        SIZE_CLASS[size],
        TONE_CLASS[tone],
        corner && 'absolute -right-2 -top-2',
        className
      )}
    >
      {count}
    </span>
  );
}
