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
import { components, NodeStatus, Stream } from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { RefreshButton } from '@/components/ui/refresh-button';
import { Spinner } from '@/components/ui/spinner';
import { Switch } from '@/components/ui/switch';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { downloadFromUrl } from '@/lib/download';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useUserPreferences } from '../../../../contexts/UserPreference';
import { useStepLogSSE } from '../../../../hooks/useStepLogSSE';
import {
  type ForeachLogTarget,
  isSubDAGRun as isSubDAGRunDetails,
  stepLogDownloadPath,
  useStepLogQuery,
} from '../../hooks/useStepLogQuery';
import { AnsiLine, stripAnsi } from '@/lib/ansi';
import { isActiveNodeStatus } from '../../../../lib/status-utils';
import LoadingIndicator from '@/components/ui/loading-indicator';
import LogPageSizeSelect from './LogPageSizeSelect';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { LogChunks } from './LogChunks';
import {
  awayKeys,
  distanceFromLiveEnd,
  lineAt,
  LogOrderToggle,
  useLogOrder,
} from './LogOrderToggle';
import { LogViewModeSwitch, type LogViewMode } from './LogViewModeSwitch';

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
  /** Status of the step whose log is shown, when no node describes it */
  status?: NodeStatus;
  /** Reads a body step log of a foreach item; stepName is then the body step */
  foreach?: ForeachLogTarget;
  followTail?: boolean;
  onFollowTailChange?: (following: boolean) => void;
  onSettled?: (stepName: string) => void;
  /** Reports the stream shown after the reader switches it in place. */
  onStreamShown?: (stream: Stream) => void;
  /** Offers the Page View switch (default true). Without it only the tail shows. */
  pageView?: boolean;
};

type ContentProps = Props & {
  /** Switches the shown stream; absent when the caller owns the stream. */
  onStreamChange?: (stream: Stream) => void;
};

/**
 * StepLog displays the log output for a specific step in a DAG run
 * Fetches log data from the API and refreshes every 30 seconds
 */
function StepLog(props: Props) {
  const remoteNode = useRemoteNode();
  // The viewer switches streams itself unless a caller that follows the tail,
  // such as Run progress, owns the stream. A new stream from the caller
  // replaces the viewer's choice in the same render, so the previous stream
  // is never read.
  const ownsStream = props.followTail !== undefined;
  const [selectedStream, setSelectedStream] = useState(
    props.stream ?? Stream.stdout
  );
  const [streamProp, setStreamProp] = useState(props.stream);
  if (streamProp !== props.stream) {
    setStreamProp(props.stream);
    setSelectedStream(props.stream ?? Stream.stdout);
  }
  const stream = ownsStream ? (props.stream ?? Stream.stdout) : selectedStream;
  const { onStreamShown } = props;
  const showStream = useCallback(
    (next: Stream) => {
      setSelectedStream(next);
      onStreamShown?.(next);
    },
    [onStreamShown]
  );
  // A log identity owns its search, cache and scroll position. The stream is
  // not part of it: a stream change swaps the content in place, so the viewer
  // never unmounts and the previous stream stays shown until the new one loads.
  const identity = JSON.stringify([
    remoteNode,
    props.dagName,
    props.dagRunId,
    props.stepName,
    props.dagRun?.rootDAGRunName,
    props.dagRun?.rootDAGRunId,
    props.foreach?.stepName,
    props.foreach?.item,
  ]);
  return (
    <StepLogContent
      key={identity}
      {...props}
      stream={stream}
      onStreamChange={ownsStream ? undefined : showStream}
    />
  );
}

function StepLogContent({
  dagName,
  dagRunId,
  stepName,
  dagRun,
  stream = Stream.stdout,
  node,
  status,
  foreach,
  followTail,
  onFollowTailChange,
  onSettled,
  onStreamChange,
  pageView = true,
}: ContentProps) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const config = useConfig();
  const { preferences, updatePreference } = useUserPreferences();
  const [viewMode, setViewMode] = useState<LogViewMode>('tail');
  const { newestFirst, toggle: toggleOrder } = useLogOrder();
  const [pageSize, setPageSize] = useState(SSE_TAIL_LINES);
  const [currentPage, setCurrentPage] = useState(1);
  const [pageInput, setPageInput] = useState<number | ''>(1);
  const [jumpToLine, setJumpToLine] = useState<number | ''>('');
  const [searchTerm, setSearchTerm] = useState('');
  const [activeMatch, setActiveMatch] = useState(0);
  const [navigationOpen, setNavigationOpen] = useState(false);
  const controlledFollowTail = followTail !== undefined;
  const showNavigation = !controlledFollowTail || navigationOpen;
  const isActive = isActiveNodeStatus(status ?? node?.status);

  const [localLiveMode, setLocalLiveMode] = useState(isActive);
  const [localFollowing, setLocalFollowing] = useState(true);
  const following = followTail ?? localFollowing;
  const liveMode = controlledFollowTail || localLiveMode;
  const [pausedData, setPausedData] = useState<LogWithPagination | null>(null);
  const wasActive = useRef(isActive);
  const wasFollowing = useRef(following);

  const [cachedData, setCachedData] = useState<LogWithPagination | null>(null);
  const [isNavigating, setIsNavigating] = useState(false);

  // A new stream reads from its own tail: the search, the pause and the page
  // of the previous stream end in the same render. The cached content stays,
  // so the log does not empty while the new stream loads.
  const [shownStream, setShownStream] = useState(stream);
  if (shownStream !== stream) {
    setShownStream(stream);
    setSearchTerm('');
    setActiveMatch(0);
    setJumpToLine('');
    setPausedData(null);
    setViewMode('tail');
    setCurrentPage(1);
    setLocalFollowing(true);
  }

  useEffect(() => {
    if (!isActive) {
      setLocalLiveMode(false);
    }
  }, [isActive]);

  const isInitialLoad = useRef(true);
  const navigationTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const logContainerRef = useRef<HTMLDivElement>(null);

  const isSubDAGRun = isSubDAGRunDetails(dagRun);

  // SSE supplies a fixed tail with stdout counts; other views use REST.
  // Foreach body logs have no SSE topic and always poll.
  const shouldUseSSE =
    viewMode === 'tail' &&
    liveMode &&
    isActive &&
    !isSubDAGRun &&
    !foreach &&
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
  const offset =
    viewMode === 'page' ? (currentPage - 1) * pageSize + 1 : undefined;
  const limit = viewMode === 'page' ? pageSize : undefined;

  // SWR options - poll only when SSE is not available
  const swrOptions = React.useMemo(
    () => ({
      refreshInterval: usePolling && liveMode && isActive ? 2000 : 0,
      keepPreviousData: false,
      revalidateOnFocus: false,
      dedupingInterval: 1000,
    }),
    [isActive, liveMode, usePolling]
  );

  const { data, isLoading, error, mutate } = useStepLogQuery(
    { dagName, dagRunId, stepName, dagRun, foreach },
    { stream, tail, offset, limit },
    swrOptions
  );

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
      content: sseResult.data.stdoutContent,
      lineCount: sseResult.data.lineCount,
      totalLines: sseResult.data.totalLines,
      hasMore: sseResult.data.hasMore,
    };
  }, [sseIsActive, sseResult.data]);
  const previousRestData = useRef(data);
  // Retain streamed output until a fresh REST response replaces it. A query
  // without data and without error is still loading its new stream or page:
  // the cached content stays until the response arrives. An error shows no
  // stale content, which belongs to another stream.
  const restData =
    data !== previousRestData.current
      ? data
      : data === undefined
        ? undefined
        : cachedData || data;
  // The content of the shown stream and page. The cache can still hold the
  // previous stream while the new one loads.
  const loadedData = sseLogData || restData;
  const latestData = loadedData || (error ? undefined : cachedData);
  const logData = pausedData || latestData;
  const hasNewOutput =
    !!pausedData && latestData?.content !== pausedData.content;
  // A pause freezes loaded content only: a pause while the cache stands in
  // freezes the new stream once it comes.
  const freezeWhenLoaded = useRef(false);

  function pauseFollowing(freeze = true): void {
    if (freeze && !pausedData) {
      if (loadedData) setPausedData(loadedData);
      else freezeWhenLoaded.current = true;
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
    setLocalLiveMode(true);
    onFollowTailChange?.(true);
  }

  useLayoutEffect(() => {
    if (
      viewMode === 'tail' &&
      !following &&
      (wasFollowing.current || freezeWhenLoaded.current) &&
      !pausedData
    ) {
      if (loadedData) setPausedData(loadedData);
      freezeWhenLoaded.current = !loadedData;
    }
    if (following) freezeWhenLoaded.current = false;
    if (following && !wasFollowing.current) {
      setPausedData(null);
      setSearchTerm('');
      setViewMode('tail');
      setCurrentPage(1);
    }
    wasFollowing.current = following;
  }, [following, pausedData, loadedData, viewMode]);

  // The live end of the log: the bottom when the oldest line is at the top,
  // the top when the newest line is.
  const scrollToLiveEnd = useCallback(() => {
    const container = logContainerRef.current;
    if (container) {
      container.scrollTop = newestFirst ? 0 : container.scrollHeight;
    }
  }, [newestFirst]);

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
      scrollToLiveEnd();
    }
  }, [logData?.content, following, viewMode, scrollToLiveEnd]);

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

  // Keep the page-jump input in sync with the applied page
  useEffect(() => {
    setPageInput(currentPage);
  }, [currentPage]);

  function handleViewModeChange(mode: LogViewMode): void {
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
    const endpoint = stepLogDownloadPath(config.apiURL, {
      dagName,
      dagRunId,
      stepName,
      dagRun,
      foreach,
    });

    const url = new URL(endpoint, window.location.origin);
    url.searchParams.set('remoteNode', remoteNode);
    url.searchParams.set('stream', stream);

    const fileStem = foreach
      ? `${dagName}-${dagRunId}-${foreach.stepName}-${foreach.item}-${stepName}`
      : `${dagName}-${dagRunId}-${stepName}`;
    try {
      await downloadFromUrl(url.toString(), `${fileStem}-${stream}.log`);
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
    foreach,
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

  function handlePageJump(): void {
    if (pageInput === '' || !Number.isFinite(pageInput)) {
      return;
    }
    const page = Math.min(Math.max(Math.floor(pageInput), 1), totalPages);
    setPageInput(page);
    handlePageChange(page);
  }

  function getLineNumber(index: number): number {
    switch (viewMode) {
      case 'tail':
        return Math.max(1, effectiveTotalLines - lines.length + 1) + index;
      case 'page':
        return (currentPage - 1) * pageSize + index + 1;
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-col">
      {/* One compact row; it wraps only when the width runs out. */}
      <div
        data-testid="step-log-toolbar"
        className="mb-1 flex shrink-0 flex-col gap-1 rounded bg-muted px-2 py-1"
      >
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {onStreamChange && (
            <ToggleGroup
              aria-label={ts('Stream')}
              className="max-w-full shrink-0 overflow-x-auto p-0.5"
            >
              {[Stream.stdout, Stream.stderr].map((option) => (
                <ToggleButton
                  key={option}
                  value={option}
                  groupValue={stream}
                  onClick={() => onStreamChange(option)}
                  className="h-6 px-2 font-mono"
                >
                  {option}
                </ToggleButton>
              ))}
            </ToggleGroup>
          )}
          <LogOrderToggle newestFirst={newestFirst} onToggle={toggleOrder} />
          {showNavigation && pageView && (
            <LogViewModeSwitch
              value={viewMode}
              onChange={handleViewModeChange}
              disabled={isNavigating}
            />
          )}

          {controlledFollowTail && (
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

          {showNavigation && (
            <LogPageSizeSelect
              pageSize={pageSize}
              disabled={isNavigating}
              onPageSizeChange={(size) => {
                setPausedData(null);
                if (viewMode === 'tail') {
                  resumeFollowing();
                }
                setPageSize(size);
                setCurrentPage(1);
              }}
            />
          )}

          {showNavigation && (
            <span className="whitespace-nowrap text-xs text-muted-foreground">
              <I18nText
                text="Showing {visible} of {total} lines"
                values={{ visible: lines.length, total: effectiveTotalLines }}
              />{' '}
              {isEstimate ? <I18nText text={'(estimated)'} /> : ''}{' '}
              {hasMore ? <I18nText text={'(more available)'} /> : ''}
            </span>
          )}

          {/* Search within loaded lines */}
          <div className="flex min-w-40 max-w-80 flex-1 basis-40 items-center gap-1">
            <Search
              className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
              aria-hidden="true"
            />
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
                className="h-7 min-w-0 flex-1 text-xs"
              />
            </I18nProps>
            {trimmedSearch && (
              <>
                <span className="whitespace-nowrap text-xs text-muted-foreground">
                  {matchIndexes.length === 0 ? (
                    <I18nText text={'No matches'} />
                  ) : (
                    `${Math.min(activeMatch + 1, matchIndexes.length)}/${matchIndexes.length}`
                  )}
                </span>
                <I18nProps>
                  <Button
                    size="icon-sm"
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
                    size="icon-sm"
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
                    size="icon-sm"
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

          {/* Jump to line controls. A jump opens a page: a tail-only log
              has none. */}
          {showNavigation && pageView && (
            <div className="flex items-center gap-1">
              <span className="hidden whitespace-nowrap text-xs text-muted-foreground xl:inline">
                <I18nText text={'Jump to line:'} />
              </span>
              <Input
                aria-label={ts('Jump to line:')}
                title={ts('Jump to line:')}
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
                className="h-7 w-20 text-xs"
                disabled={isNavigating}
              />
              <Button
                size="xs"
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

          {/* Display and download controls */}
          <div className="ml-auto flex shrink-0 items-center gap-2">
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
              <RefreshButton
                onRefresh={async () => {
                  if (mutate) {
                    await mutate();
                  }
                }}
                isLoading={isNavigating || isLoading}
                className="size-7"
                description="Reload logs"
              />
            </I18nProps>

            {/* Download button */}
            <I18nProps>
              <Button
                size="icon-sm"
                variant="outline"
                onClick={handleDownload}
                disabled={isNavigating}
                title="Download full log"
              >
                <Download className="h-4 w-4" />
              </Button>
            </I18nProps>

            {controlledFollowTail ? (
              <span role="status" className="text-xs text-muted-foreground">
                {hasNewOutput
                  ? ts('New output available')
                  : !following
                    ? ts('Reading output')
                    : isActive
                      ? ts('Live')
                      : ts('Finished')}
              </span>
            ) : null}
            {!controlledFollowTail && isActive ? (
              <Button
                size="sm"
                variant={localLiveMode ? 'primary' : 'default'}
                onClick={() => setLocalLiveMode(!localLiveMode)}
              >
                <span
                  className={`inline-block w-2 h-2 rounded-full ${localLiveMode ? 'bg-white animate-pulse' : 'bg-muted-foreground'}`}
                />
                <I18nText text={'LIVE'} />
              </Button>
            ) : null}
            {!controlledFollowTail && !following && (
              <Button size="sm" onClick={resumeFollowing}>
                <I18nText text="Back to live" />
              </Button>
            )}
          </div>
        </div>

        {/* Page navigation controls */}
        {showNavigation && viewMode === 'page' && effectiveTotalLines > 0 && (
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              onClick={() => handlePageChange(Math.max(1, currentPage - 1))}
              disabled={currentPage <= 1 || isNavigating}
            >
              <I18nText text="Previous page" />
            </Button>
            <span className="flex items-center gap-1 text-xs">
              <I18nText text={'Page'} />
              <Input
                aria-label={ts('Page')}
                type="number"
                min={1}
                max={totalPages}
                value={pageInput}
                onChange={(e) =>
                  setPageInput(
                    e.target.value === '' ? '' : Number(e.target.value)
                  )
                }
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !isNavigating) {
                    handlePageJump();
                  }
                }}
                className="w-16 h-6 px-1 text-xs"
                disabled={isNavigating}
              />
              <I18nText text="of {total}" values={{ total: totalPages }} />
            </span>
            <Button
              size="sm"
              onClick={handlePageJump}
              disabled={
                isNavigating ||
                pageInput === '' ||
                (pageInput as number) < 1 ||
                (pageInput as number) > totalPages
              }
            >
              <I18nText text={'Go'} />
            </Button>
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
          // Reading away from the live end pauses the follow.
          if (newestFirst ? event.deltaY > 0 : event.deltaY < 0) {
            pauseFollowing();
          }
        }}
        onPointerDown={() => pauseFollowing()}
        onTouchStart={() => pauseFollowing()}
        onKeyDown={(event) => {
          if (awayKeys(newestFirst).includes(event.key)) {
            pauseFollowing();
          }
        }}
        onScroll={(event) => {
          if (
            following &&
            distanceFromLiveEnd(event.currentTarget, newestFirst) >
              TAIL_THRESHOLD_PX
          ) {
            pauseFollowing();
          }
        }}
        className={`min-h-0 flex-1 overscroll-contain rounded-lg bg-muted pt-2 pr-4 pb-2 relative ${preferences.logWrap ? 'overflow-auto' : 'overflow-x-auto overflow-y-auto'}`}
      >
        {isNavigating && (
          <div className="absolute inset-0 bg-black/20 flex items-center justify-center z-10 pointer-events-none">
            <div className="bg-card rounded-lg p-2">
              <Spinner className="size-5 text-primary" />
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
          <LogChunks
            count={lines.length}
            renderLine={(row) => {
              const index = lineAt(row, lines.length, newestFirst);
              const line = lines[index];
              return (
                <div
                  key={row}
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
              );
            }}
          />
        </pre>
      </div>
    </div>
  );
}

export default StepLog;
