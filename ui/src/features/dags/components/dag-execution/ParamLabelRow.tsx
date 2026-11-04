// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';

/**
 * Lays out a parameter label and, when the label differs from the name,
 * the name beside it, so the identifier used in commands and references
 * stays visible. A long name moves to its own line instead of squeezing
 * the label.
 */
export function ParamLabelRow({
  name,
  label,
  children,
}: {
  name?: string;
  label?: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
      {children}
      {name && name !== label && (
        <code className="min-w-0 break-words font-mono text-xs text-muted-foreground">
          {name}
        </code>
      )}
    </div>
  );
}
