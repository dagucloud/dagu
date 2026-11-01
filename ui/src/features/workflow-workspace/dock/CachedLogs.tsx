// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { Stream } from '@/api/v1/schema';
import {
  lineAt,
  useLogOrder,
} from '@/features/dags/components/dag-execution/LogOrderToggle';
import { useStepLogQuery } from '@/features/dags/hooks/useStepLogQuery';
import {
  runInputProvenance,
  type InputProvenance,
} from '@/features/visual-editor/run/inputProvenance';
import { useI18n } from '@/i18n/I18nProvider';
import { shortRunId } from '@/lib/shortRunId';
import { stripAnsi } from '@/lib/ansi';
import type { ApiDAGRunDetails } from '../contracts';

/** Lines read from the end of each cached log. */
const CACHED_TAIL_LINES = 500;

/** How the log of the run shows its lines: the activity rows or the raw text. */
export type CachedLogMode = 'activity' | 'raw';

/** One reused step with the source run that its log is read from. */
export interface CachedStep {
  step: string;
  runId: string;
  /** The start of the step in its source run, in ms; null when unknown. */
  startedAt: number | null;
}

/**
 * The steps that the run reused and that name a source run, in the order that
 * they ran: by their start in the source run, the steps with no known start
 * after them, in the order of the nodes.
 */
export function cachedSteps(details: ApiDAGRunDetails): CachedStep[] {
  const reused = runInputProvenance(details);
  const found: CachedStep[] = [];
  for (const node of details.nodes ?? []) {
    const record: InputProvenance | undefined = reused.get(node.step.name);
    if (!record?.runId) continue;
    found.push({
      step: node.step.name,
      runId: record.runId,
      startedAt: record.startedAt ?? null,
    });
  }
  return found
    .map((entry, at) => ({ entry, at }))
    .sort(
      (a, b) =>
        (a.entry.startedAt ?? Infinity) - (b.entry.startedAt ?? Infinity) ||
        a.at - b.at
    )
    .map(({ entry }) => entry);
}

function linesOf(content: string | undefined): string[] {
  if (!content) return [];
  const lines = content.split('\n');
  return lines[lines.length - 1] === '' ? lines.slice(0, -1) : lines;
}

const ACTIVITY_GRID =
  'grid grid-cols-[5.5rem_3rem_minmax(0,1fr)] items-start gap-x-3 border-b border-border px-3 py-1 text-xs leading-4 sm:grid-cols-[5.5rem_3rem_minmax(8rem,12rem)_minmax(0,1fr)]';

/**
 * One line of a cached step. It sits among the lines of the run, in the
 * shape of the mode of the log, and says "cached" where a line of the run
 * has its time. The title names the source run.
 */
function CachedLine({
  mode,
  entry,
  text,
  stderr,
}: {
  mode: CachedLogMode;
  entry: CachedStep;
  text: string;
  stderr: boolean;
}) {
  const { ts } = useI18n();
  const title = ts('Cached from run {id}', { id: shortRunId(entry.runId) });
  const line = stripAnsi(text) || ' ';
  if (mode === 'raw') {
    return (
      <div
        data-testid="cached-log-line"
        data-step={entry.step}
        className="flex py-0.5 pr-2 font-mono text-sm"
      >
        <span
          title={title}
          className="mr-4 w-14 flex-shrink-0 select-none self-start pl-4 pr-2 text-right text-muted-foreground"
        >
          {ts('cached')}
        </span>
        <span className="flex-grow select-text whitespace-pre-wrap break-words text-muted-foreground">
          {line}
        </span>
      </div>
    );
  }
  return (
    <div
      data-testid="cached-log-line"
      data-step={entry.step}
      className={ACTIVITY_GRID}
    >
      <span
        title={title}
        className="font-mono text-xs leading-4 text-muted-foreground"
      >
        {ts('cached')}
      </span>
      <span
        className={
          stderr
            ? 'rounded bg-error/10 px-1.5 text-center text-[10px] font-semibold leading-4 text-error'
            : undefined
        }
      >
        {/* i18n-ignore: a stream tag, written as in English like stderr */}
        {stderr ? 'ERR' : null}
      </span>
      <span
        className="min-w-0 truncate font-mono text-xs font-semibold leading-4 text-foreground"
        title={entry.step}
      >
        {entry.step}
      </span>
      <div className="col-span-3 min-w-0 whitespace-pre-wrap break-words font-mono text-xs leading-4 text-muted-foreground sm:col-span-1">
        {line}
      </div>
    </div>
  );
}

/** A one-line note of a cached step: loading, gone, or cut. */
function CachedNote({
  children,
  testId,
}: {
  children: string;
  testId?: string;
}) {
  return (
    <p
      role="status"
      data-testid={testId}
      className="border-b border-border px-3 py-1 text-xs text-muted-foreground"
    >
      {children}
    </p>
  );
}

/**
 * The lines of a cached step, read from the step log of its source run, as
 * lines of the log of the run.
 */
function CachedStepLines({
  dagName,
  entry,
  mode,
}: {
  dagName: string;
  entry: CachedStep;
  mode: CachedLogMode;
}) {
  const { ts } = useI18n();
  const { newestFirst } = useLogOrder();
  const options = { refreshInterval: 0, revalidateOnFocus: false };
  const out = useStepLogQuery(
    { dagName, dagRunId: entry.runId, stepName: entry.step },
    { stream: Stream.stdout, tail: CACHED_TAIL_LINES },
    options
  );
  const err = useStepLogQuery(
    { dagName, dagRunId: entry.runId, stepName: entry.step },
    { stream: Stream.stderr, tail: CACHED_TAIL_LINES },
    options
  );
  const outLines = useMemo(
    () => linesOf(out.data?.content),
    [out.data?.content]
  );
  const errLines = useMemo(
    () => linesOf(err.data?.content),
    [err.data?.content]
  );

  if (out.isLoading || err.isLoading) {
    return <CachedNote>{ts('Loading...')}</CachedNote>;
  }
  // A source run that the server dropped (a test run expires) has no log.
  if (out.error && err.error) {
    return (
      <CachedNote testId="cached-log-gone">
        {ts('Log of run {id} is no longer available', {
          id: shortRunId(entry.runId),
        })}
      </CachedNote>
    );
  }
  const rows = [
    ...outLines.map((text) => ({ text, stderr: false })),
    ...errLines.map((text) => ({ text, stderr: true })),
  ];
  const total = out.data?.totalLines ?? 0;
  const cut = total > outLines.length ? total : null;
  return (
    <>
      {cut !== null && !newestFirst && (
        <CachedNote>
          {ts('Showing {visible} of {total} lines', {
            visible: outLines.length,
            total: cut,
          })}
        </CachedNote>
      )}
      {rows.map((_, row) => {
        const item = rows[lineAt(row, rows.length, newestFirst)]!;
        return (
          <CachedLine
            key={row}
            mode={mode}
            entry={entry}
            text={item.text}
            stderr={item.stderr}
          />
        );
      })}
      {cut !== null && newestFirst && (
        <CachedNote>
          {ts('Showing {visible} of {total} lines', {
            visible: outLines.length,
            total: cut,
          })}
        </CachedNote>
      )}
    </>
  );
}

/**
 * The logs of the steps that the run reused, as lines of the log of the run.
 * They ran before the run: the host puts these lines before the lines of the
 * run, or after them when the newest line is at the top. Each line says
 * "cached" where a line of the run has its time, and names its step.
 */
export function CachedLogs({
  details,
  mode = 'activity',
}: {
  details: ApiDAGRunDetails;
  mode?: CachedLogMode;
}) {
  const { newestFirst } = useLogOrder();
  const steps = useMemo(() => cachedSteps(details), [details]);
  if (steps.length === 0) return null;
  const ordered = newestFirst ? [...steps].reverse() : steps;
  return (
    <div
      data-testid="cached-logs"
      data-order={newestFirst ? 'newToOld' : 'oldToNew'}
      className="shrink-0"
    >
      {ordered.map((entry) => (
        <CachedStepLines
          key={entry.step}
          dagName={details.name}
          entry={entry}
          mode={mode}
        />
      ))}
    </div>
  );
}
