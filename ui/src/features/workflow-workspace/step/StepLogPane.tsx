// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ExternalLink } from 'lucide-react';
import { Stream } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import ForeachBodyStepLog from '@/features/dags/components/dag-execution/ForeachBodyStepLog';
import StepLog from '@/features/dags/components/dag-execution/StepLog';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import { useI18n } from '@/i18n/I18nProvider';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import type { StepRunTarget } from './StepRunView';

export interface StepLogPaneProps {
  target: StepRunTarget;
  /** The run that holds the step; null before it loads or when none is selected. */
  holder: ApiDAGRunDetails | null;
  node: ApiNode | undefined;
}

function toStream(s: 'stdout' | 'stderr' | undefined): Stream {
  return s === 'stderr' ? Stream.stderr : Stream.stdout;
}

/** The full-page log of one step: /dags/:f/log?dagRunId&step&stream, or the sub-run form. */
export function stepLogPageURL({
  fileName,
  holder,
  remoteNode,
  step,
  stream,
}: {
  /** Workflow file used when the run names no source file. */
  fileName: string;
  holder: ApiDAGRunDetails;
  remoteNode: string;
  step: string;
  stream: Stream;
}): string {
  const sub = isSubDAGRun(holder);
  return buildDAGPageURL({
    fileName: holder.sourceFileName || fileName,
    remoteNode,
    tab: 'log',
    rootDAGRunId: sub ? holder.rootDAGRunId : holder.dagRunId,
    rootDAGRunName: sub ? holder.rootDAGRunName : undefined,
    subDAGRunId: sub ? holder.dagRunId : undefined,
    step,
    stream: stream !== Stream.stdout ? stream : undefined,
  });
}

/** The full-page scheduler log of a run: /dags/:f/dagRun-log, or the sub-run form. */
export function runLogPageURL({
  fileName,
  holder,
  remoteNode,
}: {
  /** Workflow file used when the run names no source file. */
  fileName: string;
  holder: ApiDAGRunDetails;
  remoteNode: string;
}): string {
  const sub = isSubDAGRun(holder);
  return buildDAGPageURL({
    fileName: holder.sourceFileName || fileName,
    remoteNode,
    tab: 'dagRun-log',
    rootDAGRunId: sub ? holder.rootDAGRunId : holder.dagRunId,
    rootDAGRunName: sub ? holder.rootDAGRunName : undefined,
    subDAGRunId: sub ? holder.dagRunId : undefined,
  });
}

/** A toolbar button of a log that opens its full page in a new tab. */
export function LogPageLink({ to }: { to: string }) {
  const { ts } = useI18n();
  const { basePath } = useConfig();
  return (
    <Button asChild size="icon-sm" variant="outline">
      <a
        href={`${basePath || ''}${to}`}
        target="_blank"
        rel="noopener noreferrer"
        title={ts('Open in new tab')}
        aria-label={ts('Open in new tab')}
      >
        <ExternalLink className="h-4 w-4" aria-hidden="true" />
      </a>
    </Button>
  );
}

/**
 * The step log of the dock: StepLog, or ForeachBodyStepLog for a foreach
 * body step. The log of a step, not of a foreach body step, opens on its
 * own page in a new tab.
 */
export function StepLogPane({ target, holder, node }: StepLogPaneProps) {
  const { ts } = useI18n();
  const [, setUrl] = useWorkspaceUrl();
  const { fileName } = useWorkspaceData('StepLogPane');
  const remoteNode = useRemoteNode();
  const stream = toStream(target.stream);

  if (!holder) {
    return (
      <p className="p-3 text-xs text-muted-foreground">
        {ts(target.subRun ? 'Loading run...' : 'No run selected')}
      </p>
    );
  }

  const { foreach } = target;
  return (
    <div
      className="flex h-full min-h-0 flex-col gap-1"
      data-testid="ww-step-log"
    >
      <div className="min-h-0 flex-1 px-3 pb-2 pt-1.5">
        {foreach ? (
          <ForeachBodyStepLog
            dagName={holder.name}
            dagRunId={holder.dagRunId}
            dagRun={holder}
            bodyStepName={foreach.bodyStep}
            foreach={{ stepName: target.step, item: foreach.item }}
            stream={stream}
          />
        ) : (
          <StepLog
            dagName={holder.name}
            dagRunId={holder.dagRunId}
            stepName={target.step}
            dagRun={holder}
            stream={stream}
            node={node}
            toolbarExtra={
              <LogPageLink
                to={stepLogPageURL({
                  fileName,
                  holder,
                  remoteNode,
                  step: target.step,
                  stream,
                })}
              />
            }
            onStreamShown={(next) => {
              setUrl(
                { stream: next === Stream.stderr ? 'stderr' : 'stdout' },
                { replace: true }
              );
            }}
          />
        )}
      </div>
    </div>
  );
}
