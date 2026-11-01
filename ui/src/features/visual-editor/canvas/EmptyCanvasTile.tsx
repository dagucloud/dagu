// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus } from 'lucide-react';
import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { useCanvasAffordances } from './affordances';

/**
 * Tile at the center of a canvas with no node. A canvas that can edit offers
 * to add the first step; any other canvas only says that it has no steps.
 */
export function EmptyCanvasTile(): ReactElement {
  const { ts } = useI18n();
  const affordances = useCanvasAffordances();
  return (
    <div
      data-testid="ve-canvas-empty"
      className="pointer-events-none absolute inset-0 flex items-center justify-center"
    >
      {affordances ? (
        <button
          type="button"
          onClick={() => affordances.openPalette({ mode: 'first' })}
          className="pointer-events-auto flex h-28 w-28 flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed bg-card/80 text-sm text-muted-foreground hover:border-primary hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Plus className="h-6 w-6" aria-hidden="true" />
          {ts('Add first step')}
        </button>
      ) : (
        <p className="rounded-md border bg-card/80 px-3 py-2 text-sm text-muted-foreground">
          {ts('No steps')}
        </p>
      )}
    </div>
  );
}
