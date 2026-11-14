// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { LayoutGrid } from 'lucide-react';
import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

/**
 * The "Simplified view" button of the canvas toolbar. Not pressed (the
 * default), the step cards show their name, id and action. Pressed, they
 * show only their icon, as the small tiles of the icon: a grid of squares.
 */
export function SimplifiedToggle({
  simplified,
  onSimplifiedChange,
}: {
  simplified: boolean;
  onSimplifiedChange(simplified: boolean): void;
}): ReactElement {
  const { ts } = useI18n();
  const label = ts('Simplified view');
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={simplified}
      onClick={() => onSimplifiedChange(!simplified)}
      className={cn(
        'flex h-7 w-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        simplified && 'bg-muted text-foreground'
      )}
    >
      <LayoutGrid className="h-3.5 w-3.5" aria-hidden="true" />
    </button>
  );
}
