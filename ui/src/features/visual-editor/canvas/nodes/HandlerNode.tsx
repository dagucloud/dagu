// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasActions } from '../context';
import type { NodeProps } from '../engine';
import type { StepFlowNode } from '../types';
import {
  CompactFrame,
  NodeCompactBody,
  NodeHeader,
  NodeToolbar,
  cardStateClass,
  compactCardPlace,
  compactTitle,
  failureTitle,
} from './NodeParts';

/** Lifecycle handler card: 240 x 64 px, dashed, with no handles. */
function HandlerNodeComponent({ data, selected }: NodeProps<StepFlowNode>) {
  const { node } = data;
  const { ts } = useI18n();
  const { compact = false, direction } = useCanvasActions();

  const card = (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      title={compact ? compactTitle(node) : failureTitle(node.run)}
      role={compact ? 'group' : undefined}
      aria-label={compact ? node.label : undefined}
      style={compact ? compactCardPlace(direction) : undefined}
      className={cn(
        'group flex rounded-md border-[1.5px] border-dashed border-muted-foreground/60 bg-card text-card-foreground dark:border dark:border-muted-foreground/40',
        compact ? 'absolute h-14 w-14' : 'relative h-16 w-60',
        cardStateClass(node, selected, data.highlighted)
      )}
    >
      <span className="absolute -top-2 left-2 rounded border bg-card px-1 text-[11px] font-medium uppercase leading-4 tracking-wide text-muted-foreground">
        {ts('Handler')}
      </span>
      <NodeToolbar
        step={node.key}
        label={node.label}
        selected={selected}
        runnable={false}
        centred={compact}
      />
      {compact ? (
        <NodeCompactBody node={node} className="h-full flex-1" />
      ) : (
        <NodeHeader node={node} className="h-full flex-1" />
      )}
    </div>
  );
  return compact ? (
    <CompactFrame direction={direction} label={node.label}>
      {card}
    </CompactFrame>
  ) : (
    card
  );
}

export const HandlerNode = memo(HandlerNodeComponent);
