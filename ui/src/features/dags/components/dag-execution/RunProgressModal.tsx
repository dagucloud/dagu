// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { ExternalLink, Terminal } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import LoadingIndicator from '@/components/ui/loading-indicator';
import StatusChip from '@/components/ui/status-chip';
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
import RunOutput from './RunOutput';

type Props = {
  dagName: string;
  dagRunId: string;
  visible: boolean;
  dismissModal: () => void;
};

function RunProgressModal({ dagName, dagRunId, visible, dismissModal }: Props) {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();
  const target = React.useMemo(
    () =>
      dagName && dagRunId
        ? {
            remoteNode,
            name: dagName,
            dagRunId,
          }
        : null,
    [dagName, dagRunId, remoteNode]
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

  function openDetails(step?: string): void {
    if (!canOpenDetails) {
      return;
    }
    dismissModal();
    navigate(
      buildDAGRunPageURL({
        rootDAGRunName: dagName,
        rootDAGRunId: dagRunId,
        remoteNode,
        step,
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
            <RunOutput
              dagRun={dagRun}
              onInspect={(node) => openDetails(node.step.name)}
            />
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
