// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasAffordances } from '../affordances';
import { useCanvasActions } from '../context';
import { EdgeLabelRenderer } from '../engine';
import { useViewportWhile } from '../engine/store';
import { LinkButton } from '../LinkButton';
import { toolbarScale } from '../nodes/toolbarPlacement';

/**
 * The round buttons on the middle of an edge: the "+" that inserts a step,
 * the split that adds a step in parallel with the target, the arrow that
 * restores the explicit link of an inferred edge, and the trash that removes
 * the link. They mount on selection, and after a short hover. Every edge that can be
 * changed has the same ones, with the same size and the same hover. The
 * buttons lie in one row centred on the middle of the edge. In a
 * top-to-bottom layout the row crosses the link, so that the link stays short.
 */
export function EdgeButtons({
  id,
  x,
  y,
  shown,
  insertable,
  restorable = false,
  deletable,
  restoreTitle,
  onHover,
}: {
  id: string;
  x: number;
  y: number;
  shown: boolean;
  insertable: boolean;
  restorable?: boolean;
  deletable: boolean;
  /** Tooltip of the restore button: it tells why the link is inferred. */
  restoreTitle?: string;
  onHover(hovered: boolean): void;
}): ReactElement | null {
  const { ts } = useI18n();
  const { onInsertOnEdge } = useCanvasActions();
  const affordances = useCanvasAffordances();
  // The buttons keep a target of 24 px on screen down to 60 % of zoom. Hidden
  // buttons do not follow the zoom: they read it when they appear.
  const buttonScale = Math.max(1, toolbarScale(useViewportWhile(shown).zoom));
  const insertLabel = ts('Insert a step here');
  const parallelLabel = ts('Add a parallel step');
  const restoreLabel = ts('Restore the link');
  const deleteLabel = ts('Remove dependency');
  const showRestore = restorable && affordances !== null;
  if (!insertable && !showRestore && !deletable) {
    return null;
  }
  // They mount only for a selected edge or after a short hover: an edge that
  // the pointer crosses has none.
  if (!shown) {
    return null;
  }
  return (
    <EdgeLabelRenderer>
      <div
        data-testid="ve-canvas-edge-buttons"
        style={{
          transform: `translate(-50%, -50%) translate(${x}px, ${y}px) scale(${buttonScale})`,
        }}
        className={cn('nodrag nopan absolute flex flex-row gap-1')}
        onMouseEnter={() => onHover(true)}
        onMouseLeave={() => onHover(false)}
      >
        {insertable && (
          <LinkButton
            kind="add"
            aria-label={insertLabel}
            title={insertLabel}
            onClick={() => onInsertOnEdge(id)}
          />
        )}
        {insertable && affordances !== null && (
          <LinkButton
            kind="parallel"
            aria-label={parallelLabel}
            title={parallelLabel}
            onClick={() =>
              affordances.openPalette({ mode: 'parallel', edgeId: id })
            }
          />
        )}
        {showRestore && (
          <LinkButton
            kind="restore"
            aria-label={restoreLabel}
            title={restoreTitle ?? restoreLabel}
            onClick={() => affordances.restoreEdge(id)}
          />
        )}
        {deletable && affordances !== null && (
          <LinkButton
            kind="remove"
            aria-label={deleteLabel}
            title={deleteLabel}
            onClick={() => affordances.deleteEdge(id)}
          />
        )}
      </div>
    </EdgeLabelRenderer>
  );
}
