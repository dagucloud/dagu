// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo, type CSSProperties } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasAffordances } from '../affordances';
import { useCanvasActions } from '../context';
import { Handle, Position, type NodeProps } from '../engine';
import { LinkButton } from '../LinkButton';
import { SOURCE_HANDLE, TARGET_HANDLE, type StepFlowNode } from '../types';
import {
  CARD_OUTLINE,
  CompactFrame,
  HANDLE_CLASS,
  NodeCompactBody,
  NodeHeader,
  NodeToolbar,
  cardStateClass,
  compactCardPlace,
  compactTitle,
  failureTitle,
  handlePosition,
  ReusedOutline,
  reusedOutline,
  reuseTitle,
} from './NodeParts';

/**
 * A short line and a "+" button beyond a source handle. `at` is the point of
 * the handle in the node; the stalk goes out to the right or down. The line
 * reaches from the handle to the button, with the colour and the width of a
 * dependency edge (1.5 px of var(--muted-foreground)).
 */
export function AddStalk({
  side,
  at,
  label,
  onAdd,
}: {
  side: Position.Right | Position.Bottom;
  at: CSSProperties;
  label: string;
  onAdd(): void;
}) {
  const right = side === Position.Right;
  return (
    <div
      data-testid="ve-canvas-stalk"
      style={at}
      className={cn(
        'pointer-events-none absolute flex items-center',
        right ? 'flex-row -translate-y-1/2' : 'flex-col -translate-x-1/2'
      )}
    >
      {/* The line reaches the button, 28 px from the handle. */}
      <span
        aria-hidden="true"
        data-testid="ve-canvas-stalk-line"
        className={cn(
          'bg-muted-foreground',
          right ? 'h-[1.5px] w-7' : 'h-7 w-[1.5px]'
        )}
      />
      <LinkButton
        kind="add"
        aria-label={label}
        title={label}
        onClick={(event) => {
          // The click must not select the node or clear the selection.
          event.stopPropagation();
          onAdd();
        }}
      />
    </div>
  );
}

/**
 * Dense card of one step: 240 x 64 px, or a 56 px square with only the icon
 * and the name under it when the canvas is compact. The layout depends on
 * this size.
 */
function StepNodeComponent({
  data,
  selected,
  isConnectable,
}: NodeProps<StepFlowNode>) {
  const { ts } = useI18n();
  const { node } = data;
  const { direction, editable, compact = false } = useCanvasActions();
  const affordances = useCanvasAffordances();
  const connectable = isConnectable && editable;
  const lr = direction === 'LR';

  // A sub-DAG has the two bars of a subroutine, and a fan-out of sub-DAGs is
  // a stack of cards: the shapes of the Graph view.
  const subDag = node.kind === 'call';
  const stacked = subDag && node.parallel === true;

  const targetHandle = (
    <Handle
      id={TARGET_HANDLE}
      type="target"
      position={handlePosition(direction, 'target')}
      isConnectable={connectable}
      className={HANDLE_CLASS}
    />
  );
  const sourceHandle = (
    <Handle
      id={SOURCE_HANDLE}
      type="source"
      position={handlePosition(direction, 'source')}
      isConnectable={connectable}
      className={HANDLE_CLASS}
    />
  );
  const stalk = affordances && data.isSink && (
    <AddStalk
      side={lr ? Position.Right : Position.Bottom}
      at={lr ? { left: '100%', top: '50%' } : { left: '50%', top: '100%' }}
      label={ts('Add a step')}
      onAdd={() => affordances.openPalette({ mode: 'child', source: node.key })}
    />
  );

  const reuseNote = reuseTitle(node, ts);
  const cardTitle =
    [compact ? compactTitle(node) : failureTitle(node.run), reuseNote]
      .filter(Boolean)
      .join('\n') || undefined;

  // A compact card sits in a frame with its caption: the handles and the
  // stalk belong to the frame, so that they stay on the sides of the box.
  const card = (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape={stacked ? 'stack' : subDag ? 'subroutine' : 'card'}
      title={cardTitle}
      role={compact ? 'group' : undefined}
      aria-label={compact ? node.label : undefined}
      style={compact ? compactCardPlace(direction) : undefined}
      className={cn(
        'group flex rounded-md bg-card text-card-foreground',
        CARD_OUTLINE,
        compact ? 'absolute h-14 w-14' : 'relative h-16 w-60',
        cardStateClass(node, selected, data.highlighted),
        reusedOutline(node)
      )}
    >
      {stacked && (
        <>
          <span
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 -z-10 translate-x-2 translate-y-2 rounded-md border bg-card"
          />
          <span
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 -z-10 translate-x-1 translate-y-1 rounded-md border bg-card"
          />
        </>
      )}
      {subDag && (
        <>
          <span
            aria-hidden="true"
            className="pointer-events-none absolute inset-y-0 left-2 w-px bg-border"
          />
          <span
            aria-hidden="true"
            className="pointer-events-none absolute inset-y-0 right-2 w-px bg-border"
          />
        </>
      )}
      {node.reuseOutline && (
        <ReusedOutline tone={node.reuseOutline} selected={selected} />
      )}
      {!compact && targetHandle}
      <NodeToolbar
        step={node.key}
        label={node.label}
        selected={selected}
        runnable
        centred={compact}
      />
      {compact ? (
        <NodeCompactBody node={node} className="h-full flex-1" />
      ) : (
        <NodeHeader
          node={node}
          className={cn('h-full flex-1', subDag && 'px-4')}
        />
      )}
      {!compact && sourceHandle}
      {!compact && stalk}
    </div>
  );
  if (!compact) {
    return card;
  }
  return (
    <CompactFrame direction={direction} label={node.label}>
      {targetHandle}
      {card}
      {sourceHandle}
      {stalk}
    </CompactFrame>
  );
}

export const StepNode = memo(StepNodeComponent);
