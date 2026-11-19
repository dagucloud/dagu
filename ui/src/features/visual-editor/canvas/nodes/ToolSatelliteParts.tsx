// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Lock } from 'lucide-react';
import type { ReactElement } from 'react';
import { Badge } from '@/components/ui/badge';
import { useI18n } from '@/i18n/I18nProvider';
import type { ToolSelection } from '../../contracts';
import type { ToolParamModel } from '../../tools/model';
import type { ToolLimits } from '../chatModel';
import { CountBadge } from '../CountBadge';
import type { ToolRunInfo } from '../toolRun';

/** True when the selection `a` names the same tool entry as `b`. */
export function sameTool(
  a: ToolSelection | null | undefined,
  b: ToolSelection
): boolean {
  return (
    !!a && a.step === b.step && a.category === b.category && a.index === b.index
  );
}

function valueText(value: unknown): string {
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  return text.length > 40 ? `${text.slice(0, 39)}…` : text;
}

/** The fixed inputs of an entry as `name: value` lines. */
export function fixedLines(params: ToolParamModel[]): string[] {
  return params
    .filter((param) => param.state === 'fixed')
    .map((param) => `${param.name}: ${valueText(param.value)}`);
}

/** A small lock for an entry with fixed inputs; its tooltip lists them. */
export function FixedLock({
  params,
}: {
  params: ToolParamModel[];
}): ReactElement | null {
  const { ts } = useI18n();
  const lines = fixedLines(params);
  if (lines.length === 0) return null;
  const title = [ts('Fixed inputs, hidden from the model'), ...lines].join(
    '\n'
  );
  return (
    <span
      role="img"
      aria-label={title}
      title={title}
      data-testid="ve-canvas-tool-fixed"
      className="inline-flex shrink-0 text-muted-foreground"
    >
      <Lock className="h-3 w-3" aria-hidden="true" />
    </span>
  );
}

/** The count of the calls of a tool in the shown run, as the agent tool cards show it. */
export function CallsBadge({
  count,
}: {
  count: number | undefined;
}): ReactElement | null {
  const { ts } = useI18n();
  if (count === undefined || count <= 0) return null;
  const title = ts('Calls: {count}', { count });
  return (
    <CountBadge
      role="img"
      data-testid="ve-canvas-tool-calls"
      aria-label={title}
      title={title}
      count={count}
      size="inline"
    />
  );
}

/** Calls of the tools `names` in the step: undefined without a run. */
export function callsOf(
  run: ToolRunInfo | null,
  step: string | undefined,
  names: readonly string[]
): number | undefined {
  if (!run?.runId || step === undefined) return undefined;
  const own = run.calls[step] ?? {};
  return names.reduce((total, name) => total + (own[name] ?? 0), 0);
}

/** Calls of every tool of the step in the shown run: undefined without a run. */
export function totalCallsOf(
  run: ToolRunInfo | null,
  step: string | undefined
): number | undefined {
  if (!run?.runId || step === undefined) return undefined;
  return Object.values(run.calls[step] ?? {}).reduce(
    (total, count) => total + count,
    0
  );
}

// A chip that is longer than the card (a `${...}` reference) ends with an
// ellipsis. The turns keep their text up to a share of the row; the calls take the rest.
const TURNS_CHIP_CLASS = 'min-w-0 max-w-[55%]';
const CALLS_CHIP_CLASS = 'min-w-0 shrink';

/**
 * The limits of the tool loop in a row under the header of the Tools card:
 * the turns, and the calls. A shown run gives the calls made, as "7 of 100".
 * The calls chip turns to the warning tone when the run made as many calls as
 * the limit allows.
 */
export function ToolLimitsRow({
  limits,
  used,
  height,
}: {
  limits: ToolLimits;
  /** Calls made in the shown run. */
  used?: number | undefined;
  height: number;
}): ReactElement {
  const { ts } = useI18n();
  const { turns, calls } = limits;
  const callsText =
    calls === undefined
      ? undefined
      : used === undefined
        ? ts(calls === 1 ? 'max {count} call' : 'max {count} calls', {
            count: calls,
          })
        : ts('{used} of {count} calls', { used, count: calls });
  const full = typeof calls === 'number' && used !== undefined && used >= calls;
  return (
    <div
      data-testid="ve-canvas-slot-limits"
      style={{ height }}
      className="flex shrink-0 items-center gap-1 overflow-hidden px-2.5"
    >
      {turns !== undefined && (
        <Badge
          size="sm"
          data-testid="ve-canvas-limit-turns"
          title={ts('The most model turns of one attempt.')}
          className={TURNS_CHIP_CLASS}
        >
          <span className="min-w-0 truncate">
            {ts(turns === 1 ? 'max {count} turn' : 'max {count} turns', {
              count: turns,
            })}
          </span>
        </Badge>
      )}
      {callsText !== undefined && (
        <Badge
          size="sm"
          variant={full ? 'warning' : 'default'}
          data-testid="ve-canvas-limit-calls"
          title={
            limits.callsDefault
              ? ts(
                  'The most calls of DAG tools in one attempt. This is the default.'
                )
              : ts('The most calls of DAG tools in one attempt.')
          }
          className={CALLS_CHIP_CLASS}
        >
          <span className="min-w-0 truncate">{callsText}</span>
        </Badge>
      )}
    </div>
  );
}
