// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Calendar,
  FileText,
  RefreshCw,
  Server,
  SlidersHorizontal,
  Terminal,
  Timer,
} from 'lucide-react';
import React, { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { components, Status } from '../../../../api/v1/schema';
import dayjs from '../../../../lib/dayjs';
import StatusChip from '@/components/ui/status-chip';
import AutoRetryBadge from '../common/AutoRetryBadge';
import { DAGRunActions } from '../common';

interface DAGRunHeaderProps {
  dagRun: components['schemas']['DAGRunDetails'];
  refreshFn: () => void;
}

const DAGRunHeader: React.FC<DAGRunHeaderProps> = ({ dagRun, refreshFn }) => {
  const navigate = useNavigate();
  const [isRefreshing, setIsRefreshing] = React.useState(false);

  function formatDuration(startDate: string, endDate: string): string {
    if (!startDate || !endDate) return '--';

    const duration = dayjs.duration(dayjs(endDate).diff(dayjs(startDate)));
    const hours = Math.floor(duration.asHours());
    const minutes = duration.minutes();
    const seconds = duration.seconds();

    if (hours > 0) return `${hours}h ${minutes}m ${seconds}s`;
    if (minutes > 0) return `${minutes}m ${seconds}s`;
    return `${seconds}s`;
  }

  function getDurationDisplay(startedAt: string, finishedAt: string): string {
    if (finishedAt) {
      return formatDuration(startedAt, finishedAt);
    }
    if (startedAt) {
      return formatDuration(startedAt, dayjs().toISOString());
    }
    return '--';
  }

  const handleRootDAGRunClick = (e: React.MouseEvent) => {
    e.preventDefault();
    navigate(`/dag-runs/${dagRun.rootDAGRunName}/${dagRun.rootDAGRunId}`);
  };

  const handleParentDAGRunClick = (e: React.MouseEvent) => {
    e.preventDefault();
    if (dagRun.parentDAGRunId) {
      const searchParams = new URLSearchParams();
      searchParams.set('subDAGRunId', dagRun.parentDAGRunId);
      searchParams.set('dagRunId', dagRun.rootDAGRunId);
      searchParams.set('dagRunName', dagRun.rootDAGRunName);
      navigate(
        `/dag-runs/${dagRun.rootDAGRunName}/${dagRun.rootDAGRunId}?${searchParams.toString()}`
      );
    }
  };

  const handleRefresh = () => {
    setIsRefreshing(true);
    refreshFn();
    setTimeout(() => setIsRefreshing(false), 600);
  };

  // Add keyboard shortcut for refresh
  useEffect(() => {
    const handleKeyPress = (e: KeyboardEvent) => {
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
  }, [handleRefresh]);

  return (
    <div className="bg-card rounded-2xl p-6 border border-border shadow-sm">
      {/* Header with title and actions */}
      <div className="flex items-start justify-between gap-6 mb-4">
        <div className="flex-1 min-w-0">
          {/* Breadcrumb navigation */}
          <nav className="flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground mb-2">
            {dagRun.rootDAGRunId !== dagRun.dagRunId && (
              <>
                <a
                  href={`/dag-runs/${dagRun.rootDAGRunName}/${dagRun.rootDAGRunId}`}
                  onClick={handleRootDAGRunClick}
                  className="text-primary hover:text-primary hover:underline transition-colors font-medium"
                >
                  {dagRun.rootDAGRunName}
                </a>
                <span className="text-muted-foreground mx-1">/</span>
              </>
            )}

            {dagRun.parentDAGRunName &&
              dagRun.parentDAGRunId &&
              dagRun.parentDAGRunName !== dagRun.rootDAGRunName &&
              dagRun.parentDAGRunName !== dagRun.name && (
                <>
                  <a
                    href="#"
                    onClick={handleParentDAGRunClick}
                    className="text-primary hover:text-primary hover:underline transition-colors font-medium"
                  >
                    {dagRun.parentDAGRunName}
                  </a>
                  <span className="text-muted-foreground mx-1">/</span>
                </>
              )}
          </nav>

          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold text-foreground truncate">
              {dagRun.name}
            </h1>
            {dagRun.sourceFileName && (
              <a
                href={`/dags/${encodeURIComponent(dagRun.sourceFileName)}/`}
                onClick={(e) => {
                  e.preventDefault();
                  navigate(
                    `/dags/${encodeURIComponent(dagRun.sourceFileName!)}/`
                  );
                }}
                className="inline-flex items-center gap-1 px-2 py-1 text-xs font-medium rounded-md text-muted-foreground hover:text-foreground hover:bg-muted transition-all"
                title="View DAG Definition"
              >
                <FileText className="h-3.5 w-3.5" />
                <span>Definition</span>
              </a>
            )}
          </div>
        </div>
      </div>

      {/* Status and metadata row */}
      {dagRun.status != Status.NotStarted && (
        <div className="flex flex-wrap items-center gap-2 lg:gap-6">
          {/* Status, Refresh and actions */}
          <div className="flex items-center gap-3">
            {dagRun.status && (
              <StatusChip status={dagRun.status} size="md">
                {dagRun.statusLabel || ''}
              </StatusChip>
            )}
            <AutoRetryBadge
              status={dagRun.status}
              count={dagRun.autoRetryCount}
              limit={dagRun.autoRetryLimit}
            />
            <button
              onClick={handleRefresh}
              disabled={isRefreshing}
              className="relative group inline-flex items-center gap-1 px-2 py-1 text-xs font-medium rounded-md text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed transition-all"
              title="Refresh (R)"
            >
              <RefreshCw
                className={`h-3 w-3 ${isRefreshing ? 'animate-spin' : ''}`}
              />
              <span>Refresh</span>
              <span className="absolute -bottom-1 -right-1 bg-muted text-muted-foreground text-xs font-medium px-1 rounded-sm border opacity-0 group-hover:opacity-100 transition-opacity">
                R
              </span>
            </button>
            <DAGRunActions
              dagRun={dagRun}
              name={dagRun.name}
              refresh={refreshFn}
              displayMode="compact"
              isRootLevel={dagRun.rootDAGRunId === dagRun.dagRunId}
            />
          </div>

          {/* Metadata items */}
          <div className="flex flex-wrap items-center gap-4 lg:gap-6 text-sm">
            <div className="flex items-center gap-2 text-foreground bg-accent rounded-md px-3 py-1.5 border">
              <Calendar className="h-4 w-4 text-muted-foreground" />
              <span className="font-medium text-xs">
                {dagRun?.startedAt
                  ? `${dayjs(dagRun.startedAt).format('MMM D, HH:mm:ss')} ${dayjs(dagRun.startedAt).format('z')}`
                  : '--'}
              </span>
            </div>

            <div className="flex items-center gap-2 text-foreground bg-accent rounded-md px-3 py-1.5 border">
              <Timer className="h-4 w-4 text-muted-foreground" />
              <span className="font-medium text-xs">
                {getDurationDisplay(dagRun.startedAt, dagRun.finishedAt)}
              </span>
            </div>

            {dagRun.workerId && (
              <div className="flex items-center gap-2 text-foreground bg-accent rounded-md px-3 py-1.5 border">
                <Server className="h-4 w-4 text-muted-foreground" />
                <span className="font-medium text-xs font-mono">
                  {dagRun.workerId}
                </span>
              </div>
            )}

            {dagRun.profileName && (
              <div className="flex items-center gap-2 text-foreground bg-accent rounded-md px-3 py-1.5 border">
                <SlidersHorizontal className="h-4 w-4 text-muted-foreground" />
                <span className="font-medium text-xs font-mono">
                  {dagRun.profileName}
                </span>
              </div>
            )}

            <div className="flex items-center gap-2 text-muted-foreground ml-auto">
              <span className="font-medium text-xs text-muted-foreground uppercase tracking-wide">
                Run ID
              </span>
              <code className="bg-accent text-foreground px-3 py-1.5 rounded-md text-xs font-mono border">
                {dagRun.dagRunId}
              </code>
            </div>
          </div>
        </div>
      )}

      {/* Parameters - Show if present */}
      {dagRun.params && (
        <div className="mt-4 border-t border-border pt-4">
          <div className="flex items-center gap-2 mb-2">
            <Terminal className="h-4 w-4 text-muted-foreground" />
            <span className="text-xs font-semibold text-foreground/90">
              Parameters
            </span>
          </div>
          <div className="bg-accent rounded-md px-3 py-1.5 font-mono text-xs text-foreground max-h-[120px] overflow-y-auto border">
            {dagRun.params}
          </div>
        </div>
      )}
    </div>
  );
};

export default DAGRunHeader;
