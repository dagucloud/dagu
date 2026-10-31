// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Archive, History, type LucideIcon } from 'lucide-react';
import { useState, type ReactElement, type ReactNode } from 'react';
import { Badge } from '@/components/ui/badge';
import { RelativeTime } from '@/components/ui/relative-time';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { shortRunId } from '@/lib/shortRunId';
import { cn } from '@/lib/utils';
import type { InputProvenance } from '../run/inputProvenance';
import { useRunAvailability, useRunLinks } from './RunLinks';

type Translate = ReturnType<typeof useI18n>['ts'];

const ICONS: Record<InputProvenance['source'], LucideIcon> = {
  run: History,
  cache: Archive,
};

const SLOT = '\u0001';

/**
 * A sentence with values inside it: the text of `key` with each `{slot}`
 * replaced by the node of `values`, in the order of the translation.
 */
function sentence(
  ts: Translate,
  key: string,
  values: Record<string, ReactNode>
): ReactNode[] {
  const text = ts(
    key,
    Object.fromEntries(
      Object.keys(values).map((k) => [k, `${SLOT}${k}${SLOT}`])
    )
  );
  return text.split(SLOT).map((part, i) =>
    // The parts at odd places are the names of the slots.
    i % 2 === 1 ? <span key={i}>{values[part]}</span> : part
  );
}

function timeOf(p: InputProvenance): string | null {
  return p.capturedAt === null
    ? null
    : (formatAbsoluteTime(p.capturedAt) ?? null);
}

/**
 * The sentence of a mark in plain text: where the outputs of the step come
 * from, with the short id of the run and the time. The whole run id follows
 * in the tooltip.
 */
export function inputMarkTitle(p: InputProvenance, ts: Translate): string {
  const time = timeOf(p);
  const id = shortRunId(p.runId ?? '');
  let text: string;
  if (p.source === 'run') {
    text = time
      ? ts('Outputs reused from run {id}, {time}', { id, time })
      : ts('Outputs reused from run {id}', { id });
  } else if (p.runId) {
    text = time
      ? ts('Cached values from run {id}, {time}', { id, time })
      : ts('Cached values from run {id}', { id });
  } else {
    text = time
      ? ts('From your cache ({time})', { time })
      : ts('From your cache');
  }
  return p.stale ? `${text}. ${ts('The step changed since')}` : text;
}

/** The text of a mark, as nodes: the source and, for a run, a button that selects it. */
function inputMarkText(
  p: InputProvenance,
  ts: Translate,
  onSelectRun?: (runId: string) => void
): ReactNode {
  if (p.source === 'cache' && !p.runId) {
    if (p.capturedAt === null) return ts('cached');
    return sentence(ts, 'cached {time}', {
      time: (
        <RelativeTime
          timestamp={new Date(p.capturedAt).toISOString()}
          compact
          ago
        />
      ),
    });
  }
  const runId = p.runId ?? '';
  const id = (
    <span className="font-mono" title={runId}>
      {shortRunId(runId)}
    </span>
  );
  const link = onSelectRun ? (
    <button
      type="button"
      data-testid="input-mark-run"
      className="rounded-sm underline underline-offset-2 hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      onClick={() => onSelectRun(runId)}
    >
      {id}
    </button>
  ) : (
    id
  );
  if (p.source === 'run') {
    return sentence(ts, p.stale ? 'changed since run {id}' : 'from run {id}', {
      id: link,
    });
  }
  if (p.capturedAt === null) {
    return sentence(ts, 'cached from run {id}', { id: link });
  }
  return sentence(ts, 'cached from run {id}, {time}', {
    id: link,
    time: (
      <RelativeTime
        timestamp={new Date(p.capturedAt).toISOString()}
        compact
        ago
      />
    ),
  });
}

/**
 * What the tooltip says of the source: the sentence, the whole run id, and
 * the run as a button that selects it, or the words "no longer available"
 * when the server does not have the run any more.
 */
function MarkTip({
  provenance,
  title,
  onSelectRun,
  open,
}: {
  provenance: InputProvenance;
  title: string;
  onSelectRun?: (runId: string) => void;
  open: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const links = useRunLinks();
  const availability = useRunAvailability(provenance.runId, open);
  const runId = provenance.runId;
  const select =
    onSelectRun ??
    (availability.state === 'available' && links
      ? () => links.select(availability.runId)
      : undefined);
  return (
    <div className="space-y-1" data-testid="input-mark-tip">
      <div>{title}</div>
      {runId && (
        <div className="flex items-center gap-2">
          <span className="break-all font-mono text-muted-foreground">
            {runId}
          </span>
          {availability.state === 'gone' ? (
            <span data-testid="input-mark-gone">
              {ts('run {id} (no longer available)', { id: shortRunId(runId) })}
            </span>
          ) : (
            select && (
              <button
                type="button"
                data-testid="input-mark-show"
                className="shrink-0 rounded-sm underline underline-offset-2 hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                onClick={() => select(runId)}
              >
                {ts('Show run')}
              </button>
            )
          )}
        </div>
      )}
    </div>
  );
}

/**
 * The mark of a step that a run did not execute but whose outputs it reused:
 * the one component for the INPUT column, the run details and the graph.
 * `label` is a small badge with the source ("from run 034mhevD", "cached
 * from run 034mhevD, 12s ago"); `chip` is a short badge for the card of the
 * graph ("from run 034mhevD", "cache of run 034mhevD"); `icon` is the icon
 * alone, for a compact card that has no room for text; `plain` is the
 * label with no box, for a row of text. A step that changed
 * since the source run shows in the warning tone. The run id of the label
 * selects that run when `onSelectRun` is given. The tooltip of the chip and of
 * the icon always names the source run (id, time), and links to it when the
 * history holds it, or says that it is no longer available.
 */
export function InputMark({
  provenance,
  variant = 'label',
  onSelectRun,
  testId,
  className,
}: {
  provenance: InputProvenance;
  variant?: 'label' | 'chip' | 'icon' | 'plain';
  onSelectRun?: (runId: string) => void;
  testId?: string;
  className?: string;
}): ReactElement {
  const { ts } = useI18n();
  const Icon = ICONS[provenance.source];
  const title = inputMarkTitle(provenance, ts);
  const [tipOpen, setTipOpen] = useState(false);
  const common = {
    'data-testid': testId ?? 'input-mark',
    'data-source': provenance.source,
    ...(provenance.stale ? { 'data-stale': '' } : {}),
  };
  if (variant === 'icon' || variant === 'chip') {
    const chip = variant === 'chip';
    const chipText = provenance.runId
      ? ts(
          provenance.source === 'cache' ? 'cache of run {id}' : 'from run {id}',
          { id: shortRunId(provenance.runId) }
        )
      : ts('cached');
    return (
      <Tooltip onOpenChange={setTipOpen}>
        <TooltipTrigger asChild>
          <span
            {...common}
            tabIndex={0}
            role="img"
            aria-label={
              provenance.runId ? `${title} (${provenance.runId})` : title
            }
            className={cn(
              'inline-flex shrink-0 items-center gap-1 rounded-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
              chip ? 'border border-dashed px-1 text-[10px] leading-4' : null,
              provenance.stale
                ? 'text-warning border-warning/50'
                : 'text-muted-foreground border-muted-foreground/50',
              className
            )}
          >
            <Icon className={chip ? 'size-3' : 'size-3.5'} aria-hidden="true" />
            {chip && (
              <span data-testid="input-mark-chip-text" className="font-mono">
                {chipText}
              </span>
            )}
          </span>
        </TooltipTrigger>
        <TooltipContent side="top" className="max-w-80">
          <MarkTip
            provenance={provenance}
            title={title}
            onSelectRun={onSelectRun}
            open={tipOpen}
          />
        </TooltipContent>
      </Tooltip>
    );
  }
  if (variant === 'plain') {
    // The icon and the text, with no box: it lines up with the text of a row.
    return (
      <span
        {...common}
        title={title}
        className={cn(
          'inline-flex shrink-0 items-center gap-1 text-[11px] leading-4 [&_svg]:size-3 [&_svg]:shrink-0',
          provenance.stale ? 'text-warning' : 'text-muted-foreground',
          className
        )}
      >
        <Icon aria-hidden="true" />
        {inputMarkText(provenance, ts, onSelectRun)}
      </span>
    );
  }
  return (
    <Badge
      {...common}
      size="sm"
      variant={provenance.stale ? 'warning' : 'default'}
      title={title}
      className={className}
    >
      <Icon aria-hidden="true" />
      {inputMarkText(provenance, ts, onSelectRun)}
    </Badge>
  );
}
