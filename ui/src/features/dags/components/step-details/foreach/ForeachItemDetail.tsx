// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, NodeStatus, Stream } from '@/api/v1/schema';
import { formatRunDuration } from '@/lib/dagRunTiming';
import { isActiveNodeStatus } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { Maximize2 } from 'lucide-react';
import React, { useState } from 'react';
import { InlineLogViewer } from '../../common/InlineLogViewer';
import NodeStatusChip from '../../common/NodeStatusChip';
import type { ForeachLogTarget } from '../../../hooks/useStepLogQuery';
import { ForeachItemsSection } from './ForeachItemsSection';
import {
  type ForeachStepRef,
  useForeachItem,
} from '../../../hooks/useForeachQueries';

type ForeachBodyStep = components['schemas']['ForeachBodyStep'];

/** How a body step log is opened in the full log viewer. */
export type OpenBodyStepLog = (
  bodyStepName: string,
  foreach: ForeachLogTarget,
  stream: Stream,
  status: NodeStatus
) => void;

type Props = {
  stepRef: ForeachStepRef;
  item: string;
  /** Status the item list reported; drives polling until the item settles. */
  itemStatus: NodeStatus;
  depth: number;
  onOpenLog?: OpenBodyStepLog;
};

/**
 * ForeachItemDetail lists the body steps one foreach item ran, with inline
 * stdout and stderr and a way into the full log viewer.
 */
export function ForeachItemDetail({
  stepRef,
  item,
  itemStatus,
  depth,
  onOpenLog,
}: Props) {
  const live = isActiveNodeStatus(itemStatus);
  const { data, isLoading } = useForeachItem(stepRef, item, live);
  const foreach: ForeachLogTarget = { stepName: stepRef.stepName, item };

  if (!data) {
    return (
      <div className="px-3 py-2 text-xs text-muted-foreground">
        {isLoading ? (
          <I18nText text={'Loading item…'} />
        ) : (
          <I18nText text={'No record for this item.'} />
        )}
      </div>
    );
  }

  return (
    <div className="space-y-1 px-2 py-1.5">
      {data.steps.map((step) => (
        <BodyStepRow
          key={step.name}
          stepRef={stepRef}
          foreach={foreach}
          step={step}
          depth={depth}
          onOpenLog={onOpenLog}
        />
      ))}
    </div>
  );
}

function BodyStepRow({
  stepRef,
  foreach,
  step,
  depth,
  onOpenLog,
}: {
  stepRef: ForeachStepRef;
  foreach: ForeachLogTarget;
  step: ForeachBodyStep;
  depth: number;
  onOpenLog?: OpenBodyStepLog;
}) {
  const { ts } = useI18n();
  const [stream, setStream] = useState<Stream | null>(null);
  const live = isActiveNodeStatus(step.status);
  const duration =
    step.startedAt && formatRunDuration(step.startedAt, step.finishedAt);
  const toggleStream = (next: Stream) =>
    setStream((current) => (current === next ? null : next));

  return (
    <div className="rounded-md border border-border/60 bg-card/50">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-2 py-1.5 text-sm">
        <span className="min-w-0 flex-1 truncate font-medium" title={step.name}>
          {step.name}
        </span>
        <NodeStatusChip status={step.status} size="sm">
          {step.statusLabel}
        </NodeStatusChip>
        {duration && duration !== '-' && (
          <span className="tabular-nums text-xs text-muted-foreground">
            {duration}
          </span>
        )}
        {!!step.retryCount && step.retryCount > 0 && (
          <span className="text-xs text-muted-foreground">
            <I18nText text={'Retries'} />: {step.retryCount}
          </span>
        )}
        <div className="flex items-center gap-1">
          {([Stream.stdout, Stream.stderr] as const).map((candidate) => {
            const available =
              candidate === Stream.stdout ? step.hasStdout : step.hasStderr;
            return (
              <button
                key={candidate}
                type="button"
                disabled={!available}
                aria-pressed={stream === candidate}
                className={cn(
                  'rounded border px-1.5 py-0.5 font-mono text-[11px] transition-colors',
                  stream === candidate
                    ? 'border-primary/40 bg-primary/10 text-primary'
                    : 'border-border bg-background text-muted-foreground hover:bg-muted',
                  !available &&
                    'cursor-not-allowed opacity-40 hover:bg-background'
                )}
                onClick={() => toggleStream(candidate)}
              >
                {candidate}
              </button>
            );
          })}
          {onOpenLog && (step.hasStdout || step.hasStderr) && (
            <I18nProps>
              <button
                type="button"
                className="rounded border border-border bg-background p-1 text-muted-foreground hover:bg-muted"
                title="Open in log viewer"
                aria-label={ts('Open {step} in log viewer', {
                  step: step.name,
                })}
                onClick={() =>
                  onOpenLog(
                    step.name,
                    foreach,
                    stream ?? Stream.stdout,
                    step.status
                  )
                }
              >
                <Maximize2 className="h-3 w-3" />
              </button>
            </I18nProps>
          )}
        </div>
      </div>
      {step.error && (
        <div
          className="truncate border-t border-border/60 px-2 py-1 text-xs text-destructive"
          title={step.error}
        >
          {step.error}
        </div>
      )}
      {stream && (
        <div className="border-t border-border/60 p-1.5">
          <InlineLogViewer
            dagName={stepRef.dagName}
            dagRunId={stepRef.dagRunId}
            dagRun={stepRef.dagRun}
            stepName={step.name}
            stream={stream}
            foreach={foreach}
            live={live}
          />
        </div>
      )}
      {step.foreachParent && step.startedAt && (
        <div className="border-t border-border/60 p-1.5">
          <ForeachItemsSection
            stepRef={stepRef}
            stepStatus={step.status}
            parent={step.foreachParent}
            depth={depth + 1}
            onOpenLog={onOpenLog}
          />
        </div>
      )}
    </div>
  );
}
