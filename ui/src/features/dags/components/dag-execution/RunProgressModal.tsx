// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { ExternalLink, GitGraph, Terminal } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import LoadingIndicator from '@/components/ui/loading-indicator';
import StatusChip from '@/components/ui/status-chip';
import { Tab, Tabs } from '@/components/ui/tabs';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useI18n } from '@/i18n/I18nProvider';
import { DAGGraph } from '@/features/dags/components/visualization';
import { toMermaidNodeId } from '@/lib/utils';
import type { SubRunStackEntry } from '../common';
import RunOutput from './RunOutput';

type Props = {
  dagName: string;
  dagRunId: string;
  remoteNode?: string;
  visible: boolean;
  dismissModal: () => void;
};

function RunProgressModal({
  dagName,
  dagRunId,
  remoteNode,
  visible,
  dismissModal,
}: Props) {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const selectedRemoteNode = useRemoteNode();
  const activeRemoteNode = remoteNode || selectedRemoteNode;
  const [view, setView] = React.useState<'output' | 'visualization'>('output');
  const target = React.useMemo(
    () =>
      dagName && dagRunId
        ? {
            remoteNode: activeRemoteNode,
            name: dagName,
            dagRunId,
          }
        : null,
    [activeRemoteNode, dagName, dagRunId]
  );
  const {
    data: dagRun,
    error,
    isLoading,
    refresh,
  } = useBoundedDAGRunDetails({
    target,
    enabled: visible,
    pollIntervalMs: 2000,
  });
  const canOpenDetails = Boolean(dagName && dagRunId);

  React.useEffect(() => {
    if (visible) {
      setView('output');
    }
  }, [dagName, dagRunId, visible]);

  function openDetails(step?: string): void {
    if (!canOpenDetails) {
      return;
    }
    dismissModal();
    navigate(
      buildDAGRunPageURL({
        rootDAGRunName: dagName,
        rootDAGRunId: dagRunId,
        remoteNode: activeRemoteNode,
        step,
      })
    );
  }

  function findNode(stepId: string) {
    return dagRun?.nodes?.find(
      (node) => toMermaidNodeId(node.step.name) === stepId
    );
  }

  function openGraphStep(stepId: string): void {
    const node = findNode(stepId);
    if (node) {
      openDetails(node.step.name);
    }
  }

  function openGraphSubRun(stepId: string): void {
    const node = findNode(stepId);
    const subRuns = node
      ? [...(node.subRuns ?? []), ...(node.subRunsRepeated ?? [])]
      : [];
    const subRun = subRuns.length === 1 ? subRuns[0] : null;
    if (!node || !subRun?.dagRunId) {
      openGraphStep(stepId);
      return;
    }

    dismissModal();
    navigate(
      buildDAGRunPageURL({
        rootDAGRunName: dagRun?.rootDAGRunName || dagRun?.name || dagName,
        rootDAGRunId: dagRun?.rootDAGRunId || dagRun?.dagRunId || dagRunId,
        remoteNode: activeRemoteNode,
        subDAGRunId: subRun.dagRunId,
        step: node.step.name,
      })
    );
  }

  function openTimelineSubRun(entry: SubRunStackEntry): void {
    dismissModal();
    navigate(
      buildDAGRunPageURL({
        rootDAGRunName: dagRun?.rootDAGRunName || dagRun?.name || dagName,
        rootDAGRunId: dagRun?.rootDAGRunId || dagRun?.dagRunId || dagRunId,
        remoteNode: activeRemoteNode,
        subDAGRunId: entry.dagRunId,
      })
    );
  }

  return (
    <Dialog open={visible} onOpenChange={(open) => !open && dismissModal()}>
      <DialogContent className="flex max-h-[90dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[1000px] max-sm:left-0 max-sm:top-0 max-sm:h-[100dvh] max-sm:max-h-[100dvh] max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-none">
        <DialogHeader className="shrink-0 border-b border-border px-5 py-3 pr-12">
          <DialogTitle className="flex items-center gap-2 text-base">
            <Terminal className="h-4 w-4 text-muted-foreground" />
            {ts('Run progress')}
          </DialogTitle>
          <DialogDescription className="sr-only">
            {ts('Live output for the submitted DAG run.')}
          </DialogDescription>
        </DialogHeader>

        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-5 py-3">
          <div className="min-w-0">
            <div className="truncate text-sm font-medium">
              {dagRun?.name || dagName}
            </div>
            <div className="truncate font-mono text-xs text-muted-foreground">
              {dagRun?.dagRunId || dagRunId}
            </div>
          </div>
          {dagRun?.status && (
            <StatusChip status={dagRun.status} size="sm">
              {ts(dagRun.statusLabel)}
            </StatusChip>
          )}
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          {error ? (
            <div
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive"
            >
              {error.message || ts('Failed to load DAG run details')}
              <div className="mt-3">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void refresh()}
                >
                  {ts('Retry')}
                </Button>
              </div>
            </div>
          ) : dagRun ? (
            <div className="space-y-4">
              <Tabs className="h-9">
                <Tab
                  isActive={view === 'output'}
                  onClick={() => setView('output')}
                  className="h-9 gap-2 px-3"
                >
                  <Terminal className="h-4 w-4" />
                  {ts('Run output')}
                </Tab>
                <Tab
                  isActive={view === 'visualization'}
                  onClick={() => setView('visualization')}
                  className="h-9 gap-2 px-3"
                >
                  <GitGraph className="h-4 w-4" />
                  {ts('Visualization')}
                </Tab>
              </Tabs>
              {view === 'output' ? (
                <RunOutput
                  dagRun={dagRun}
                  onInspect={(node) => openDetails(node.step.name)}
                />
              ) : (
                <DAGGraph
                  dagRun={dagRun}
                  onClickStep={openGraphStep}
                  onSelectStep={openGraphSubRun}
                  onOpenSubRun={openTimelineSubRun}
                />
              )}
            </div>
          ) : (
            <div
              role="status"
              className="flex h-64 items-center justify-center gap-2 text-sm text-muted-foreground"
            >
              {isLoading && <LoadingIndicator />}
              {ts('Loading run...')}
            </div>
          )}
        </div>

        <DialogFooter className="shrink-0 border-t border-border px-5 py-3">
          <Button variant="ghost" onClick={dismissModal}>
            {ts('Close')}
          </Button>
          <Button onClick={() => openDetails()} disabled={!canOpenDetails}>
            <ExternalLink className="h-4 w-4" />
            {ts('View details')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default RunProgressModal;
