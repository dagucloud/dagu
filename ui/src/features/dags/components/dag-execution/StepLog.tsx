// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * StepLog component displays the execution log for a specific step in a DAG run.
 *
 * @module features/dags/components/dag-execution
 */
import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { ChevronDown, ChevronUp, Download, Search, X } from 'lucide-react';
import { components, Stream } from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ReloadButton } from '@/components/ui/reload-button';
import { Switch } from '@/components/ui/switch';
import { downloadFromUrl } from '@/lib/download';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useUserPreferences } from '../../../../contexts/UserPreference';
import { useQuery } from '../../../../hooks/api';
import { whenEnabled } from '../../../../hooks/queryUtils';
import { useStepLogSSE } from '../../../../hooks/useStepLogSSE';
import { AnsiLine, stripAnsi } from '@/lib/ansi';
import { isActiveNodeStatus } from '../../../../lib/status-utils';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

const TAIL_THRESHOLD_PX = 4;
const SSE_TAIL_LINES = 1000;

// Extended Log type with pagination fields
interface LogWithPagination {
  content: string;
  lineCount?: number;
  totalLines?: number;
  hasMore?: boolean;
  isEstimate?: boolean;
}

function calculateTotalPages(totalLines: number, pageSize: number): number {
  return Math.ceil(totalLines / pageSize);
}

/**
 * Props for the StepLog component
 */
type Props = {
  /** DAG name or fileName */
  dagName: string;
  /** DAG-run ID of the execution */
  dagRunId: string;
  /** Name of the step to display logs for */
  stepName: string;
  /** Full DAG-run details (optional) - used to determine if this is a sub DAG-run */
  dagRun?: components['schemas']['DAGRunDetails'];
  /** Whether to show stdout or stderr logs */
  stream?: Stream;
  /** Node information (optional) - contains repeated log files */
  node?: components['schemas']['Node'];
  followTail?: boolean;
  onFollowTailChange?: (following: boolean) => void;
  onSettled?: (stepName: string) => void;
};

/**
 * StepLog displays the log output for a specific step in a DAG run
 * Fetches log data from the API and refreshes every 30 seconds
 */
function StepLog(props: Props) {
  const remoteNode = useRemoteNode();
  // A log identity owns its search, cache and scroll position.
  const identity = JSON.stringify([
    remoteNode,
    props.dagName,
    props.dagRunId,
    props.stepName,
    props.stream,
    props.dagRun?.rootDAGRunName,
    props.dagRun?.rootDAGRunId,
  ]);
  return <StepLogContent key={identity} {...props} />;
}

function StepLogContent({
  dagName,
  dagRunId,
  stepName,
  dagRun,
  stream = Stream.stdout,
  node,
  followTail,
  onFollowTailChange,
  onSettled,
}: Props) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const config = useConfig();
  const { preferences, updatePreference } = useUserPreferences();
  const [viewMode, setViewMode] = useState<'tail' | 'head' | 'page'>('tail');
  const [pageSize, setPageSize] = useState(SSE_TAIL_LINES);
  const [currentPage, setCurrentPage] = useState(1);
  const [jumpToLine, setJumpToLine] = useState<number | ''>('');
  const [searchTerm, setSearchTerm] = useState('');
  const [activeMatch, setActiveMatch] = useState(0);
  const [navigationOpen, setNavigationOpen] = useState(false);
  const showNavigation = followTail === undefined || navigationOpen;
  const isActive = isActiveNodeStatus(node?.status);

  const [localFollowing, setLocalFollowing] = useState(true);
  const following = followTail ?? localFollowing;
  const [pausedData, setPausedData] = useState<LogWithPagination | null>(null);
  const wasActive = useRef(isActive);
  const wasFollowing = useRef(following);

  const [cachedData, setCachedData] = useState<LogWithPagination | null>(null);
  const [isNavigating, setIsNavigating] = useState(false);

  const isInitialLoad = useRef(true);
  const navigationTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const logContainerRef = useRef<HTMLDivElement>(null);

  const isSubDAGRun =
    dagRun &&
    dagRun.rootDAGRunId &&
    dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId;

  // SSE supplies a fixed tail with stdout counts; other views use REST.
  const shouldUseSSE =
    viewMode === 'tail' &&
    isActive &&
    !isSubDAGRun &&
    stream === Stream.stdout &&
    pageSize === SSE_TAIL_LINES;
  const sseResult = useStepLogSSE(
    dagName,
    dagRunId,
    stepName,
    shouldUseSSE,
    remoteNode
  );
  const sseIsActive =
    shouldUseSSE && sseResult.isConnected && !sseResult.shouldUseFallback;

  // Fall back to REST polling when SSE is not available or not connected
  const usePolling = !sseIsActive;

  const tail = viewMode === 'tail' ? pageSize : undefined;
  const head = viewMode === 'head' ? pageSize : undefined;
  const offset =
    viewMode === 'page' ? (currentPage - 1) * pageSize + 1 : undefined;
  const limit = viewMode === 'page' ? pageSize : undefined;

  // SWR options - poll only when SSE is not available
  const swrOptions = React.useMemo(
    () => ({
      refreshInterval: usePolling && isActive ? 2000 : 0,
      keepPreviousData: false,
      revalidateOnFocus: false,
      dedupingInterval: 1000,
    }),
    [isActive, usePolling]
  );

  const subDAGQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/log',
    whenEnabled(!!isSubDAGRun, {
      params: {
        query: {
          remoteNode,
          stream,
          tail,
          head,
          offset,
          limit,
        },
        path: {
          name: dagRun?.rootDAGRunName as string,
          dagRunId: dagRun?.rootDAGRunId as string,
          subDAGRunId: dagRun?.dagRunId as string,
          stepName,
        },
      },
    }),
    swrOptions
  );

  const dagRunQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log',
    whenEnabled(!isSubDAGRun, {
      params: {
        query: {
          remoteNode,
          stream,
          tail,
          head,
          offset,
          limit,
        },
        path: {
          name: dagName,
          dagRunId,
          stepName,
        },
      },
    }),
    swrOptions
  );

  const { data, isLoading, error, mutate } = isSubDAGRun
    ? subDAGQuery
    : dagRunQuery;

  useEffect(() => {
    const finished = !isActive && (wasActive.current || !!onSettled);
    wasActive.current = isActive;
    if (!finished) {
      return;
    }
    // Status can arrive before the final log event. Fetch once before advancing.
    let cancelled = false;
    void mutate()
      .then(() => {
        if (!cancelled) {
          onSettled?.(stepName);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [isActive, mutate, onSettled, stepName]);

  const sseLogData = React.useMemo<LogWithPagination | null>(() => {
    if (!sseIsActive || !sseResult.data) {
      return null;
    }
    return {
      content:
        stream === Stream.stdout
          ? sseResult.data.stdoutContent
          : sseResult.data.stderrContent,
      lineCount: sseResult.data.lineCount,
      totalLines: sseResult.data.totalLines,
      hasMore: sseResult.data.hasMore,
    };
  }, [sseIsActive, sseResult.data, stream]);
  const previousRestData = useRef(data);
  // Retain streamed output until a fresh REST response replaces it.
  const latestData =
    sseLogData ||
    (data !== previousRestData.current ? data : cachedData || data);
  const logData = pausedData || latestData;
  const hasNewOutput =
    !!pausedData && latestData?.content !== pausedData.content;

  function pauseFollowing(freeze = true): void {
    if (freeze && !pausedData && logData) {
      setPausedData(logData);
    }
    setLocalFollowing(false);
    onFollowTailChange?.(false);
  }

  function resumeFollowing(): void {
    setPausedData(null);
    setSearchTerm('');
    setViewMode('tail');
    setCurrentPage(1);
    setLocalFollowing(true);
    onFollowTailChange?.(true);
  }

  useLayoutEffect(() => {
    if (
      viewMode === 'tail' &&
      !following &&
      wasFollowing.current &&
      !pausedData &&
      latestData
    ) {
      setPausedData(latestData);
    }
    if (following && !wasFollowing.current) {
      setPausedData(null);
      setSearchTerm('');
      setViewMode('tail');
      setCurrentPage(1);
    }
    wasFollowing.current = following;
  }, [following, pausedData, latestData, viewMode]);

  const scrollToBottom = useCallback(() => {
    if (logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, []);

  // Handle data updates from either SSE or REST API
  useEffect(() => {
    const activeData = latestData;
    previousRestData.current = data;

    if (activeData) {
      setCachedData(activeData as LogWithPagination);
      setIsNavigating(false);
      isInitialLoad.current = false;

      if (navigationTimeoutRef.current) {
        clearTimeout(navigationTimeoutRef.current);
        navigationTimeoutRef.current = null;
      }

      return;
    }

    // Reset navigation state when loading completes without data
    if (!isLoading && cachedData && !isInitialLoad.current) {
      setIsNavigating(false);
    }
  }, [data, latestData, isLoading, cachedData]);

  useLayoutEffect(() => {
    if (following && viewMode === 'tail') {
      scrollToBottom();
    }
  }, [logData?.content, following, viewMode, scrollToBottom]);

  // Set navigating state when view parameters change (after initial load)
  useEffect(() => {
    if (isInitialLoad.current) return;

    setIsNavigating(true);

    if (navigationTimeoutRef.current) {
      clearTimeout(navigationTimeoutRef.current);
    }

    // Safety timeout to prevent stuck navigation state
    navigationTimeoutRef.current = setTimeout(() => {
      setIsNavigating(false);
      navigationTimeoutRef.current = null;
    }, 3000);

    return () => {
      if (navigationTimeoutRef.current) {
        clearTimeout(navigationTimeoutRef.current);
        navigationTimeoutRef.current = null;
      }
    };
  }, [viewMode, currentPage, pageSize]);

  function handleViewModeChange(mode: 'tail' | 'head' | 'page'): void {
    if (mode === 'tail') {
      resumeFollowing();
    } else {
      pauseFollowing(false);
      setPausedData(null);
    }
    setViewMode(mode);
    setCurrentPage(1);
  }

  function handlePageChange(newPage: number): void {
    pauseFollowing(false);
    setPausedData(null);
    setCurrentPage(newPage);
  }

  function handleJumpToLine(): void {
    if (
      jumpToLine === '' ||
      jumpToLine < 1 ||
      jumpToLine > (cachedData?.totalLines || 0)
    ) {
      return;
    }

    pauseFollowing(false);
    setPausedData(null);
    const lineNum = jumpToLine as number;
    const targetPage = Math.ceil(lineNum / pageSize);

    setCurrentPage(targetPage);
    setViewMode('page');

    // Scroll to the specific line after DOM update
    setTimeout(() => {
      const lineElements =
        logContainerRef.current?.querySelectorAll('[data-line-number]') || [];
      for (const element of lineElements) {
        const htmlElement = element as HTMLElement;
        const lineNumber = parseInt(
          htmlElement.getAttribute('data-line-number') || '0',
          10
        );
        if (lineNumber === lineNum) {
          htmlElement.scrollIntoView({
            behavior: window.matchMedia?.('(prefers-reduced-motion: reduce)')
              .matches
              ? 'instant'
              : 'smooth',
            block: 'center',
          });
          htmlElement.classList.add('bg-primary/20');
          setTimeout(() => {
            htmlElement.classList.remove('bg-primary/20');
          }, 2000);
          break;
        }
      }
    }, 500);
  }

  const handleDownload = useCallback(async () => {
    const endpoint = isSubDAGRun
      ? `${config.apiURL}/dag-runs/${dagRun?.rootDAGRunName}/${dagRun?.rootDAGRunId}/sub-dag-runs/${dagRun?.dagRunId}/steps/${stepName}/log/download`
      : `${config.apiURL}/dag-runs/${dagName}/${dagRunId}/steps/${stepName}/log/download`;

    const url = new URL(endpoint, window.location.origin);
    url.searchParams.set('remoteNode', remoteNode);
    url.searchParams.set('stream', stream);

    try {
      await downloadFromUrl(
        url.toString(),
        `${dagName}-${dagRunId}-${stepName}-${stream}.log`
      );
    } catch (err) {
      console.error('Download failed:', err);
    }
  }, [
    config.apiURL,
    dagName,
    dagRunId,
    stepName,
    stream,
    dagRun,
    isSubDAGRun,
    remoteNode,
  ]);

  const content = logData?.content || '';

  const lines = React.useMemo(() => {
    const rawLines = content ? content.split('\n') : [];
    return rawLines[rawLines.length - 1] === ''
      ? rawLines.slice(0, -1)
      : rawLines;
  }, [content]);

  const trimmedSearch = searchTerm.trim();
  const matchIndexes = React.useMemo(() => {
    if (!trimmedSearch) return [];
    const term = trimmedSearch.toLowerCase();
    return lines.reduce<number[]>((acc, line, index) => {
      if (stripAnsi(line).toLowerCase().includes(term)) acc.push(index);
      return acc;
    }, []);
  }, [lines, trimmedSearch]);

  if (isLoading && !cachedData && isInitialLoad.current) {
    return <LoadingIndicator />;
  }

  // Show error state (but not 404 since that means no log file exists yet)
  const isNotFoundError = error?.message?.includes('not found');
  if (error && !logData && !isNotFoundError) {
    return (
      <div className="w-full h-full flex items-center justify-center">
        <div className="text-error">
          <I18nText text={'Error loading log data:'} />{' '}
          {error.message || <I18nText text={'Unknown error'} />}
        </div>
      </div>
    );
  }

  const totalLines = logData?.totalLines || 0;
  const hasMore = logData?.hasMore || false;
  const isEstimate = logData?.isEstimate || false;
  const effectiveTotalLines =
    totalLines - lines.length <= 1 ? lines.length : totalLines;

  const totalPages = calculateTotalPages(effectiveTotalLines, pageSize);

  function scrollToMatch(matchPosition: number): void {
    const lineIndex = matchIndexes[matchPosition];
    if (lineIndex === undefined) return;
    const element = logContainerRef.current?.querySelector(
      `[data-log-index="${lineIndex}"]`
    ) as HTMLElement | null;
    if (!element) return;
    element.scrollIntoView({
      behavior: window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
        ? 'instant'
        : 'smooth',
      block: 'center',
    });
    element.classList.add('bg-primary/20');
    setTimeout(() => element.classList.remove('bg-primary/20'), 1200);
  }

  function goToMatch(direction: 1 | -1): void {
    if (matchIndexes.length === 0) return;
    const next =
      (activeMatch + direction + matchIndexes.length) % matchIndexes.length;
    setActiveMatch(next);
    scrollToMatch(next);
  }

  function getLineNumber(index: number): number {
    switch (viewMode) {
      case 'tail':
        return Math.max(1, effectiveTotalLines - lines.length + 1) + index;
      case 'head':
        return index + 1;
      case 'page':
        return (currentPage - 1) * pageSize + index + 1;
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-col">
      {/* Controls for log navigation */}
      <div className="mb-2 flex shrink-0 flex-col gap-2 rounded bg-muted p-2">
        <div className="flex flex-wrap items-center gap-2">
          {/* Responsive button container */}
          {showNavigation && (
            <div className="flex flex-wrap gap-1">
              <Button
                size="sm"
                variant={viewMode === 'tail' ? 'primary' : 'default'}
                onClick={() => handleViewModeChange('tail')}
                disabled={isNavigating}
              >
                <I18nText text={'Show End'} />
              </Button>
              <Button
                size="sm"
                variant={viewMode === 'head' ? 'primary' : 'default'}
                onClick={() => handleViewModeChange('head')}
                disabled={isNavigating}
              >
                <I18nText text={'Show Beginning'} />
              </Button>
              <Button
                size="sm"
                variant={viewMode === 'page' ? 'primary' : 'default'}
                onClick={() => handleViewModeChange('page')}
                disabled={isNavigating}
              >
                <I18nText text={'Page View'} />
              </Button>
            </div>
          )}

          {showNavigation && (
            <select
              aria-label={ts('Lines per page')}
              className="h-7 px-2 text-xs border border-border rounded-md bg-surface text-foreground flex-shrink-0 focus:outline-none focus:border-ring"
              value={pageSize}
              onChange={(e) => {
                setPausedData(null);
                if (viewMode === 'tail') {
                  resumeFollowing();
                }
                setPageSize(Number(e.target.value));
              }}
              disabled={isNavigating}
            >
              <option value="100">
                <I18nText text={'100 lines'} />
              </option>
              <option value="500">
                <I18nText text={'500 lines'} />
              </option>
              <option value="1000">
                <I18nText text={'1000 lines'} />
              </option>
              <option value="5000">
                <I18nText text={'5000 lines'} />
              </option>
              <option value="10000">
                <I18nText text={'10000 lines'} />
              </option>
            </select>
          )}

          {followTail !== undefined && (
            <Button
              size="sm"
              variant="ghost"
              aria-expanded={navigationOpen}
              onClick={() => setNavigationOpen(!navigationOpen)}
            >
              {ts('Log options')}
              <ChevronDown
                className={navigationOpen ? 'h-3 w-3 rotate-180' : 'h-3 w-3'}
                aria-hidden="true"
              />
            </Button>
          )}

          {/* Display and download controls */}
          <div className="ml-auto flex flex-wrap items-center gap-2">
            {/* Wrap toggle */}
            <div className="flex items-center gap-1.5">
              <span className="text-xs text-muted-foreground">
                <I18nText text={'Wrap'} />
              </span>
              <Switch
                aria-label={ts('Wrap')}
                checked={preferences.logWrap}
                onCheckedChange={(checked) =>
                  updatePreference('logWrap', checked)
                }
              />
            </div>

            {/* Reload button */}
            <I18nProps>
              <ReloadButton
                onReload={async () => {
                  if (mutate) {
                    await mutate();
                  }
                }}
                isLoading={isNavigating || isLoading}
                title="Reload logs"
              />
            </I18nProps>

            {/* Download button */}
            <I18nProps>
              <Button
                size="sm"
                variant="outline"
                onClick={handleDownload}
                disabled={isNavigating}
                title="Download full log"
              >
                <Download className="h-4 w-4" />
              </Button>
            </I18nProps>

            <span role="status" className="text-xs text-muted-foreground">
              {hasNewOutput
                ? ts('New output available')
                : !following
                  ? ts('Reading output')
                  : isActive
                    ? ts('Live')
                    : ts('Finished')}
            </span>
            {followTail === undefined && !following && (
              <Button size="sm" onClick={resumeFollowing}>
                <I18nText text="Back to live" />
              </Button>
            )}
          </div>
        </div>

        {/* Stats line - full width on mobile */}
        {showNavigation && (
          <div className="text-xs text-muted-foreground flex items-center">
            <I18nText
              text="Showing {visible} of {total} lines"
              values={{ visible: lines.length, total: effectiveTotalLines }}
            />{' '}
            {isEstimate ? <I18nText text={'(estimated)'} /> : ''}{' '}
            {hasMore ? <I18nText text={'(more available)'} /> : ''}
          </div>
        )}

        {/* Page navigation controls */}
        {showNavigation && viewMode === 'page' && effectiveTotalLines > 0 && (
          <div className="flex items-center gap-2 mt-2">
            <Button
              size="sm"
              onClick={() => handlePageChange(Math.max(1, currentPage - 1))}
              disabled={currentPage <= 1 || isNavigating}
            >
              <I18nText text="Previous page" />
            </Button>
            <span className="text-xs">
              <I18nText
                text="Page {current} of {total}"
                values={{ current: currentPage, total: totalPages }}
              />
            </span>
            <Button
              size="sm"
              onClick={() =>
                handlePageChange(Math.min(totalPages, currentPage + 1))
              }
              disabled={currentPage >= totalPages || isNavigating}
            >
              <I18nText text="Next page" />
            </Button>
          </div>
        )}

        {/* Search within loaded lines */}
        <div className="flex flex-wrap items-center gap-2">
          <Search className="h-3.5 w-3.5 text-muted-foreground" />
          <I18nProps>
            <Input
              type="text"
              placeholder="Search in loaded lines..."
              value={searchTerm}
              onFocus={() => pauseFollowing()}
              onChange={(e) => {
                pauseFollowing();
                setSearchTerm(e.target.value);
                setActiveMatch(0);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  goToMatch(e.shiftKey ? -1 : 1);
                } else if (e.key === 'Escape') {
                  setSearchTerm('');
                  setActiveMatch(0);
                }
              }}
              className="h-7 min-w-0 max-w-64 flex-1 text-xs"
            />
          </I18nProps>
          {trimmedSearch && (
            <>
              <span className="text-xs text-muted-foreground whitespace-nowrap">
                {matchIndexes.length === 0 ? (
                  <I18nText text={'No matches'} />
                ) : (
                  `${Math.min(activeMatch + 1, matchIndexes.length)}/${matchIndexes.length}`
                )}
              </span>
              <I18nProps>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => goToMatch(-1)}
                  disabled={matchIndexes.length === 0}
                  title="Previous match (Shift+Enter)"
                >
                  <ChevronUp className="h-3.5 w-3.5" />
                </Button>
              </I18nProps>
              <I18nProps>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => goToMatch(1)}
                  disabled={matchIndexes.length === 0}
                  title="Next match (Enter)"
                >
                  <ChevronDown className="h-3.5 w-3.5" />
                </Button>
              </I18nProps>
              <I18nProps>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    setSearchTerm('');
                    setActiveMatch(0);
                  }}
                  title="Clear search (Esc)"
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </I18nProps>
            </>
          )}
        </div>

        {/* Jump to line controls */}
        {showNavigation && (
          <div className="flex items-center gap-2 mt-2">
            <span className="text-xs text-muted-foreground">
              <I18nText text={'Jump to line:'} />
            </span>
            <Input
              aria-label={ts('Jump to line:')}
              type="number"
              min={1}
              max={effectiveTotalLines}
              value={jumpToLine}
              onChange={(e) =>
                setJumpToLine(
                  e.target.value === '' ? '' : Number(e.target.value)
                )
              }
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  if (
                    !isNavigating &&
                    jumpToLine !== '' &&
                    (jumpToLine as number) >= 1 &&
                    (jumpToLine as number) <= effectiveTotalLines
                  ) {
                    handleJumpToLine();
                  }
                }
              }}
              className="w-20 h-7 text-xs"
              disabled={isNavigating}
            />
            <Button
              size="sm"
              onClick={handleJumpToLine}
              disabled={
                isNavigating ||
                jumpToLine === '' ||
                (jumpToLine as number) < 1 ||
                (jumpToLine as number) > effectiveTotalLines
              }
            >
              <I18nText text={'Go'} />
            </Button>
          </div>
        )}
      </div>

      {(sseResult.error || error) && logData && (
        <p role="status" className="mb-2 text-xs text-warning">
          <I18nText text="Connection interrupted. Retrying..." />
        </p>
      )}
      {/* Log content with overlay loading indicator when navigating */}
      <div
        ref={logContainerRef}
        role="region"
        aria-label={ts('Step output')}
        tabIndex={0}
        onWheel={(event) => {
          if (event.deltaY < 0) {
            pauseFollowing();
          }
        }}
        onPointerDown={() => pauseFollowing()}
        onTouchStart={() => pauseFollowing()}
        onKeyDown={(event) => {
          if (['ArrowUp', 'PageUp', 'Home'].includes(event.key)) {
            pauseFollowing();
          }
        }}
        onScroll={(event) => {
          const element = event.currentTarget;
          if (
            following &&
            element.scrollHeight - element.clientHeight - element.scrollTop >
              TAIL_THRESHOLD_PX
          ) {
            pauseFollowing();
          }
        }}
        className={`min-h-0 flex-1 overscroll-contain rounded-lg bg-muted pt-4 pr-4 pb-4 relative ${preferences.logWrap ? 'overflow-auto' : 'overflow-x-auto overflow-y-auto'}`}
      >
        {isNavigating && (
          <div className="absolute inset-0 bg-black/20 flex items-center justify-center z-10 pointer-events-none">
            <div className="bg-card rounded-lg p-2">
              <div className="h-5 w-5 animate-spin rounded-full border-3 border-primary border-t-transparent"></div>
            </div>
          </div>
        )}
        {!content && (
          <p role="status" className="px-4 py-8 text-sm text-muted-foreground">
            {isActive ? ts('Waiting for output...') : ts('No output recorded.')}
          </p>
        )}
        <pre
          className={`font-mono text-sm text-foreground log-content ${preferences.logWrap ? '' : 'min-w-max'}`}
        >
          {lines.map((line, index) => (
            <div
              key={index}
              className="flex pr-2 py-0.5"
              data-log-index={index}
            >
              <span
                className="text-muted-foreground mr-4 select-none w-14 text-right flex-shrink-0 self-start sticky left-0 bg-muted pl-4 pr-2 z-10"
                data-line-number={getLineNumber(index)}
              >
                {getLineNumber(index)}
              </span>
              <span
                className={`flex-grow select-text cursor-text ${preferences.logWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'}`}
              >
                {line ? (
                  <AnsiLine
                    text={line}
                    highlight={trimmedSearch || undefined}
                  />
                ) : (
                  ' '
                )}
              </span>
            </div>
          ))}
        </pre>
      </div>
    </div>
  );
}

export default StepLog;
