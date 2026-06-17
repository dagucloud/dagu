// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Maximize2, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { components } from '../../../../api/v1/schema';
import { usePageContext } from '../../../../contexts/PageContext';
import {
  RemoteNodeProvider,
  useRemoteNode,
} from '../../../../contexts/RemoteNodeContext';
import { UnsavedChangesProvider } from '../../../../contexts/UnsavedChangesContext';
import { useQuery } from '../../../../hooks/api';
import { useDAGRunSSE } from '../../../../hooks/useDAGRunSSE';
import { useDAGSSE } from '../../../../hooks/useDAGSSE';
import { whenEnabled } from '../../../../hooks/queryUtils';
import {
  sseFallbackOptions,
  useSSECacheSync,
} from '../../../../hooks/useSSECacheSync';
import dayjs from '../../../../lib/dayjs';
import { shouldIgnoreKeyboardShortcuts } from '../../../../lib/keyboard-shortcuts';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { DAGContext } from '../../contexts/DAGContext';
import { RootDAGRunContext } from '../../contexts/RootDAGRunContext';
import DAGDetailsContent from './DAGDetailsContent';

function formatDuration(startDate: string, endDate: string): string {
  if (!startDate || !endDate) {
    return '--';
  }

  const duration = dayjs.duration(dayjs(endDate).diff(dayjs(startDate)));
  const hours = Math.floor(duration.asHours());
  const minutes = duration.minutes();
  const seconds = duration.seconds();

  if (hours > 0) {
    return `${hours}h ${minutes}m ${seconds}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

type Props = {
  fileName: string;
  onClose: () => void;
  onNavigate?: (direction: 'up' | 'down') => void;
};

type DAGRunDetails = components['schemas']['DAGRunDetails'];

function DAGDetailsPanel({
  fileName,
  onClose,
  onNavigate,
}: Props): React.ReactElement | null {
  const navigate = useNavigate();
  const { setContext } = usePageContext();
  const remoteNode = useRemoteNode();

  const [currentDAGRun, setCurrentDAGRun] = useState<
    DAGRunDetails | undefined
  >();
  const [trackedDagRunId, setTrackedDagRunId] = useState<string>();
  const [activeTab, setActiveTab] = useState('status');
  const [notFound, setNotFound] = useState(false);

  // Set page context for agent chat
  useEffect(() => {
    if (fileName) {
      setContext({
        dagFile: fileName,
        source: 'dag-details-panel',
      });
    }
    return () => {
      setContext(null);
    };
  }, [fileName, setContext]);

  const dagSSE = useDAGSSE(fileName, !!fileName, remoteNode);
  // Fetch DAG details — SWR is the single source of truth, refreshed by live invalidations
  const sseOpts = sseFallbackOptions(dagSSE);
  const { data, error, mutate } = useQuery(
    '/dags/{fileName}',
    {
      params: {
        query: { remoteNode },
        path: { fileName: fileName || '' },
      },
    },
    { ...sseOpts, refreshInterval: notFound ? 0 : sseOpts.refreshInterval }
  );
  useSSECacheSync(dagSSE, mutate);

  const dagName = data?.dag?.name || '';
  const trackedRunEnabled = !!dagName && !!trackedDagRunId;
  const trackedRunSSE = useDAGRunSSE(
    dagName,
    trackedDagRunId || '',
    trackedRunEnabled,
    remoteNode
  );
  const { data: trackedRunData, mutate: mutateTrackedRun } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    whenEnabled(trackedRunEnabled, {
      params: {
        path: { name: dagName, dagRunId: trackedDagRunId || '' },
        query: { remoteNode },
      },
    }),
    sseFallbackOptions(trackedRunSSE)
  );
  useSSECacheSync(trackedRunSSE, mutateTrackedRun);

  // Track data loading state and handle 404 errors
  useEffect(() => {
    if (error) {
      const is404 = (error as { status?: number })?.status === 404;
      if (is404 && !data) {
        setNotFound(true);
      }
    } else if (data) {
      setNotFound(false);
    }
  }, [error, data]);

  // Reset UI state when switching DAGs or nodes
  useEffect(() => {
    setNotFound(false);
    setActiveTab('status');
    setTrackedDagRunId(undefined);
    setCurrentDAGRun(undefined);
  }, [fileName, remoteNode]);

  function refreshFn(): void {
    setTimeout(() => mutate(), 500);
    if (trackedDagRunId) {
      setTimeout(() => mutateTrackedRun(), 500);
    }
  }

  const handleRunStarted = useCallback(
    (dagRunId: string) => {
      setTrackedDagRunId(dagRunId);
      setActiveTab('status');
      void mutate();
    },
    [mutate]
  );

  function handleFullscreenClick(e?: React.MouseEvent): void {
    const tabPath = activeTab === 'status' ? '' : `/${activeTab}`;
    const searchParams = new URLSearchParams();
    if (trackedDagRunId) {
      searchParams.set('dagRunId', trackedDagRunId);
      if (data?.dag?.name) {
        searchParams.set('dagRunName', data.dag.name);
      }
    }
    if (remoteNode && remoteNode !== 'local') {
      searchParams.set('remoteNode', remoteNode);
    }
    const query = searchParams.toString();
    const url = `/dags/${fileName}${tabPath}${query ? `?${query}` : ''}`;

    if (e?.metaKey || e?.ctrlKey) {
      window.open(url, '_blank');
    } else {
      navigate(url);
    }
  }

  useEffect(() => {
    if (trackedRunData?.dagRunDetails) {
      setCurrentDAGRun(trackedRunData.dagRunDetails);
    } else if (data) {
      setCurrentDAGRun(data.latestDAGRun);
    }
  }, [data, trackedRunData]);

  const displayDAGRun = currentDAGRun || data?.latestDAGRun;

  // Keyboard shortcuts
  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent): void {
      if (shouldIgnoreKeyboardShortcuts()) {
        return;
      }

      if (event.key === 'Escape') {
        onClose();
        return;
      }

      if (event.key === 'f' || event.key === 'F') {
        handleFullscreenClick();
        return;
      }

      if (
        (event.key === 'ArrowDown' || event.key === 'ArrowUp') &&
        onNavigate
      ) {
        event.preventDefault();
        onNavigate(event.key === 'ArrowDown' ? 'down' : 'up');
      }
    }

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [
    onClose,
    onNavigate,
    activeTab,
    fileName,
    navigate,
    trackedDagRunId,
    data?.dag?.name,
    remoteNode,
  ]);

  // Show error state if DAG not found
  if (notFound) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-4 text-muted-foreground">
        <p className="text-sm">DAG not found or has been deleted.</p>
        <Button variant="outline" size="sm" onClick={onClose}>
          Close Tab
        </Button>
      </div>
    );
  }

  // Only show loading on initial load, not when switching DAGs
  // Gate on dag existence, not latestDAGRun, so DAGs with no runs can still be displayed
  if (!data?.dag) {
    return (
      <div className="flex items-center justify-center h-full">
        <LoadingIndicator />
      </div>
    );
  }

  return (
    <UnsavedChangesProvider>
      <RemoteNodeProvider remoteNode={remoteNode}>
        <DAGContext.Provider
          value={{
            refresh: refreshFn,
            fileName: fileName || '',
            name: data.dag.name || '',
          }}
        >
          <RootDAGRunContext.Provider
            value={{
              data: displayDAGRun,
              setData: setCurrentDAGRun,
            }}
          >
            <div className="px-2 pt-2 w-full flex flex-col h-full overflow-hidden">
              <div className="flex justify-between items-center mb-2 flex-shrink-0 pr-4">
                <p className="text-xs text-muted-foreground">
                  Use{' '}
                  <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                    ↑
                  </kbd>{' '}
                  <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                    ↓
                  </kbd>{' '}
                  to navigate DAGs
                </p>
                <div className="flex gap-2">
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

              <div className="flex-1 overflow-y-auto overflow-x-hidden min-h-0 pr-4">
                <DAGDetailsContent
                  fileName={fileName}
                  filePath={data.filePath}
                  dag={data.dag}
                  currentDAGRun={displayDAGRun}
                  refreshFn={refreshFn}
                  formatDuration={formatDuration}
                  activeTab={activeTab}
                  onTabChange={setActiveTab}
                  dagRunId={trackedDagRunId ?? 'latest'}
                  stepName={null}
                  isModal={true}
                  navigateToStatusTab={() => setActiveTab('status')}
                  localDags={data.localDags}
                  editorHints={data.editorHints}
                  onRunStarted={handleRunStarted}
                />
              </div>
            </div>
          </RootDAGRunContext.Provider>
        </DAGContext.Provider>
      </RemoteNodeProvider>
    </UnsavedChangesProvider>
  );
}

export default DAGDetailsPanel;
