// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { CompactCount } from '@/components/ui/compact-count';
import { formatAbsoluteTime, formatElapsed } from '@/lib/dagRunTiming';
import { ScrollText, X } from 'lucide-react';
import React from 'react';
import NodeStatusChip from '../common/NodeStatusChip';
import { StepDetails } from './StepDetails';
import type { RightPanelId } from '@/contexts/RightPanelContext';
import { StepSideDrawer } from './StepSideDrawer';
import { ForeachItemsSection, type OpenBodyStepLog } from './foreach';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type Step = components['schemas']['Step'];
type Node = components['schemas']['Node'];
type DAGRunDetails = components['schemas']['DAGRunDetails'];

type LogStream = 'stdout' | 'stderr';

type StepDetailsDrawerProps = {
  dagName?: string;
  isOpen: boolean;
  onClose: () => void;
  step?: Step;
  /** Runtime state of the step within the inspected run; omitted in spec-only views. */
  node?: Node;
  /** The inspected run; lets a foreach step list its items. */
  dagRun?: DAGRunDetails;
  onViewLog?: (node: Node, stream: LogStream) => void;
  onOpenSubRun?: (node: Node, subRunIndex: number) => void;
  onViewBodyStepLog?: OpenBodyStepLog;
  /** The right-side panel that the drawer is; see StepSideDrawer. */
  panelId?: RightPanelId;
};

function StepRuntimeSection({
  node,
  dagName,
  dagRun,
  onViewLog,
  onOpenSubRun,
  onViewBodyStepLog,
}: {
  node: Node;
  dagName?: string;
  dagRun?: DAGRunDetails;
  onViewLog?: (stream: LogStream) => void;
  onOpenSubRun?: (subRunIndex: number) => void;
  onViewBodyStepLog?: OpenBodyStepLog;
}) {
  const isForeach = node.step.executorConfig?.type === 'foreach';
  const errorMessage = node.error || node.rejectionReason;
  const startedAt = formatAbsoluteTime(node.startedAt);
  const finishedAt = formatAbsoluteTime(node.finishedAt);
  const duration = startedAt
    ? formatElapsed(node.startedAt, node.finishedAt, { live: true })
    : null;
  const subRuns = [...(node.subRuns ?? []), ...(node.subRunsRepeated ?? [])];

  const infoRows: Array<[string, React.ReactNode]> = [];
  if (startedAt) infoRows.push(['Started', startedAt]);
  if (finishedAt) infoRows.push(['Finished', finishedAt]);
  if (duration) infoRows.push(['Duration', duration]);
  if (node.retryCount > 0) infoRows.push(['Retries', String(node.retryCount)]);
  if (node.doneCount > 1)
    infoRows.push([
      'Completions',
      <CompactCount key="completions" value={node.doneCount} />,
    ]);
  if (node.approvedBy) {
    infoRows.push([
      'Approved by',
      node.approvedAt
        ? `${node.approvedBy} (${formatAbsoluteTime(node.approvedAt) ?? node.approvedAt})`
        : node.approvedBy,
    ]);
  }
  if (node.rejectedBy) {
    infoRows.push([
      'Rejected by',
      node.rejectedAt
        ? `${node.rejectedBy} (${formatAbsoluteTime(node.rejectedAt) ?? node.rejectedAt})`
        : node.rejectedBy,
    ]);
  }
  if (node.humanTaskCompletedBy) {
    infoRows.push(['Completed by', node.humanTaskCompletedBy]);
  }

  return (
    <div className="mb-5 space-y-3">
      {errorMessage && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/5 p-3">
          <div className="max-h-40 overflow-auto whitespace-pre-wrap break-words text-sm text-foreground">
            {errorMessage}
          </div>
        </div>
      )}
      {infoRows.length > 0 && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          {infoRows.map(([label, value]) => (
            <React.Fragment key={label}>
              <dt className="text-muted-foreground">
                <I18nText text={label} />
              </dt>
              <dd className="min-w-0 whitespace-normal break-words tabular-nums text-foreground">
                {value}
              </dd>
            </React.Fragment>
          ))}
        </dl>
      )}
      {onViewLog && (
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs font-medium hover:bg-muted"
            onClick={() => onViewLog('stdout')}
          >
            <ScrollText className="h-3.5 w-3.5" />
            <I18nText text={'View stdout'} />
          </button>
          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs font-medium hover:bg-muted"
            onClick={() => onViewLog('stderr')}
          >
            <ScrollText className="h-3.5 w-3.5" />
            <I18nText text={'View stderr'} />
          </button>
        </div>
      )}
      {isForeach && dagRun && (
        <ForeachItemsSection
          stepRef={{
            dagName: dagName || dagRun.name,
            dagRunId: dagRun.dagRunId,
            stepName: node.step.name,
            dagRun,
          }}
          stepStatus={node.status}
          onOpenLog={onViewBodyStepLog}
        />
      )}
      {subRuns.length > 0 && (
        <div className="space-y-1">
          <div className="text-xs font-medium text-muted-foreground">
            <I18nText text={'Sub-runs'} />
          </div>
          {subRuns.map((subRun, index) => (
            <div
              key={`${subRun.dagRunId}-${index}`}
              className="flex items-center justify-between gap-2 text-sm"
            >
              <span className="min-w-0 whitespace-normal break-words">
                {subRun.dagName || node.step.name}{' '}
                <code className="text-xs text-muted-foreground">
                  {subRun.dagRunId}
                </code>
              </span>
              {onOpenSubRun && (
                <button
                  type="button"
                  className="rounded-md border border-border bg-background px-2 py-0.5 text-xs font-medium hover:bg-muted"
                  onClick={() => onOpenSubRun(index)}
                >
                  <I18nText text={'Open'} />
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function StepDetailsDrawer({
  dagName,
  isOpen,
  onClose,
  step,
  node,
  dagRun,
  onViewLog,
  onOpenSubRun,
  onViewBodyStepLog,
  panelId,
}: StepDetailsDrawerProps) {
  const [renderedStep, setRenderedStep] = React.useState(step);
  const [renderedNode, setRenderedNode] = React.useState(node);

  // The rendered snapshot is held across the exit animation so content does
  // not blank while the drawer slides out.
  React.useEffect(() => {
    if (isOpen && step) {
      setRenderedStep(step);
      setRenderedNode(node);
    }
  }, [isOpen, step, node]);

  if (!renderedStep) {
    return null;
  }

  return (
    <StepSideDrawer
      isOpen={isOpen}
      onClose={onClose}
      hasContent={!!step}
      panelId={panelId}
      labelledBy="step-details-title"
      header={
        <header className="flex items-start justify-between gap-4 border-b border-border bg-card px-4 py-3">
          <div className="min-w-0">
            <div className="text-xs font-medium text-muted-foreground">
              {dagName || <I18nText text={'Workflow'} />}
            </div>
            <h2
              id="step-details-title"
              className="mt-1 truncate text-base font-semibold text-foreground"
            >
              {renderedStep.name}
            </h2>
            <div className="mt-1 text-xs text-muted-foreground">
              {renderedNode ? (
                <NodeStatusChip status={renderedNode.status} size="sm">
                  {renderedNode.statusLabel}
                </NodeStatusChip>
              ) : (
                <I18nText text={'Step definition'} />
              )}
            </div>
          </div>
          <I18nProps>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={onClose}
              title="Close step details"
            >
              <X className="h-4 w-4" />
            </Button>
          </I18nProps>
        </header>
      }
    >
      <div className="min-h-0 flex-1 overflow-auto bg-background p-5">
        {renderedNode && (
          <StepRuntimeSection
            node={renderedNode}
            dagName={dagName}
            dagRun={dagRun}
            onViewBodyStepLog={
              onViewBodyStepLog
                ? (bodyStepName, foreach, stream, status) => {
                    onClose();
                    onViewBodyStepLog(bodyStepName, foreach, stream, status);
                  }
                : undefined
            }
            onViewLog={
              onViewLog
                ? (stream) => {
                    // The drawer and the log viewer are both modal portals;
                    // close this one before opening the other.
                    onClose();
                    onViewLog(renderedNode, stream);
                  }
                : undefined
            }
            onOpenSubRun={
              onOpenSubRun
                ? (subRunIndex) => {
                    onClose();
                    onOpenSubRun(renderedNode, subRunIndex);
                  }
                : undefined
            }
          />
        )}
        <StepDetails step={renderedStep} />
      </div>
    </StepSideDrawer>
  );
}
