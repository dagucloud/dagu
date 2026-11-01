// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ScrollText } from 'lucide-react';
import { useState, type ReactElement } from 'react';
import { Spinner } from '@/components/ui/spinner';
import NodeStatusChip from '@/features/dags/components/common/NodeStatusChip';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { NodeStatus, Stream, type components } from '@/api/v1/schema';
import LogViewer from '@/features/dags/components/dag-execution/LogViewer';
import { StepDetailsDrawer } from '@/features/dags/components/step-details';
import { useStepOutputs } from '../api/useStepOutputs';
import { JsonValue } from '../references/JsonValue';
import { formatAbsoluteTime, formatElapsed } from '@/lib/dagRunTiming';
import { runInputProvenance } from './inputProvenance';
import {
  findRunNode,
  formatOutputSize,
  isRunActive,
  stepParticipation,
} from './stepRunInfo';

type OutputValue = components['schemas']['StepOutputValue'];
type Node = components['schemas']['Node'];

const ACTIVE_RUN_REFRESH_MS = 2000;

type LogTarget = { node: Node; stream: 'stdout' | 'stderr' };

type Props = { dagName: string; runId: string | null; stepName: string };

function Row({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">
        <I18nText text={label} />
      </dt>
      <dd className="min-w-0 tabular-nums text-foreground">{value}</dd>
    </>
  );
}

function OutputItem({
  value,
  prefix,
}: {
  value: OutputValue;
  prefix?: string;
}) {
  const { ts } = useI18n();
  return (
    <li className="space-y-0.5">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <span className="font-medium text-foreground">
          {prefix && <>{ts(prefix)}: </>}
          {value.name}
        </span>
        {value.truncated && (
          <span className="text-warning">
            {ts('truncated ({size})', { size: formatOutputSize(value.size) })}
          </span>
        )}
      </div>
      <JsonValue value={value.value} />
    </li>
  );
}

function OutputsSection({
  outputs,
  error,
}: {
  outputs: components['schemas']['StepOutputs'] | undefined;
  error: unknown;
}) {
  if (error && !outputs) {
    return (
      <p className="text-muted-foreground">
        <I18nText text="Failed to load data" />
      </p>
    );
  }
  if (!outputs) return null;
  const { outputVariable } = outputs;
  return (
    <div className="space-y-1">
      <div className="font-medium text-muted-foreground">
        <I18nText text="Outputs" />
      </div>
      {outputs.outputs.length === 0 && !outputVariable ? (
        <p className="text-muted-foreground">
          <I18nText text="No outputs collected" />
        </p>
      ) : (
        <ul className="m-0 list-none space-y-1.5 p-0">
          {outputVariable && (
            <OutputItem value={outputVariable} prefix="Output variable" />
          )}
          {outputs.outputs.map((o) => (
            <OutputItem key={o.name} value={o} />
          ))}
        </ul>
      )}
    </div>
  );
}

export function StepRunPanel({
  dagName,
  runId,
  stepName,
}: Props): ReactElement {
  const remoteNode = useRemoteNode();
  const { ts } = useI18n();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [logTarget, setLogTarget] = useState<LogTarget | null>(null);
  // Same key as the run overlay, so SWR shares the request. Live SSE updates
  // of the overlay do not reach this cache, so poll while the run is active.
  const { data, error, isLoading } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    runId
      ? {
          params: {
            path: { name: dagName, dagRunId: runId },
            query: { remoteNode },
          },
        }
      : null,
    {
      refreshInterval: (latest) =>
        isRunActive(latest?.dagRunDetails) ? ACTIVE_RUN_REFRESH_MS : 0,
    }
  );

  const details = data?.dagRunDetails;
  const node = findRunNode(details, stepName);
  const state = stepParticipation(
    node,
    runInputProvenance(details).has(stepName)
  );
  const shown = state === 'ran' || state === 'reused';
  const stepRunning =
    node?.status === NodeStatus.Running || node?.status === NodeStatus.Retrying;
  // An absent, unstarted, running or waiting step has no outputs to ask for
  // yet. The request key does not change when such a step finishes, so an
  // early read would stay empty.
  const hasOutputs =
    shown && !stepRunning && node?.status !== NodeStatus.Waiting;
  const outputs = useStepOutputs(dagName, runId, hasOutputs ? stepName : null);
  // The run goes on and the step has not started: it waits its turn.
  const waiting =
    !!details &&
    isRunActive(details) &&
    (state === 'absent' || state === 'notRun');

  if (runId === null) {
    return (
      <p className="text-xs text-muted-foreground">
        <I18nText text="No run selected" />
      </p>
    );
  }

  return (
    <div className="space-y-1.5 text-xs">
      {isLoading && !details && (
        <p className="text-muted-foreground">
          <I18nText text="Loading run..." />
        </p>
      )}
      {error && !details && (
        <p className="text-muted-foreground">
          <I18nText text="Failed to load DAG run details" />
        </p>
      )}
      {waiting && (
        <p
          data-testid="step-run-waiting"
          className="flex items-center gap-1.5 text-muted-foreground"
        >
          <Spinner />
          <I18nText text="Waiting for the step to run" />
        </p>
      )}
      {details && !waiting && (state === 'absent' || state === 'notRun') && (
        <p className="text-muted-foreground">
          <I18nText text="Not run in this run" />
        </p>
      )}
      {node && shown && (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <NodeStatusChip status={node.status} size="sm">
              {node.statusLabel}
            </NodeStatusChip>
            {state === 'reused' && (
              <span className="text-muted-foreground">
                <I18nText text="Reused from an earlier run" />
              </span>
            )}
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
            <Row
              label="Started"
              value={formatAbsoluteTime(node.startedAt) ?? '-'}
            />
            <Row
              label="Finished"
              value={formatAbsoluteTime(node.finishedAt) ?? '-'}
            />
            <Row
              label="Duration"
              value={
                formatElapsed(node.startedAt, node.finishedAt, {
                  live: true,
                }) ?? '-'
              }
            />
          </dl>
          {node.error && (
            <div className="max-h-32 overflow-auto whitespace-pre-wrap break-words rounded border border-destructive/40 bg-destructive/10 p-1.5 text-destructive">
              <span className="font-medium">
                <I18nText text="Error" />:
              </span>{' '}
              {node.error}
            </div>
          )}
          {stepRunning && (
            <p
              data-testid="step-run-running"
              className="flex items-center gap-1.5 text-muted-foreground"
            >
              <Spinner />
              <I18nText text="Running" />
            </p>
          )}
          <OutputsSection outputs={outputs.data} error={outputs.error} />
          <button
            type="button"
            aria-label={ts('View logs for {name}', { name: stepName })}
            className="inline-flex h-7 items-center gap-1 rounded border border-border bg-transparent px-2 text-xs font-medium text-foreground hover:bg-muted"
            onClick={() => setDetailsOpen(true)}
          >
            <ScrollText className="h-3.5 w-3.5" aria-hidden="true" />
            <I18nText text="View logs" />
          </button>
        </>
      )}
      {details && node && shown && (
        <>
          <StepDetailsDrawer
            dagName={dagName}
            isOpen={detailsOpen}
            step={node.step}
            node={node}
            dagRun={details}
            onClose={() => setDetailsOpen(false)}
            onViewLog={(n, stream) => setLogTarget({ node: n, stream })}
          />
          <LogViewer
            isOpen={logTarget !== null}
            onClose={() => setLogTarget(null)}
            logType="step"
            dagName={dagName}
            dagRunId={details.dagRunId}
            stepName={stepName}
            dagRun={details}
            stream={
              logTarget?.stream === 'stderr' ? Stream.stderr : Stream.stdout
            }
            node={logTarget?.node ?? node}
          />
        </>
      )}
    </div>
  );
}
