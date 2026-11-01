// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useAgentRun } from '../agentRunContext';
import type { NodeProps } from '../engine';
import type { StepFlowNode } from '../types';
import {
  CARD_OUTLINE,
  IssueBadges,
  NodeIcon,
  NodeToolbar,
  RunDot,
  cardStateClass,
} from './NodeParts';

/**
 * Compact card of a tool of an agent: 190 x 52 px. The agent chooses the
 * tool, so the card has no handle. With a run it shows how many times the
 * agent called the tool, and a tool that it never called is dimmed.
 */
function ToolNodeComponent({ data, selected }: NodeProps<StepFlowNode>) {
  const { node } = data;
  const { ts } = useI18n();
  const agentRun = useAgentRun();
  // A tool that the run does not list has zero calls.
  const calls = agentRun
    ? (agentRun.calls[node.serverName ?? node.label] ?? 0)
    : undefined;
  const detail = node.tool?.description ?? node.subtitle;
  const filtering = data.filterMatch !== undefined;
  const dimmed = filtering ? !data.filterMatch : calls === 0;
  const title = [node.label, node.tool?.description].filter(Boolean).join('\n');

  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="tool"
      data-called={calls === undefined ? undefined : calls > 0}
      data-dimmed={dimmed || undefined}
      data-filter-match={filtering ? data.filterMatch : undefined}
      title={title}
      className={cn(
        'group relative flex h-full w-full items-center gap-2 rounded-md bg-card px-2 text-card-foreground transition-opacity',
        CARD_OUTLINE,
        cardStateClass(node, selected, data.highlighted),
        data.filterMatch && !selected && 'ring-2 ring-primary/60',
        dimmed && 'opacity-40'
      )}
    >
      <NodeToolbar
        step={node.key}
        label={node.label}
        selected={selected}
        runnable
        inside
      />
      <NodeIcon node={node} small />
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="min-w-0 flex-1 truncate text-xs font-medium">
            {node.label}
          </span>
          <IssueBadges
            issues={node.issues}
            issueMessages={node.issueMessages}
          />
          <RunDot run={node.run} />
        </div>
        {detail && (
          <span className="truncate text-[11px] text-muted-foreground">
            {detail}
          </span>
        )}
      </div>
      {calls !== undefined && calls > 0 && (
        <span
          data-testid="ve-canvas-tool-calls"
          title={ts('Calls: {count}', { count: calls })}
          aria-label={ts('Calls: {count}', { count: calls })}
          className="absolute -right-1.5 -top-1.5 min-w-4 rounded-full bg-primary px-1 text-center text-[10px] font-semibold leading-4 text-primary-foreground shadow-sm"
        >
          {calls}
        </span>
      )}
    </div>
  );
}

export const ToolNode = memo(ToolNodeComponent);
