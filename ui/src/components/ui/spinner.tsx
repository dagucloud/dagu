// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Loader2 } from 'lucide-react';

import { cn } from '@/lib/utils';

export interface SpinnerProps {
  className?: string;
  /** Accessible name. Without it the spinner is decorative and hidden. */
  label?: string;
}

/**
 * The one busy indicator. Put it in the slot of the icon that it replaces, so
 * the control keeps its size.
 */
export function Spinner({ className, label }: SpinnerProps) {
  const icon = (
    <Loader2
      className={cn('size-3.5 animate-spin', className)}
      aria-hidden="true"
    />
  );
  if (!label) return icon;
  return (
    <span role="status" aria-label={label} className="inline-flex">
      {icon}
    </span>
  );
}
