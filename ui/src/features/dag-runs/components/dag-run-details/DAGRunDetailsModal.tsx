// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Loader2, Maximize2, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import type { StatusTab } from '@/features/dags/components/DAGStatus';
import { cn } from '@/lib/utils';
import { components } from '../../../../api/v1/schema';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { shouldIgnoreKeyboardShortcuts } from '../../../../lib/keyboard-shortcuts';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { DAGRunContext } from '../../contexts/DAGRunContext';
import { useBoundedDAGRunDetails } from '../../hooks/useBoundedDAGRunDetails';
import { matchesRequestedDAGRunDetails } from '../../hooks/dagRunDetailsRequest';
import { buildDAGRunPageURL } from '../../lib/dagRunUrls';
import DAGRunDetailsContent from './DAGRunDetailsContent';

type DAGRunDetailsModalProps = {
  name: string;
  dagRunId: string;
  isOpen: boolean;
  onClose: () => void;
  initialTab?: StatusTab;
};

type PreviousData = {
  name: string;
  dagRunId: string;
  dagRunDetails: components['schemas']['DAGRunDetails'];
};

function DAGRunDetailsModal({
  name,
  dagRunId,
  isOpen,
  onClose,
  initialTab = 'status',
}: DAGRunDetailsModalProps): React.ReactElement | null {
  const navigate = useNavigate();

  const [shouldRender, setShouldRender] = useState(isOpen);
  const [isVisible, setIsVisible] = useState(false);
  const remoteNode = useRemoteNode();
  const previousDataRef = useRef<PreviousData | null>(null);
  const prevRemoteNodeRef = useRef(remoteNode);
  if (prevRemoteNodeRef.current !== remoteNode) {
    prevRemoteNodeRef.current = remoteNode;
    previousDataRef.current = null;
  }

  useEffect(() => {
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
      previousDataRef.current = null;
    }, 150);
    return () => clearTimeout(timer);
  }, [isOpen]);

  const searchParams = new URLSearchParams(window.location.search);
  const subDAGRunId = searchParams.get('subDAGRunId');
  const parentDAGRunId = searchParams.get('dagRunId');
  const parentName = searchParams.get('dagRunName') || name;
  const canQuerySubDag = Boolean(subDAGRunId && parentDAGRunId && parentName);
  const subDAGQueryEnabled = isOpen && canQuerySubDag;
  const dagRunQueryEnabled = isOpen && !canQuerySubDag && Boolean(name);
  const detailsTarget = subDAGQueryEnabled
    ? {
        remoteNode,
        name: name || '',
        dagRunId: dagRunId || 'latest',
        parentName,
        parentDAGRunId: parentDAGRunId ?? '',
        subDAGRunId: subDAGRunId ?? '',
      }
    : dagRunQueryEnabled
      ? {
          remoteNode,
          name: name || '',
          dagRunId: dagRunId || 'latest',
        }
      : null;

  const {
    data: latestDetails,
    error,
    isLoading,
    isValidating,
    refresh,
  } = useBoundedDAGRunDetails({
    target: detailsTarget,
    enabled: subDAGQueryEnabled || dagRunQueryEnabled,
    pollIntervalMs: isOpen ? 2000 : 0,
  });

  const expectedDagRunId = canQuerySubDag
    ? (subDAGRunId ?? '')
    : dagRunId || 'latest';
  const freshDetails = matchesRequestedDAGRunDetails(
    latestDetails,
    expectedDagRunId,
    canQuerySubDag ? undefined : name
  )
    ? latestDetails
    : null;
  const displayData = freshDetails ?? previousDataRef.current?.dagRunDetails;
  const displayName = freshDetails
    ? name
    : (previousDataRef.current?.name ?? name);
  const displayDagRunId = freshDetails
    ? dagRunId
    : (previousDataRef.current?.dagRunId ?? dagRunId);
  const fillContentHeight = initialTab === 'artifacts';

  useEffect(() => {
    if (freshDetails) {
      previousDataRef.current = { name, dagRunId, dagRunDetails: freshDetails };
    }
  }, [freshDetails, name, dagRunId]);

  // Note: previousDataRef is cleared synchronously during render (above)
  // when remoteNode changes, preventing stale cross-node data.

  const isInitialLoading = isLoading && !displayData;
  const previousData = previousDataRef.current;
  const isTransitioning =
    isValidating &&
    previousData !== null &&
    (previousData.dagRunId !== dagRunId || previousData.name !== name);

  const refreshFn = useCallback(() => {
    setTimeout(() => {
      void refresh();
    }, 500);
  }, [refresh]);

  const handleFullscreenClick = useCallback(
    (e?: React.MouseEvent): void => {
      const url = canQuerySubDag
        ? buildDAGRunPageURL({
            rootDAGRunName: parentName,
            rootDAGRunId: parentDAGRunId ?? '',
            remoteNode,
            subDAGRunId: subDAGRunId ?? '',
          })
        : buildDAGRunPageURL({
            rootDAGRunName: name,
            rootDAGRunId: dagRunId,
            remoteNode,
          });

      if (e?.metaKey || e?.ctrlKey) {
        window.open(url, '_blank');
      } else {
        navigate(url);
      }
    },
    [
      canQuerySubDag,
      dagRunId,
      name,
      navigate,
      parentDAGRunId,
      parentName,
      remoteNode,
      subDAGRunId,
    ]
  );

  useEffect(() => {
    if (!isOpen) {
      return;
    }

    function handleKeyDown(event: KeyboardEvent): void {
      if (shouldIgnoreKeyboardShortcuts()) {
        return;
      }

      if (event.key === 'Escape') {
        onClose();
      }

      if (event.key === 'f' || event.key === 'F') {
        handleFullscreenClick();
      }
    }

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose, handleFullscreenClick]);

  if (!shouldRender) {
    return null;
  }

  const modalVisibilityClass = isVisible
    ? 'translate-x-0 opacity-100'
    : 'translate-x-full opacity-0';

  return (
    <>
      <div className="fixed inset-0 h-screen w-screen z-40" onClick={onClose} />

      <div
        className={cn(
          'fixed top-0 bottom-0 right-0 z-50 h-screen w-full border-l border-indigo-500/30 bg-background transition-all duration-150 ease-out md:w-3/4',
          fillContentHeight ? 'overflow-hidden' : 'overflow-y-auto',
          modalVisibilityClass
        )}
      >
        <DAGRunContext.Provider
          value={{
            refresh: refreshFn,
            name: name || '',
            dagRunId: dagRunId || '',
          }}
        >
          <div className="p-6 w-full flex flex-col h-full dagRun-modal-content">
            <div className="flex justify-between items-center mb-4">
              <p className="text-xs text-muted-foreground">
                Use{' '}
                <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                  ↑
                </kbd>{' '}
                <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                  ↓
                </kbd>{' '}
                to navigate histories
              </p>
              <div className="flex gap-2 items-center">
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

            <div
              className={cn(
                'relative min-h-0 flex-1',
                fillContentHeight ? 'overflow-hidden' : 'overflow-y-auto'
              )}
            >
              {isTransitioning && (
                <div className="absolute top-2 right-2 z-10">
                  <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                </div>
              )}

              {isInitialLoading && (
                <div className="flex items-center justify-center h-full">
                  <LoadingIndicator />
                </div>
              )}
              {!isInitialLoading && error && !displayData && (
                <div className="p-4">
                  <div className="rounded-lg border border-error/30 bg-error-muted p-4 text-sm text-error">
                    {error.message || 'Failed to load DAG run details'}
                  </div>
                </div>
              )}
              {!isInitialLoading && displayData && (
                <DAGRunDetailsContent
                  name={displayName}
                  dagRun={displayData}
                  refreshFn={refreshFn}
                  dagRunId={displayDagRunId}
                  initialTab={initialTab}
                  fillHeight={fillContentHeight}
                />
              )}
            </div>
          </div>
        </DAGRunContext.Provider>
      </div>
    </>
  );
}

export default DAGRunDetailsModal;
