// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { cn } from '@/lib/utils';
import {
  Ban,
  CircleCheck,
  ExternalLink,
  CircleSlash,
  MessageCircleOff,
  Pause,
  Play,
  RotateCcw,
} from 'lucide-react';

type AgentEvent = components['schemas']['AgentEvent'];

interface AgentTimelineProps {
  events: AgentEvent[];
  /** Opens the child DAG-run an action produced. */
  onOpenChildRun?: (event: AgentEvent) => void;
}

/** Seconds between two RFC3339 stamps, or null when either is missing. */
function durationSeconds(event: AgentEvent): number | null {
  if (!event.startedAt || !event.finishedAt) return null;
  const ms =
    new Date(event.finishedAt).getTime() - new Date(event.startedAt).getTime();
  return Number.isFinite(ms) && ms >= 0 ? ms / 1000 : null;
}

function icon(event: AgentEvent) {
  const base = 'mt-0.5 h-4 w-4 shrink-0';
  switch (event.kind) {
    case 'task_status':
      if (event.status === 'completed') {
        return <CircleCheck className={cn(base, 'text-success')} />;
      }
      if (event.status === 'failed') {
        return <Ban className={cn(base, 'text-error')} />;
      }
      if (event.status === 'skipped') {
        return <CircleSlash className={cn(base, 'text-muted-foreground')} />;
      }
      return <RotateCcw className={cn(base, 'text-warning')} />;
    case 'rejected':
      return <CircleSlash className={cn(base, 'text-error')} />;
    case 'stalled':
      return <MessageCircleOff className={cn(base, 'text-muted-foreground')} />;
    default:
      return event.status === 'waiting' ? (
        <Pause className={cn(base, 'text-warning')} />
      ) : (
        <Play
          className={cn(
            base,
            event.status === 'failed' ? 'text-error' : 'text-muted-foreground'
          )}
        />
      );
  }
}

/** Short label describing what happened, shown to the right of the name. */
function outcome(event: AgentEvent): string {
  switch (event.kind) {
    case 'task_status':
      return event.status === 'open' ? 'task reopened' : `task ${event.status}`;
    case 'rejected':
      return 'rejected';
    case 'stalled':
      return 'no action chosen';
    default:
      return event.status ?? '';
  }
}

/**
 * Decision timeline of an agent DAG-run. An agent has no dependency
 * edges, so execution order lives here rather than in the graph.
 */
export function AgentTimeline({
  events,
  onOpenChildRun,
}: AgentTimelineProps) {
  return (
    <div className="divide-border bg-card divide-y rounded border">
      {events.map((event, index) => {
        const seconds = durationSeconds(event);
        const linkable = !!event.childDagRunId && !!onOpenChildRun;
        return (
          <div
            key={`${event.turn}-${index}`}
            role={linkable ? 'button' : undefined}
            tabIndex={linkable ? 0 : undefined}
            onClick={linkable ? () => onOpenChildRun(event) : undefined}
            onKeyDown={
              linkable
                ? (e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      onOpenChildRun(event);
                    }
                  }
                : undefined
            }
            className={cn(
              'flex items-start gap-2 px-3 py-1.5 text-sm',
              linkable && 'hover:bg-muted/50 cursor-pointer transition-colors'
            )}
          >
            <span className="text-muted-foreground w-6 shrink-0 text-right text-xs tabular-nums">
              {event.turn}
            </span>
            {icon(event)}
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="text-foreground font-medium break-all">
                  {event.name}
                </span>
                <span className="text-muted-foreground text-xs">
                  {outcome(event)}
                </span>
                {event.attempt && event.attempt > 1 ? (
                  <span className="text-muted-foreground bg-muted rounded px-1 text-xs">
                    #{event.attempt}
                  </span>
                ) : null}
                {seconds !== null ? (
                  <span className="text-muted-foreground/70 text-xs tabular-nums">
                    {seconds.toFixed(1)}s
                  </span>
                ) : null}
                {linkable ? (
                  <span className="text-muted-foreground/70 inline-flex items-center gap-0.5 text-xs">
                    {event.childDagName}
                    <ExternalLink className="h-3 w-3" />
                  </span>
                ) : null}
              </div>
              {event.reason ? (
                <div className="text-muted-foreground/80 text-xs break-words whitespace-normal italic">
                  {event.reason}
                </div>
              ) : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}
