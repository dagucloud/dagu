// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { Maximize2, X } from 'lucide-react';
import React from 'react';
import { useNavigate } from 'react-router-dom';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { shouldIgnoreKeyboardShortcuts } from '../../../../lib/keyboard-shortcuts';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { DAGRunContext } from '../../contexts/DAGRunContext';
import { matchesRequestedDAGRunDetails } from '../../hooks/dagRunDetailsRequest';
import { useBoundedDAGRunDetails } from '../../hooks/useBoundedDAGRunDetails';
import { buildDAGRunPageURL } from '../../lib/dagRunUrls';
import DAGRunDetailsContent from './DAGRunDetailsContent';

type Props = {
  name: string;
  dagRunId: string;
  onClose: () => void;
  onNavigate?: (direction: 'up' | 'down') => void;
};

function DAGRunDetailsPanel({
  name,
  dagRunId,
  onClose,
  onNavigate,
}: Props): React.ReactElement {
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();

  // Parse sub DAG-run params from URL
  const searchParams = new URLSearchParams(window.location.search);
  const subDAGRunId = searchParams.get('subDAGRunId');
  const parentDAGRunId = searchParams.get('dagRunId');
  const parentName = searchParams.get('dagRunName') || name;
  const isSubDAGRun = Boolean(subDAGRunId && parentDAGRunId && parentName);

  const detailsTarget = isSubDAGRun
    ? {
        remoteNode,
        name: name || '',
        dagRunId: dagRunId || 'latest',
        parentName: parentName as string,
        parentDAGRunId: parentDAGRunId as string,
        subDAGRunId: subDAGRunId as string,
      }
    : name
      ? {
          remoteNode,
          name,
          dagRunId: dagRunId || 'latest',
        }
      : null;

  const {
    data: latestDetails,
    error,
    refresh,
  } = useBoundedDAGRunDetails({
    target: detailsTarget,
    enabled: detailsTarget !== null,
    pollIntervalMs: detailsTarget ? 2000 : 0,
  });

  const expectedDagRunId = isSubDAGRun
    ? (subDAGRunId as string)
    : dagRunId || 'latest';
  const data = matchesRequestedDAGRunDetails(
    latestDetails,
    expectedDagRunId,
    isSubDAGRun ? undefined : name
  )
    ? { dagRunDetails: latestDetails }
    : null;
  const dagRunDetails = data?.dagRunDetails ?? null;

  const refreshFn = React.useCallback(() => {
    setTimeout(() => {
      void refresh();
    }, 500);
  }, [refresh]);

  const handleFullscreenClick = React.useCallback(
    (e?: React.MouseEvent) => {
      const url = isSubDAGRun
        ? buildDAGRunPageURL({
            rootDAGRunName: parentName,
            rootDAGRunId: parentDAGRunId as string,
            remoteNode,
            subDAGRunId: subDAGRunId as string,
          })
        : buildDAGRunPageURL({
            rootDAGRunName: name,
            rootDAGRunId: dagRunId,
            remoteNode,
          });

      if (e && (e.metaKey || e.ctrlKey)) {
        window.open(url, '_blank');
      } else {
        navigate(url);
      }
    },
    [
      dagRunId,
      isSubDAGRun,
      name,
      navigate,
      parentDAGRunId,
      parentName,
      remoteNode,
      subDAGRunId,
    ]
  );

  // Keyboard shortcuts
  React.useEffect(() => {
    function handleKeyDown(event: KeyboardEvent): void {
      if (shouldIgnoreKeyboardShortcuts()) {
        return;
      }

      switch (event.key) {
        case 'Escape':
          onClose();
          break;
        case 'f':
        case 'F':
          handleFullscreenClick();
          break;
        case 'ArrowDown':
          if (onNavigate) {
            event.preventDefault();
            onNavigate('down');
          }
          break;
        case 'ArrowUp':
          if (onNavigate) {
            event.preventDefault();
            onNavigate('up');
          }
          break;
      }
    }

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [onClose, onNavigate, handleFullscreenClick]);

  // Only show loading on initial load, not when switching DAG runs
  if (!dagRunDetails) {
    if (error) {
      return (
        <div className="flex h-full items-start justify-center p-4">
          <div className="w-full rounded-lg border border-error/30 bg-error-muted p-4 text-sm text-error">
            {error.message || 'Failed to load DAG run details'}
          </div>
        </div>
      );
    }
    return (
      <div className="flex items-center justify-center h-full">
        <LoadingIndicator />
      </div>
    );
  }

  return (
    <DAGRunContext.Provider
      value={{
        refresh: refreshFn,
        name: name || '',
        dagRunId: dagRunId || '',
      }}
    >
      <div className="p-4 w-full flex flex-col h-full overflow-hidden">
        <div className="flex justify-between items-center mb-3 flex-shrink-0">
          <p className="text-xs text-muted-foreground">
            Use{' '}
            <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
              ↑
            </kbd>{' '}
            <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
              ↓
            </kbd>{' '}
            to navigate runs
          </p>
          <div className="flex gap-2 items-center">
            <Button
              size="icon"
              onClick={handleFullscreenClick}
              title="Open in fullscreen (F) - Cmd/Ctrl+Click to open in new tab"
            >
              <Maximize2 className="h-4 w-4" />
            </Button>
            <Button size="icon" onClick={onClose} title="Close (Esc)">
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-hidden">
          <DAGRunDetailsContent
            name={name}
            dagRun={dagRunDetails}
            refreshFn={refreshFn}
            dagRunId={dagRunId}
            fillHeight
          />
        </div>
      </div>
    </DAGRunContext.Provider>
  );
}

export default DAGRunDetailsPanel;
