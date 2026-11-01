// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { CanvasNode } from '../../contracts';
import { useCanvasActions } from '../context';
import { Handle, type NodeProps } from '../engine';
import { SOURCE_HANDLE, TARGET_HANDLE, type StepFlowNode } from '../types';
import {
  HANDLE_CLASS,
  NodeCompactBody,
  NodeHeader,
  handlePosition,
} from './NodeParts';

/**
 * The step that the open Add step list would add, drawn as a dashed card
 * where it will go. It shows the choice that the list has under the cursor or
 * the keyboard, or a generic card. It is not a step: it has no menu, no
 * selection and no edit, and the pointer goes through it.
 */
function GhostNodeComponent({ data }: NodeProps<StepFlowNode>) {
  const { ts } = useI18n();
  const { direction, compact = false } = useCanvasActions();
  const look = data.ghost ?? null;
  const label = look ? look.title : ts('New step');
  const node: CanvasNode = {
    key: data.node.key,
    label,
    kind: look?.kind ?? 'other',
    lane: 'main',
    issues: { errors: 0, warnings: 0 },
    ...(look?.action === undefined
      ? {}
      : { action: look.action, subtitle: look.action }),
  };
  return (
    <div
      data-testid="ve-canvas-ghost"
      data-ghost={look?.action ?? 'new'}
      aria-hidden="true"
      title={compact ? label : undefined}
      className={cn(
        'pointer-events-none relative flex rounded-md border-[1.5px] border-dashed border-primary/70 bg-card/60 text-card-foreground',
        compact ? 'h-14 w-14' : 'h-16 w-60'
      )}
    >
      <Handle
        id={TARGET_HANDLE}
        type="target"
        position={handlePosition(direction, 'target')}
        isConnectable={false}
        className={HANDLE_CLASS}
      />
      {compact ? (
        <NodeCompactBody node={node} className="h-full flex-1" />
      ) : (
        <NodeHeader node={node} className="h-full flex-1" />
      )}
      <Handle
        id={SOURCE_HANDLE}
        type="source"
        position={handlePosition(direction, 'source')}
        isConnectable={false}
        className={HANDLE_CLASS}
      />
    </div>
  );
}

export const GhostNode = memo(GhostNodeComponent);
