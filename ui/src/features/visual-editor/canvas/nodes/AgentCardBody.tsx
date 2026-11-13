// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Braces, Clock, KeyRound, RotateCcw } from 'lucide-react';
import type { ReactElement } from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { useI18n } from '@/i18n/I18nProvider';
import { nodeStatusText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import { UntrustedBadge } from '../../agentRun/UntrustedBadge';
import {
  AGENT_CARD_PAD,
  AGENT_CARD_PARTS,
  agentChips,
  retryTitle,
  type ChipKind,
} from '../agentCard';
import { useToolRun } from '../toolRun';
import type { CanvasNodeWithRoutes } from '../types';
import { ConditionMarks } from './ConditionMarks';
import {
  IssueBadges,
  NodeIcon,
  ReusedMarker,
  RepeatMarker,
  RunDot,
  RunMeta,
} from './NodeParts';
import { totalCallsOf } from './ToolSatelliteParts';

/**
 * The card of an `agent.run` step has room for what a plain step card
 * truncates. Top to bottom: the icon, with two lines beside it (the name, and
 * under it the subtitle with the provider) and the status at the right; the chips of
 * the keys that decide how the step ends (trust, retry, timeout, secrets,
 * output schema, repeat, preconditions, continue_on), as many rows as they
 * need; and, while a run is shown, a row with its result: the reason of a
 * failure, the tool calls, the retries and the duration. The layout reserves
 * the height (see `agentCardHeight`).
 */
export function AgentCardBody({
  node,
}: {
  node: CanvasNodeWithRoutes;
}): ReactElement {
  const { ts } = useI18n();
  const agent = node.agent;
  const run = node.run;
  const retried = run?.retryCount ?? 0;
  const chips = agentChips(ts, node, retried);
  const kinds = new Set<ChipKind>(chips.map((chip) => chip.kind));
  const toolRun = useToolRun();
  const calls = totalCallsOf(toolRun, node.serverName);
  const failure =
    run?.status === NodeStatus.Failed
      ? run.error?.trim() || undefined
      : undefined;
  const { HEAD, CHIP_ROW, RUN_ROW, GAP, PAD_Y } = AGENT_CARD_PARTS;
  return (
    <div
      data-testid="ve-agent-card"
      style={{ padding: `${PAD_Y}px ${AGENT_CARD_PAD}px` }}
      className="flex h-full min-w-0 flex-1 flex-col"
    >
      <IssueBadges
        corner
        issues={node.issues}
        issueMessages={node.issueMessages}
      />
      <div style={{ height: HEAD }} className="flex items-center gap-3">
        <NodeIcon node={node} large />
        <div className="flex min-w-0 flex-1 flex-col justify-center">
          <span
            data-testid="ve-agent-name"
            title={node.label}
            className="line-clamp-1 min-w-0 break-words text-base font-semibold leading-5"
          >
            {node.label}
          </span>
          <div
            data-testid="ve-agent-subline"
            className="flex min-w-0 items-center gap-1.5"
          >
            <ReusedMarker node={node} chip />
            {node.subtitle && (
              <span
                data-testid="ve-agent-subtitle"
                title={node.subtitle}
                className="min-w-0 truncate font-mono text-xs leading-[18px] text-muted-foreground"
              >
                {node.subtitle}
              </span>
            )}
          </div>
        </div>
        <RunDot run={run} reuseOutline={node.reuseOutline} />
      </div>
      {chips.length > 0 && (
        <div
          data-testid="ve-agent-chips"
          style={{
            marginTop: GAP,
            minHeight: (agent?.chipRows ?? 1) * CHIP_ROW,
          }}
          className="flex flex-wrap content-start items-start gap-x-1 gap-y-1"
        >
          {kinds.has('untrusted') && (
            <UntrustedBadge source={node.trust} className="max-w-full" />
          )}
          {agent?.retry && (
            <Badge
              size="sm"
              variant={retried > 0 ? 'warning' : 'default'}
              title={retryTitle(ts, agent.retry)}
              data-testid="ve-agent-retry"
              className="max-w-full"
            >
              <RotateCcw className="size-3" aria-hidden="true" />
              {chips.find((chip) => chip.kind === 'retry')?.label}
            </Badge>
          )}
          {agent?.timeoutSec !== undefined && (
            <Badge
              size="sm"
              title={ts('Dagu stops the step at this limit.')}
              data-testid="ve-agent-timeout"
              className="max-w-full"
            >
              <Clock className="size-3" aria-hidden="true" />
              {chips.find((chip) => chip.kind === 'timeout')?.label}
            </Badge>
          )}
          {agent?.secrets && (
            <Badge
              size="sm"
              title={[ts('Secrets'), ...agent.secrets].join('\n')}
              data-testid="ve-agent-secrets"
              className="max-w-full"
            >
              <KeyRound className="size-3" aria-hidden="true" />
              {chips.find((chip) => chip.kind === 'secrets')?.label}
            </Badge>
          )}
          {agent?.schema && (
            <Badge
              size="sm"
              title={
                agent.schema.length > 0
                  ? [ts('Output schema'), ...agent.schema].join('\n')
                  : ts('Output schema')
              }
              data-testid="ve-agent-schema"
              className="max-w-full"
            >
              <Braces className="size-3" aria-hidden="true" />
              {chips.find((chip) => chip.kind === 'schema')?.label}
            </Badge>
          )}
          <RepeatMarker node={node} />
          <ConditionMarks node={node} chips className="max-w-full" />
        </div>
      )}
      {run && (
        <div
          data-testid="ve-agent-run-row"
          style={{ height: RUN_ROW, marginTop: GAP }}
          className="mt-auto flex min-w-0 items-center gap-1.5 text-[11px]"
        >
          <span
            data-testid="ve-agent-result"
            title={failure ?? undefined}
            className={cn(
              'min-w-0 flex-1 truncate',
              failure ? 'text-destructive' : 'text-muted-foreground'
            )}
          >
            {failure ?? ts(nodeStatusText(run.status))}
          </span>
          {calls !== undefined && calls > 0 && (
            <Badge
              size="sm"
              title={ts('Calls: {count}', { count: calls })}
              data-testid="ve-agent-calls"
            >
              {ts(calls === 1 ? '{count} tool call' : '{count} tool calls', {
                count: calls,
              })}
            </Badge>
          )}
          <RunMeta run={run} />
        </div>
      )}
    </div>
  );
}
