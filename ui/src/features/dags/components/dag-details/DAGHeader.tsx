// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Calendar, Check, Copy, RefreshCw, Server, Timer } from 'lucide-react';
import React, { useCallback, useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { components, Status } from '../../../../api/v1/schema';
import dayjs from '../../../../lib/dayjs';
import StatusChip from '@/components/ui/status-chip';
import AutoRetryBadge from '../../../dag-runs/components/common/AutoRetryBadge';
import { RootDAGRunContext } from '../../contexts/RootDAGRunContext';
import { DAGActions } from '../common';

interface DAGHeaderProps {
  dag: components['schemas']['DAG'] | components['schemas']['DAGDetails'];
  currentDAGRun?: components['schemas']['DAGRunDetails'];
  fileName: string;
  filePath?: string;
  refreshFn: () => void;
  formatDuration: (startDate: string, endDate: string) => string;
  navigateToStatusTab?: () => void;
}

const DAGHeader: React.FC<DAGHeaderProps> = ({
  dag,
  currentDAGRun,
  fileName,
  filePath,
  refreshFn,
  formatDuration,
  navigateToStatusTab,
}) => {
  const navigate = useNavigate();
  const params = useParams<{ tab?: string }>();
  const rootDAGRunContext = React.useContext(RootDAGRunContext);
  const [isRefreshing, setIsRefreshing] = React.useState(false);
  const [currentDuration, setCurrentDuration] = React.useState<string>('--');
  const [copiedPath, setCopiedPath] = React.useState(false);

  const copyFilePath = useCallback(async () => {
    if (!filePath) return;
    try {
      await navigator.clipboard.writeText(filePath);
      setCopiedPath(true);
      setTimeout(() => setCopiedPath(false), 2000);
    } catch {
      const textArea = document.createElement('textarea');
      textArea.value = filePath;
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
      setCopiedPath(true);
      setTimeout(() => setCopiedPath(false), 2000);
    }
  }, [filePath]);

  // Use the DAG-run from context if available, otherwise use the prop
  const dagRunToDisplay = rootDAGRunContext.data || currentDAGRun;

  // Calculate duration between start and end times
  const calculateDuration = React.useCallback(() => {
    if (!dagRunToDisplay?.startedAt || dagRunToDisplay.startedAt === '-') {
      return '--';
    }

    const end =
      dagRunToDisplay.finishedAt && dagRunToDisplay.finishedAt !== '-'
        ? dagRunToDisplay.finishedAt
        : dayjs().toISOString();

    return formatDuration(dagRunToDisplay.startedAt, end);
  }, [dagRunToDisplay?.startedAt, dagRunToDisplay?.finishedAt, formatDuration]);

  // Determine if the DAG is currently running
  const isRunning = dagRunToDisplay?.status === Status.Running;

  // Auto-update duration every second for running DAGs
  useEffect(() => {
    if (isRunning && dagRunToDisplay?.startedAt) {
      // Initial calculation
      setCurrentDuration(calculateDuration());

      // Set up interval to update duration every second
      const intervalId = setInterval(() => {
        setCurrentDuration(calculateDuration());
      }, 1000);

      // Clean up interval on unmount or when status changes
      return () => clearInterval(intervalId);
    } else {
      // For non-running DAGs, calculate once
      setCurrentDuration(calculateDuration());
    }
  }, [isRunning, dagRunToDisplay?.startedAt, dagRunToDisplay?.finishedAt]);

  const handleRootDAGRunClick = (e: React.MouseEvent) => {
    e.preventDefault();
    if (!dagRunToDisplay) return;
    navigate(
      `/dags/${fileName}?dagRunId=${dagRunToDisplay.rootDAGRunId}&dagRunName=${encodeURIComponent(dagRunToDisplay.rootDAGRunName)}`
    );
  };

  const handleParentDAGRunClick = (e: React.MouseEvent) => {
    e.preventDefault();
    if (!dagRunToDisplay) return;
    navigate(
      `/dags/${fileName}?subDAGRunId=${dagRunToDisplay.parentDAGRunId}&dagRunId=${dagRunToDisplay.rootDAGRunId}&dagRunName=${encodeURIComponent(dagRunToDisplay.rootDAGRunName)}`
    );
  };

  const handleRefresh = () => {
    setIsRefreshing(true);
    refreshFn();
    setTimeout(() => setIsRefreshing(false), 600);
  };

  // Add keyboard shortcut for refresh
  useEffect(() => {
    const handleKeyPress = (e: KeyboardEvent) => {
      // Get current tab (default to 'status' if not set)
      const currentTab = params.tab || 'status';

      // Only trigger on status tab and when not typing
      if (currentTab !== 'status') return;

      // Check if user is typing in an input field
      const target = e.target as HTMLElement;
      if (
        target.tagName === 'INPUT' ||
        target.tagName === 'TEXTAREA' ||
        target.contentEditable === 'true' ||
        target.closest('.monaco-editor') ||
        target.closest('[role="textbox"]')
      ) {
        return;
      }

      // Check for 'r' key without modifiers
      if (
        e.key === 'r' &&
        !e.metaKey &&
        !e.ctrlKey &&
        !e.altKey &&
        !e.shiftKey
      ) {
        e.preventDefault();
        handleRefresh();
      }
    };

    window.addEventListener('keydown', handleKeyPress);
    return () => window.removeEventListener('keydown', handleKeyPress);
  }, [params.tab, handleRefresh]);

  // Show actions for root DAG-runs or when no DAG-run data exists
  const showActions =
    !dagRunToDisplay ||
    !dagRunToDisplay.rootDAGRunId ||
    dagRunToDisplay.dagRunId === dagRunToDisplay.rootDAGRunId;

  return (
    <div className="min-w-0 rounded-xl border border-border bg-card p-4 shadow-sm sm:rounded-2xl sm:p-6">
      {/* Header with title and actions */}
      <div className="mb-4 flex min-w-0 flex-col items-stretch gap-4 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
        <div className="flex-1 min-w-0">
          {/* Breadcrumb navigation */}
          {dagRunToDisplay && (
            <nav className="flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground mb-2">
              {dagRunToDisplay.rootDAGRunId &&
                dagRunToDisplay.rootDAGRunId !== dagRunToDisplay.dagRunId && (
                  <>
                    <a
                      href={`/dags/${fileName}?dagRunId=${dagRunToDisplay.rootDAGRunId}&dagRunName=${encodeURIComponent(dagRunToDisplay.rootDAGRunName)}`}
                      onClick={handleRootDAGRunClick}
                      className="text-primary hover:text-primary hover:underline transition-colors font-medium"
                    >
                      {dagRunToDisplay.rootDAGRunName}
                    </a>
                    <span className="text-muted-foreground mx-1">/</span>
                  </>
                )}

              {dagRunToDisplay.parentDAGRunName &&
                dagRunToDisplay.parentDAGRunId &&
                dagRunToDisplay.parentDAGRunName !==
                  dagRunToDisplay.rootDAGRunName &&
                dagRunToDisplay.parentDAGRunName !== dagRunToDisplay.name && (
                  <>
                    <a
                      href={`/dags/${fileName}?dagRunId=${dagRunToDisplay.rootDAGRunId}&subDAGRunId=${dagRunToDisplay.parentDAGRunId}&dagRunName=${encodeURIComponent(dagRunToDisplay.rootDAGRunName)}`}
                      onClick={handleParentDAGRunClick}
                      className="text-primary hover:text-primary hover:underline transition-colors font-medium"
                    >
                      {dagRunToDisplay.parentDAGRunName}
                    </a>
                    <span className="text-muted-foreground mx-1">/</span>
                  </>
                )}
            </nav>
          )}

          <div className="flex min-w-0 items-center gap-2">
            <h1 className="min-w-0 break-words text-2xl font-bold text-foreground sm:truncate">
              {dagRunToDisplay?.name || dag.name}
            </h1>
            {filePath && (
              <button
                onClick={copyFilePath}
                className="flex-shrink-0 inline-flex items-center gap-1 px-1.5 py-0.5 text-xs rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-all"
                title={`Copy file path: ${filePath}`}
              >
                {copiedPath ? (
                  <Check className="h-3.5 w-3.5 text-green-500" />
                ) : (
                  <Copy className="h-3.5 w-3.5" />
                )}
              </button>
            )}
          </div>
        </div>

        {showActions && (
          <div className="flex min-w-0 flex-wrap items-center gap-2 sm:flex-shrink-0 sm:justify-end">
            <DAGActions
              status={dagRunToDisplay}
              dag={dag}
              fileName={fileName}
              refresh={refreshFn}
              displayMode="compact"
              navigateToStatusTab={navigateToStatusTab}
            />
          </div>
        )}
      </div>

      {/* Status and metadata row */}
      {dagRunToDisplay &&
        dagRunToDisplay.status !== undefined &&
        dagRunToDisplay.status !== null && (
          <div className="flex min-w-0 flex-wrap items-center gap-2 text-sm">
            <StatusChip status={dagRunToDisplay.status} size="md">
              {dagRunToDisplay.statusLabel || ''}
            </StatusChip>
            <AutoRetryBadge
              status={dagRunToDisplay.status}
              count={dagRunToDisplay.autoRetryCount}
              limit={dagRunToDisplay.autoRetryLimit}
            />

            <button
              onClick={handleRefresh}
              disabled={isRefreshing}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs font-medium rounded-md text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed transition-all"
              title="Refresh (R)"
            >
              <RefreshCw
                className={`h-3 w-3 ${isRefreshing ? 'animate-spin' : ''}`}
              />
              <span>Refresh</span>
            </button>

            <span className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
              <Calendar className="h-3 w-3" />
              <span className="truncate">
                {dagRunToDisplay?.startedAt
                  ? dayjs(dagRunToDisplay.startedAt).format('MMM D, HH:mm:ss')
                  : '--'}
              </span>
            </span>

            <span className="flex items-center gap-1 text-xs text-muted-foreground">
              <Timer className="h-3 w-3" />
              {currentDuration}
              {isRunning && (
                <span className="inline-block w-1.5 h-1.5 rounded-full bg-[#66ff66]" />
              )}
            </span>

            {dagRunToDisplay.workerId && (
              <span className="flex items-center gap-1 text-xs text-muted-foreground">
                <Server className="h-3 w-3" />
                <span className="min-w-0 truncate font-mono">
                  {dagRunToDisplay.workerId}
                </span>
              </span>
            )}

            <code className="max-w-full break-all text-xs font-mono text-muted-foreground sm:max-w-xs sm:truncate">
              {dagRunToDisplay.rootDAGRunId}
            </code>
          </div>
        )}
    </div>
  );
};

export default DAGHeader;
