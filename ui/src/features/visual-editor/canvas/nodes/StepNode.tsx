// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo, type CSSProperties } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasAffordances } from '../affordances';
import { useCanvasActions } from '../context';
import { Handle, Position, type NodeProps } from '../engine';
import { cardSize, chatPlan } from '../layout';
import { LinkButton } from '../LinkButton';
import { agentFactLines } from '../agentCard';
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
import { AgentCardBody } from './AgentCardBody';
import { LoopArcs } from './LoopArcs';
import { SubRowFrame, chatAnchors, chatStalk } from './SubRowFrame';

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
 * and the name under it when the canvas is compact. The card of a chat step
 * is larger (see `CHAT_NODE_WIDTH`), with a bigger tile and title. The layout
 * depends on these sizes.
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
  // A chat step has the larger card: it carries the connectors of its slots.
  const chat = !!node.chat;
  const size = cardSize(node, compact);
  // A chat step has a row of sub-nodes under it: its box is taller than its
  // card, and the handles and the stalk are on the box.
  const plan = node.subRow ? chatPlan(node, compact, direction) : null;
  const anchors = plan ? chatAnchors(plan, direction, compact) : null;

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
      {...(anchors ? { style: anchors.target } : {})}
    />
  );
  // The exit of a chat step that has no step after it is not drawn: its "+"
  // stalk is drawn in its place.
  const loose = !!plan && !!data.isSink;
  const sourceHandle = (
    <Handle
      id={SOURCE_HANDLE}
      type="source"
      position={handlePosition(direction, 'source')}
      isConnectable={connectable}
      className={cn(HANDLE_CLASS, loose && '!pointer-events-none !opacity-0')}
      {...(anchors ? { style: anchors.source } : {})}
    />
  );
  const planStalk = plan ? chatStalk(plan, compact, direction) : null;
  const stalk = affordances && data.isSink && (
    <AddStalk
      side={planStalk ? planStalk.side : lr ? Position.Right : Position.Bottom}
      at={
        planStalk
          ? planStalk.at
          : lr
            ? { left: '100%', top: '50%' }
            : { left: '50%', top: '100%' }
      }
      label={ts('Add a step')}
      onAdd={() => affordances.openPalette({ mode: 'child', source: node.key })}
    />
  );

  const reuseNote = reuseTitle(node, ts);
  const cardTitle =
    [
      compact ? compactTitle(node, ts) : failureTitle(node.run),
      ...agentFactLines(ts, node, !compact),
      reuseNote,
    ]
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
      style={{
        ...(compact ? compactCardPlace(direction, chat) : undefined),
        ...(chat ? { width: size.width, height: size.height } : undefined),
      }}
      className={cn(
        'group flex rounded-md bg-card text-card-foreground',
        CARD_OUTLINE,
        compact
          ? cn('absolute', !chat && 'h-14 w-14')
          : cn('relative', !chat && 'h-16 w-60'),
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
      {!compact && !plan && targetHandle}
      <NodeToolbar
        step={node.key}
        label={node.label}
        selected={selected}
        runnable
        centred={compact}
      />
      {compact ? (
        <NodeCompactBody node={node} className="h-full flex-1" large={chat} />
      ) : node.agent ? (
        <AgentCardBody node={node} />
      ) : (
        <NodeHeader
          node={node}
          large={chat}
          className={cn('h-full flex-1', subDag && 'px-4')}
        />
      )}
      {!compact && !plan && sourceHandle}
      {!compact && !plan && stalk}
      {/* The arcs sit where the step starts: on the upper edge in a vertical graph, on the left side in a horizontal one. */}
      <LoopArcs node={node} placement={lr ? 'side' : 'top'} compact={compact} />
    </div>
  );
  if (plan) {
    return (
      <SubRowFrame
        plan={plan}
        anchors={
          <>
            {targetHandle}
            {sourceHandle}
            {stalk}
          </>
        }
      >
        {compact ? (
          <CompactFrame direction={direction} label={node.label} chat={chat}>
            {card}
          </CompactFrame>
        ) : (
          card
        )}
      </SubRowFrame>
    );
  }
  if (!compact) {
    return card;
  }
  return (
    <CompactFrame direction={direction} label={node.label} chat={chat}>
      {targetHandle}
      {card}
      {sourceHandle}
      {stalk}
    </CompactFrame>
  );
}

export const StepNode = memo(StepNodeComponent);
