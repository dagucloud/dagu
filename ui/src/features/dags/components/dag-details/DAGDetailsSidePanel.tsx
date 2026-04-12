// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { createPortal } from 'react-dom';
import { useNavigate } from 'react-router-dom';
import { Maximize2, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { AppBarContext } from '@/contexts/AppBarContext';
import { UnsavedChangesProvider } from '@/contexts/UnsavedChangesContext';
import { useQuery } from '@/hooks/api';
import { useDAGRunSSE } from '@/hooks/useDAGRunSSE';
import { useDAGSSE } from '@/hooks/useDAGSSE';
import { whenEnabled } from '@/hooks/queryUtils';
import { sseFallbackOptions, useSSECacheSync } from '@/hooks/useSSECacheSync';
import dayjs from '@/lib/dayjs';
import { shouldIgnoreKeyboardShortcuts } from '@/lib/keyboard-shortcuts';
import { cn } from '@/lib/utils';
import LoadingIndicator from '@/ui/LoadingIndicator';
import type { components } from '@/api/v1/schema';
import { RootDAGRunContext } from '../../contexts/RootDAGRunContext';
import DAGDetailsContent from './DAGDetailsContent';

type DAGDetailsResponse = {
  dag?: components['schemas']['DAGDetails'];
  filePath?: string;
  latestDAGRun?: components['schemas']['DAGRunDetails'];
  localDags?: components['schemas']['LocalDag'][];
};

type DAGLoadState = 'loading' | 'ready' | 'not_found' | 'error';

type EnqueueHandler = (
  params: string,
  dagRunId?: string,
  immediate?: boolean
) => string | void | Promise<string | void>;

type Props = {
  fileName: string;
  isOpen: boolean;
  onClose: () => void;
  initialTab: string;
  toolbarHint?: React.ReactNode;
  backdropVisibleClassName?: string;
  renderInPortal?: boolean;
  forceEnqueue?: boolean;
  onEnqueue?: EnqueueHandler;
};

const CLOSE_ANIMATION_MS = 200;

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

function getLoadState(
  data: DAGDetailsResponse | undefined,
  error: unknown
): { state: DAGLoadState; message?: string } {
  const status = (error as { status?: number } | undefined)?.status;

  if (status === 404) {
    return {
      state: 'not_found',
      message: 'DAG not found or has been deleted.',
    };
  }

  if (data?.dag) {
    return { state: 'ready' };
  }

  if (!error) {
    return { state: 'loading' };
  }

  const errorMessage = (error as { message?: string } | undefined)?.message;
  const message =
    typeof errorMessage === 'string' && errorMessage
      ? errorMessage
      : 'Failed to load DAG details.';

  return { state: 'error', message };
}

function buildFullscreenUrl(fileName: string, activeTab: string): string {
  return activeTab === 'status'
    ? `/dags/${fileName}`
    : `/dags/${fileName}/${activeTab}`;
}

function DAGDetailsSidePanel({
  fileName,
  isOpen,
  onClose,
  initialTab,
  toolbarHint,
  backdropVisibleClassName = 'bg-black/20',
  renderInPortal = false,
  forceEnqueue = false,
  onEnqueue,
}: Props): React.ReactElement | null {
  const navigate = useNavigate();
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  const [shouldRender, setShouldRender] = React.useState(isOpen);
  const [isVisible, setIsVisible] = React.useState(false);
  const [activeTab, setActiveTab] = React.useState(initialTab);
  const [trackedDagRunId, setTrackedDagRunId] = React.useState<string>();
  const [currentDAGRun, setCurrentDAGRun] = React.useState<
    components['schemas']['DAGRunDetails'] | undefined
  >();

  const stableFileNameRef = React.useRef(fileName);
  if (fileName) {
    stableFileNameRef.current = fileName;
  }
  const stableFileName =
    isOpen || shouldRender ? stableFileNameRef.current : '';

  React.useEffect(() => {
    if (isOpen) {
      setShouldRender(true);
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          setIsVisible(true);
        });
      });
      return;
    }

    setIsVisible(false);
    const timer = setTimeout(() => {
      setShouldRender(false);
      setTrackedDagRunId(undefined);
      setCurrentDAGRun(undefined);
    }, CLOSE_ANIMATION_MS);
    return () => clearTimeout(timer);
  }, [isOpen]);

  React.useEffect(() => {
    if (!isOpen || !fileName) {
      return;
    }

    setActiveTab(initialTab);
    setTrackedDagRunId(undefined);
    setCurrentDAGRun(undefined);
  }, [fileName, initialTab, isOpen, remoteNode]);

  const navigateToStatusTab = React.useCallback(() => {
    setActiveTab('status');
  }, []);

  const dagDetailsEnabled = isOpen && !!stableFileName;
  const dagDetailsSSE = useDAGSSE(stableFileName, dagDetailsEnabled);
  const { data, error, mutate } = useQuery(
    '/dags/{fileName}',
    whenEnabled(dagDetailsEnabled, {
      params: {
        query: { remoteNode },
        path: { fileName: stableFileName },
      },
    }),
    sseFallbackOptions(dagDetailsSSE)
  );
  useSSECacheSync(dagDetailsSSE, mutate);

  const dagName = data?.dag?.name || '';
  const trackedRunEnabled = isOpen && !!dagName && !!trackedDagRunId;
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

  React.useEffect(() => {
    if (trackedRunData?.dagRunDetails) {
      setCurrentDAGRun(trackedRunData.dagRunDetails);
    } else if (data) {
      setCurrentDAGRun(data.latestDAGRun);
    }
  }, [data, trackedRunData]);

  const refreshFn = React.useCallback(() => {
    setTimeout(() => mutate(), 500);
  }, [mutate]);

  const handleEnqueue = React.useCallback<EnqueueHandler>(
    async (params, dagRunId, immediate) => {
      if (!onEnqueue) {
        return;
      }

      const result = await onEnqueue(params, dagRunId, immediate);
      setActiveTab('status');
      if (typeof result === 'string' && result) {
        setTrackedDagRunId(result);
      }
      await mutate();
      return result;
    },
    [mutate, onEnqueue]
  );

  const handleFullscreenClick = React.useCallback(
    (event?: React.MouseEvent) => {
      if (!stableFileName) {
        return;
      }

      const url = buildFullscreenUrl(stableFileName, activeTab);
      if (event?.metaKey || event?.ctrlKey) {
        window.open(url, '_blank', 'noopener,noreferrer');
      } else {
        navigate(url);
      }
    },
    [activeTab, navigate, stableFileName]
  );

  React.useEffect(() => {
    if (!isOpen) {
      return;
    }

    function handleKeyDown(event: KeyboardEvent): void {
      if (shouldIgnoreKeyboardShortcuts()) {
        return;
      }
      if (event.metaKey || event.ctrlKey) {
        return;
      }

      if (event.key === 'Escape') {
        onClose();
        return;
      }

      if (event.key === 'f' || event.key === 'F') {
        handleFullscreenClick();
      }
    }

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [handleFullscreenClick, isOpen, onClose]);

  if (!shouldRender) {
    return null;
  }

  const loadState = getLoadState(data, error);
  const backdropClassName = cn(
    'fixed inset-0 h-screen w-screen z-40 transition-colors duration-200',
    isVisible ? backdropVisibleClassName : 'bg-transparent'
  );
  const panelClassName = cn(
    'fixed top-0 bottom-0 right-0 md:w-3/4 w-full h-screen bg-background border-l border-border z-50 overflow-y-auto transition-all duration-200 ease-out',
    isVisible ? 'translate-x-0 opacity-100' : 'translate-x-full opacity-0'
  );

  const panel = (
    <>
      <div className={backdropClassName} onClick={onClose} />
      <div className={panelClassName}>
        <UnsavedChangesProvider>
          <RootDAGRunContext.Provider
            value={{
              data: currentDAGRun,
              setData: (dagRun: components['schemas']['DAGRunDetails']) => {
                setCurrentDAGRun(dagRun);
              },
            }}
          >
            <div className="p-6 w-full flex flex-col h-full dag-modal-content">
              <div className="flex justify-between items-center mb-4 gap-4">
                <div className="min-h-5 flex items-center text-xs text-muted-foreground">
                  {toolbarHint}
                </div>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={handleFullscreenClick}
                    title="Open in fullscreen (F) - Cmd/Ctrl+Click to open in new tab"
                    className="relative group"
                  >
                    <Maximize2 className="h-4 w-4" />
                    <span className="absolute -bottom-1 -right-1 bg-muted text-muted-foreground text-xs font-medium px-1 rounded-sm border opacity-0 group-hover:opacity-100 transition-opacity">
                      F
                    </span>
                  </Button>
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={onClose}
                    title="Close (Esc)"
                    className="relative group"
                  >
                    <X className="h-4 w-4" />
                    <span className="absolute -bottom-1 -right-1 bg-muted text-muted-foreground text-xs font-medium px-1 rounded-sm border opacity-0 group-hover:opacity-100 transition-opacity">
                      Esc
                    </span>
                  </Button>
                </div>
              </div>

              <div className="flex-1 overflow-y-auto overflow-x-hidden pr-4">
                {loadState.state === 'loading' && (
                  <div className="flex h-full flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
                    <LoadingIndicator />
                    <p>Loading DAG details...</p>
                  </div>
                )}

                {loadState.state === 'not_found' && (
                  <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
                    <p className="max-w-md text-sm text-muted-foreground">
                      {loadState.message}
                    </p>
                    <Button variant="outline" size="sm" onClick={onClose}>
                      Close
                    </Button>
                  </div>
                )}

                {loadState.state === 'error' && (
                  <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center">
                    <p className="max-w-md text-sm text-muted-foreground">
                      {loadState.message}
                    </p>
                    <div className="flex gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => void mutate()}
                      >
                        Retry
                      </Button>
                      <Button variant="ghost" size="sm" onClick={onClose}>
                        Close
                      </Button>
                    </div>
                  </div>
                )}

                {loadState.state === 'ready' && data?.dag && (
                  <DAGDetailsContent
                    fileName={stableFileName}
                    filePath={data.filePath}
                    dag={data.dag}
                    currentDAGRun={currentDAGRun}
                    dagRunId={trackedDagRunId ?? 'latest'}
                    stepName={null}
                    refreshFn={refreshFn}
                    formatDuration={formatDuration}
                    activeTab={activeTab}
                    onTabChange={setActiveTab}
                    isModal={true}
                    navigateToStatusTab={navigateToStatusTab}
                    localDags={data.localDags}
                    editorHints={data.editorHints}
                    onEnqueue={onEnqueue ? handleEnqueue : undefined}
                    forceEnqueue={forceEnqueue}
                    autoOpenStartModal={false}
                  />
                )}
              </div>
            </div>
          </RootDAGRunContext.Provider>
        </UnsavedChangesProvider>
      </div>
    </>
  );

  if (!renderInPortal) {
    return panel;
  }

  return createPortal(
    panel,
    document.querySelector('.radix-themes') || document.body
  );
}

export default DAGDetailsSidePanel;
