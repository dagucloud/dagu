// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertCircle,
  CircleDot,
  Code,
  RotateCcw,
  ScrollText,
} from 'lucide-react';
import { useMemo, type MouseEvent, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { NodeStatus, Stream } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { CommandDisplay } from '@/components/ui/command-display';
import { PanelMessage } from '@/components/ui/panel-message';
import RelativeTime from '@/components/ui/relative-time';
import { CompactCount } from '@/components/ui/compact-count';
import { RepeatBadge } from '@/components/ui/repeat-badge';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { RetryCount } from '@/components/ui/retry-count';
import { ScriptBadge } from '@/components/ui/script-dialog';
import { StatusDot } from '@/components/ui/status-dot';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { ManualActionSubject } from '@/features/dags/components/common/ManualActionSubject';
import PushBackHistory from '@/features/dags/components/common/PushBackHistory';
import HarnessStepSummary from '@/features/dags/components/dag-details/HarnessStepSummary';
import { LogStepMessage } from '@/features/dags/components/dag-details/LogStepMessage';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import { useStepOutputs } from '@/features/visual-editor/api/useStepOutputs';
import { runInputProvenance } from '@/features/visual-editor/run/inputProvenance';
import { stepParticipation } from '@/features/visual-editor/run/stepRunInfo';
import { useCaptureStepOutputs } from '@/features/visual-editor/run/useCaptureStepOutputs';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { ELAPSED_SLOT, useStepElapsed } from '@/hooks/useStepElapsed';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import {
  formatLogStepOutput,
  getExecutorCommand,
  getLogStepMessage,
} from '@/lib/executor-utils';
import { isHarnessStep } from '@/lib/harness-step';
import { isActiveNodeStatus, nodeStatusText } from '@/lib/status-utils';
import { statusClasses, toneClasses } from '@/lib/statusPalette';
import { cn } from '@/lib/utils';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { RepeatRule } from './RepeatRule';
import { StepForeach, isForeachStep } from './StepForeach';
import { stepLogPageURL } from './StepLogPane';
import type { StepRunTarget } from './StepRunView';
import { StepOutputRows } from './StepOutputRows';
import { StepSubRuns } from './StepSubRuns';
import { allSubRunsOf, useStepRunActions } from './useStepRunActions';

export interface StepFactsProps {
  target: StepRunTarget;
  /** The run that holds the step; null before it loads or when none is selected. */
  holder: ApiDAGRunDetails | null;
  node: ApiNode | undefined;
  onOpenLogs?(t: StepRunTarget): void;
}

const sectionTitle = 'text-xs font-medium text-muted-foreground';

const success = toneClasses('success');
const warning = toneClasses('warning');
const error = toneClasses('error');

/** The status of the step, coloured by lib/statusPalette.ts. */
function StatusChip({ node }: { node: ApiNode }) {
  const { ts } = useI18n();
  const colours = statusClasses(node.status);
  return (
    <span
      data-testid="ww-step-status"
      className={cn(
        'inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium',
        colours.bg,
        colours.text
      )}
    >
      <StatusDot
        kind="step"
        status={node.status}
        decorative
        className="h-1.5 w-1.5"
      />
      {ts(nodeStatusText(node.status))}
    </span>
  );
}

function BuildDecisionBadge({ node }: { node: ApiNode }) {
  const remoteNode = useRemoteNode();
  const { ts } = useI18n();
  const build = node.build;
  if (!build) return null;
  const label =
    build.decision === 'reuse'
      ? 'reused'
      : build.decision === 'always'
        ? 'always run'
        : build.decision;
  const producer = build.producerRun;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          className={cn(
            'inline-flex rounded-full border px-2 py-0.5 text-[11px] font-medium',
            build.decision === 'reuse'
              ? cn('border-transparent', success.bg, success.text)
              : 'border-border bg-muted text-muted-foreground'
          )}
        >
          {label}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-80 space-y-1 text-xs">
        <div className="font-medium">{build.reason.replace(/_/g, ' ')}</div>
        {build.decision === 'reuse' && <div>{ts('No executor ran.')}</div>}
        {build.detail && <div>{build.detail}</div>}
        {producer?.name && producer.id && (
          <Link
            className="block underline underline-offset-2"
            to={buildDAGRunPageURL({
              rootDAGRunName: producer.name,
              rootDAGRunId: producer.id,
              remoteNode,
            })}
          >
            {ts('Produced by')} {producer.name}:{producer.id}
          </Link>
        )}
      </TooltipContent>
    </Tooltip>
  );
}

/** The repeat policy of the step: the badge with the run count and the rule. */
function RepeatPolicy({ node }: { node: ApiNode }) {
  const policy = useMemo(
    () => readRepeatPolicy(node.step.repeatPolicy),
    [node.step.repeatPolicy]
  );
  if (!policy) return null;
  return (
    <div
      className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-muted-foreground"
      data-testid="ww-repeat"
    >
      <RepeatBadge
        policy={policy}
        count={node.doneCount}
        running={node.status === NodeStatus.Running}
      />
      <RepeatRule policy={policy} wrap />
    </div>
  );
}

function AuditLine({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="text-xs text-muted-foreground">
      <span className="font-medium">{label}</span> {children}
    </div>
  );
}

function ManualAudit({ node }: { node: ApiNode }) {
  const { ts } = useI18n();
  return (
    <>
      {(node.humanTaskCompletedBy || node.humanTaskCompletedById) && (
        <AuditLine label={ts('Completed by:')}>
          <ManualActionSubject
            name={node.humanTaskCompletedBy}
            id={node.humanTaskCompletedById}
            className="text-info"
          />
          {node.finishedAt && (
            <RelativeTime className="ml-1" timestamp={node.finishedAt} />
          )}
        </AuditLine>
      )}
      {(node.approvedBy || node.approvedById) && (
        <AuditLine label={ts('Approved by:')}>
          <ManualActionSubject
            name={node.approvedBy}
            id={node.approvedById}
            className="text-info"
          />
          {node.approvedAt && (
            <RelativeTime className="ml-1" timestamp={node.approvedAt} />
          )}
        </AuditLine>
      )}
      {node.approvalInputs && Object.keys(node.approvalInputs).length > 0 && (
        <AuditLine label={ts('Inputs:')}>
          <span className="whitespace-normal break-words font-mono text-foreground/80">
            {JSON.stringify(node.approvalInputs)}
          </span>
        </AuditLine>
      )}
      {node.pushBackHistory && node.pushBackHistory.length > 0 && (
        <PushBackHistory history={node.pushBackHistory} className="pt-1" />
      )}
      {(node.rejectedBy || node.rejectedById) && (
        <AuditLine label={ts('Rejected by:')}>
          <ManualActionSubject
            name={node.rejectedBy}
            id={node.rejectedById}
            className={error.text}
          />
          {node.rejectedAt && (
            <RelativeTime className="ml-1" timestamp={node.rejectedAt} />
          )}
        </AuditLine>
      )}
      {node.rejectionReason && (
        <AuditLine label={ts('Reason:')}>
          <span className="whitespace-normal break-words text-foreground/80">
            {node.rejectionReason}
          </span>
        </AuditLine>
      )}
    </>
  );
}

function ErrorCards({ node }: { node: ApiNode }) {
  const { ts } = useI18n();
  const unmet = node.step.preconditions?.filter((c) => c.error) ?? [];
  if (!node.error && unmet.length === 0) return null;
  return (
    <div className="space-y-2">
      {node.error && node.status === NodeStatus.Success && (
        // The status was set by hand after the failure: the error is history.
        <div
          data-testid="ww-step-previous-error"
          className="max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted p-2 text-xs leading-relaxed text-muted-foreground"
        >
          <div className="mb-1 font-medium">{ts('Previous error')}</div>
          {node.error}
        </div>
      )}
      {node.error && node.status !== NodeStatus.Success && (
        <div
          role="alert"
          className={cn(
            'max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-md p-2 text-xs leading-relaxed',
            error.bg,
            error.text
          )}
        >
          {node.error}
        </div>
      )}
      {unmet.length > 0 && (
        <div className="space-y-1">
          <div className={cn('text-xs font-medium', warning.text)}>
            {ts('Precondition unmet')}
          </div>
          {unmet.map((cond, i) => (
            <div
              key={i}
              className={cn(
                'whitespace-pre-wrap break-words rounded-md p-1.5 text-xs leading-tight',
                warning.bg,
                warning.text
              )}
            >
              <div className="font-medium">
                {ts('Condition:')} {cond.condition}
              </div>
              <div>
                {ts('Expected:')}{' '}
                {cond.expectedAny?.join(' | ') ?? cond.expected}
              </div>
              <div>
                {ts('Error:')} {cond.error}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** The stdout a log step wrote, shown in place of its configured message. */
function useLogStepOutput(holder: ApiDAGRunDetails, node: ApiNode) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const configured = getLogStepMessage(node.step);
  const enabled = configured !== null && !!node.stdout && !!holder.dagRunId;
  const sub = isSubDAGRun(holder);
  const options = {
    refreshInterval: isActiveNodeStatus(node.status) ? 2000 : 0,
    revalidateOnFocus: false,
  };
  const subQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/log',
    whenEnabled(enabled && sub, {
      params: {
        query: { remoteNode, stream: Stream.stdout },
        path: {
          name: holder.rootDAGRunName,
          dagRunId: holder.rootDAGRunId,
          subDAGRunId: holder.dagRunId,
          stepName: node.step.name,
        },
      },
    }),
    options
  );
  const rootQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log',
    whenEnabled(enabled && !sub, {
      params: {
        query: { remoteNode, stream: Stream.stdout },
        path: {
          name: holder.name,
          dagRunId: holder.dagRunId,
          stepName: node.step.name,
        },
      },
    }),
    options
  );
  const query = sub ? subQuery : rootQuery;
  const content = query.data?.content;
  if (typeof content === 'string') return formatLogStepOutput(content);
  if (enabled && !query.error) return ts('Loading log output...');
  return configured;
}

function ExecutionSection({
  holder,
  node,
}: {
  holder: ApiDAGRunDetails;
  node: ApiNode;
}) {
  const { ts } = useI18n();
  const step = node.step;
  const message = useLogStepOutput(holder, node);
  const harness = isHarnessStep(step);
  const commands = step.commands ?? [];
  const executorCommand =
    !harness && message === null && commands.length === 0
      ? getExecutorCommand(step)
      : null;
  if (
    !harness &&
    message === null &&
    commands.length === 0 &&
    !executorCommand &&
    !step.script
  ) {
    return null;
  }
  const label =
    message !== null
      ? 'Message:'
      : harness
        ? 'Execution:'
        : commands.length > 1
          ? 'Commands:'
          : 'Command:';
  return (
    <section className="space-y-1.5" aria-label={ts('Execution')}>
      <div className={sectionTitle}>{ts(label)}</div>
      {harness ? (
        <HarnessStepSummary step={step} />
      ) : message !== null ? (
        <LogStepMessage message={message} />
      ) : commands.length > 0 ? (
        <CommandDisplay commands={commands} icon="code" maxLength={80} />
      ) : executorCommand ? (
        <div className="flex items-start gap-1.5 text-xs">
          <Code
            className="mt-0.5 h-4 w-4 flex-shrink-0 text-primary"
            aria-hidden="true"
          />
          <code className="flex-1 whitespace-pre-wrap break-all rounded-md bg-muted px-2 py-1 font-mono text-xs text-foreground/90">
            {executorCommand}
          </code>
        </div>
      ) : null}
      {step.script && <ScriptBadge script={step.script} stepName={step.name} />}
    </section>
  );
}

function OutputsSection({
  holder,
  node,
}: {
  holder: ApiDAGRunDetails;
  node: ApiNode;
}) {
  const { ts } = useI18n();
  const { fileName } = useWorkspaceData('StepFacts');
  const state = stepParticipation(
    node,
    runInputProvenance(holder).has(node.step.name)
  );
  const ran = state === 'ran' || state === 'reused';
  // Outputs have a route for steps of a root run only. A running or waiting
  // step has none yet, and the request key does not change when it finishes.
  const readable =
    ran &&
    !isActiveNodeStatus(node.status) &&
    node.status !== NodeStatus.Waiting &&
    !isSubDAGRun(holder);
  const { data, error } = useStepOutputs(
    holder.name,
    readable ? holder.dagRunId : null,
    readable ? node.step.name : null
  );
  // The outputs that this section shows go to the cache of the browser.
  useCaptureStepOutputs({
    fileName: readable ? fileName : undefined,
    dagName: holder.name,
    runId: readable ? holder.dagRunId : null,
    step: node.step.name,
    data,
  });
  if (!readable) return null;
  if (error && !data) {
    return (
      <PanelMessage tone="error" align="start" className="p-0">
        {ts('Could not load {thing}', {
          thing: ts('the outputs of this step'),
        })}
      </PanelMessage>
    );
  }
  if (!data) return null;
  const { outputVariable, outputs } = data;
  return (
    <section className="space-y-1 text-xs" aria-label={ts('Outputs')}>
      <div className={sectionTitle}>{ts('Outputs')}</div>
      {outputs.length === 0 && !outputVariable ? (
        <PanelMessage tone="muted" align="start" className="p-0">
          {ts('No outputs in this run')}
        </PanelMessage>
      ) : (
        <StepOutputRows outputVariable={outputVariable} outputs={outputs} />
      )}
    </section>
  );
}

function TimingFacts({ node }: { node: ApiNode }) {
  const { ts } = useI18n();
  const duration = useStepElapsed(node);
  const startedAt = formatAbsoluteTime(node.startedAt);
  const finishedAt = formatAbsoluteTime(node.finishedAt);
  const active = isActiveNodeStatus(node.status);
  const rows: Array<[string, ReactNode]> = [];
  if (startedAt) rows.push(['Started', startedAt]);
  if (finishedAt) rows.push(['Finished', finishedAt]);
  if (startedAt && duration !== null) {
    rows.push([
      'Duration',
      <span className="inline-flex items-center gap-1.5" key="duration">
        <span className={ELAPSED_SLOT}>{duration}</span>
        {active && <StatusDot kind="step" status={node.status} decorative />}
      </span>,
    ]);
  }
  if (node.retryCount > 0) rows.push(['Retries', String(node.retryCount)]);
  if (node.doneCount > 1) {
    rows.push([
      'Completions',
      <CompactCount key="completions" value={node.doneCount} />,
    ]);
  }
  if (rows.length === 0) return null;
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-muted-foreground">{ts(label)}</dt>
          <dd className="min-w-0 break-words tabular-nums text-foreground">
            {value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** Every run fact of one step, for the OUTPUT column of the step view. It never mounts a step log. */
export function StepFacts({
  target,
  holder,
  node,
  onOpenLogs,
}: StepFactsProps) {
  const { ts } = useI18n();
  const { fileName } = useWorkspaceData('StepFacts');
  const remoteNode = useRemoteNode();
  const actions = useStepRunActions();

  if (!holder) {
    return (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('No run selected')}
      </PanelMessage>
    );
  }
  if (!node) {
    return (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('Not run in this run')}
      </PanelMessage>
    );
  }

  // Step actions address the selected run only.
  const gates = target.subRun ? null : actions.can(node.step.name);
  const logHref = (stream: 'stdout' | 'stderr') =>
    stepLogPageURL({
      fileName,
      holder,
      remoteNode,
      step: node.step.name,
      stream: stream === 'stderr' ? Stream.stderr : Stream.stdout,
    });
  // A Cmd/Ctrl+click keeps the link default: the log page opens in a new tab.
  const openLogs = (e: MouseEvent, stream: 'stdout' | 'stderr') => {
    if (e.metaKey || e.ctrlKey) return;
    e.preventDefault();
    const t: StepRunTarget = {
      step: node.step.name,
      stream,
      ...(target.subRun ? { subRun: target.subRun } : {}),
    };
    if (onOpenLogs) onOpenLogs(t);
    else actions.openLogs(t);
  };
  const canSetStatus = !!gates?.setStatus;
  // Run step is the 'Execute step' button of the node view header: one run
  // action there, none here.
  const hasActions = !!gates && (gates.retry || gates.setStatus);

  return (
    <div className="space-y-3 text-sm" data-testid="ww-step-facts">
      <div className="flex flex-wrap items-center gap-1.5">
        <span
          role={canSetStatus ? 'button' : undefined}
          tabIndex={canSetStatus ? 0 : undefined}
          title={canSetStatus ? ts('Click to update status') : undefined}
          className={cn(canSetStatus && 'cursor-pointer')}
          onClick={() => {
            if (canSetStatus) actions.setStatus(node.step.name);
          }}
          onKeyDown={(e) => {
            if (!canSetStatus || (e.key !== 'Enter' && e.key !== ' ')) return;
            e.preventDefault();
            actions.setStatus(node.step.name);
          }}
        >
          <StatusChip node={node} />
        </span>
        <BuildDecisionBadge node={node} />
        {node.retryCount > 0 && <RetryCount count={node.retryCount} />}
      </div>

      <ErrorCards node={node} />
      <TimingFacts node={node} />
      <RepeatPolicy node={node} />
      <ManualAudit node={node} />

      {(node.stdout || node.stderr) && (
        <div className="flex flex-wrap gap-2">
          {node.stdout && (
            <Button asChild variant="outline" size="xs">
              <a
                href={logHref('stdout')}
                title={ts('View stdout log (Cmd/Ctrl+Click for new tab)')}
                onClick={(e) => openLogs(e, 'stdout')}
              >
                <ScrollText aria-hidden="true" />
                {ts('View stdout')}
              </a>
            </Button>
          )}
          {node.stderr && (
            <Button asChild variant="outline" size="xs">
              <a
                href={logHref('stderr')}
                title={ts('View stderr log (Cmd/Ctrl+Click for new tab)')}
                onClick={(e) => openLogs(e, 'stderr')}
              >
                <AlertCircle className={warning.text} aria-hidden="true" />
                {ts('View stderr')}
              </a>
            </Button>
          )}
        </div>
      )}

      {hasActions && (
        <div className="flex flex-wrap gap-2">
          {gates.retry && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              title={gates.reason}
              onClick={() => actions.retryStep(node.step.name)}
            >
              <RotateCcw aria-hidden="true" />
              {ts('Retry step')}
            </Button>
          )}
          {gates.setStatus && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => actions.setStatus(node.step.name)}
            >
              <CircleDot aria-hidden="true" />
              {ts('Change status')}
            </Button>
          )}
        </div>
      )}

      <ExecutionSection holder={holder} node={node} />
      <OutputsSection holder={holder} node={node} />

      {allSubRunsOf(node).length > 0 && (
        <section className="space-y-1" aria-label={ts('Sub-runs')}>
          <div className={sectionTitle}>{ts('Sub-runs')}</div>
          <StepSubRuns
            node={node}
            holder={holder}
            parentSubDAGRunId={target.subRun?.subDAGRunId}
          />
        </section>
      )}

      {isForeachStep(node) && (
        <StepForeach
          node={node}
          holder={holder}
          subRun={target.subRun}
          onOpenLogs={onOpenLogs ?? actions.openLogs}
        />
      )}

      {actions.dialogs}
    </div>
  );
}
