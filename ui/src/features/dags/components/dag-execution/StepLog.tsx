/**
 * StepLog component displays the execution log for a specific step in a DAG run.
 *
 * @module features/dags/components/dag-execution
 */
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Download } from 'lucide-react';
import { components, NodeStatus, Stream } from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ReloadButton } from '@/components/ui/reload-button';
import { Switch } from '@/components/ui/switch';
import { TOKEN_KEY } from '../../../../contexts/AuthContext';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useUserPreferences } from '../../../../contexts/UserPreference';
import { useQuery } from '../../../../hooks/api';
import { whenEnabled } from '../../../../hooks/queryUtils';
import { useStepLogSSE } from '../../../../hooks/useStepLogSSE';
import { isActiveNodeStatus } from '../../../../lib/status-utils';
import LoadingIndicator from '@/components/ui/loading-indicator';

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
};

/**
 * Regular expression to match ANSI color codes for removal
 * Credit: https://github.com/chalk/ansi-regex/commit/02fa893d619d3da85411acc8fd4e2eea0e95a9d9 under MIT license
 */
const ANSI_CODES_REGEX = [
  '[\\u001B\\u009B][[\\]()#;?]*(?:(?:(?:(?:;[-a-zA-Z\\d\\/#&.:=?%@~_]+)*|[a-zA-Z\\d]+(?:;[-a-zA-Z\\d\\/#&.:=?%@~_]*)*)?\\u0007)',
  '(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PR-TZcf-nq-uy=><~]))',
].join('|');

/**
 * StepLog displays the log output for a specific step in a DAG run
 * Fetches log data from the API and refreshes every 30 seconds
 */
function StepLog({
  dagName,
  dagRunId,
  stepName,
  dagRun,
  stream = Stream.stdout,
  node,
}: Props) {
  const remoteNode = useRemoteNode();
  const config = useConfig();
  const { preferences, updatePreference } = useUserPreferences();
  const [viewMode, setViewMode] = useState<'tail' | 'head' | 'page'>('tail');
  const [pageSize, setPageSize] = useState(1000);
  const [currentPage, setCurrentPage] = useState(1);
  const [jumpToLine, setJumpToLine] = useState<number | ''>('');
  const isActive = isActiveNodeStatus(node?.status);

  const [isLiveMode, setIsLiveMode] = useState(isActive);

  useEffect(() => {
    if (!isActive) {
      setIsLiveMode(false);
    }
  }, [isActive]);

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

  // SSE is used for real-time updates when viewing tail of an active step (not sub-DAG runs)
  const shouldUseSSE =
    viewMode === 'tail' && isLiveMode && isActive && !isSubDAGRun;
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
      refreshInterval: usePolling && isLiveMode && isActive ? 2000 : 0,
      keepPreviousData: true,
      revalidateOnFocus: false,
      dedupingInterval: 1000,
    }),
    [isActive, isLiveMode, usePolling]
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

  // Transform SSE data to LogWithPagination format when available
  const sseLogData: LogWithPagination | null =
    sseIsActive && sseResult.data
      ? {
          content:
            stream === Stream.stdout
              ? sseResult.data.stdoutContent
              : sseResult.data.stderrContent,
          lineCount: sseResult.data.lineCount,
          totalLines: sseResult.data.totalLines,
          hasMore: sseResult.data.hasMore,
        }
      : null;

  const scrollToBottom = useCallback(() => {
    if (logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, []);

  // Handle data updates from either SSE or REST API
  useEffect(() => {
    const activeData = sseLogData || data;

    if (activeData) {
      setCachedData(activeData as LogWithPagination);
      setIsNavigating(false);
      isInitialLoad.current = false;

      if (navigationTimeoutRef.current) {
        clearTimeout(navigationTimeoutRef.current);
        navigationTimeoutRef.current = null;
      }

      if (viewMode === 'tail') {
        setTimeout(scrollToBottom, 100);
      }
      return;
    }

    // Reset navigation state when loading completes without data
    if (!isLoading && cachedData && !isInitialLoad.current) {
      setIsNavigating(false);
    }
  }, [data, sseLogData, isLoading, cachedData, viewMode, scrollToBottom]);

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
    if (mode === viewMode) return;
    setViewMode(mode);
    setCurrentPage(1);
  }

  function handlePageChange(newPage: number): void {
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

    const lineNum = jumpToLine as number;
    const targetPage = Math.ceil(lineNum / pageSize);

    setCurrentPage(targetPage);
    setViewMode('page');

    // Scroll to the specific line after DOM update
    setTimeout(() => {
      const lineElements = document.querySelectorAll('[data-line-number]');
      for (const element of lineElements) {
        const htmlElement = element as HTMLElement;
        const lineNumber = parseInt(
          htmlElement.getAttribute('data-line-number') || '0',
          10
        );
        if (lineNumber === lineNum) {
          htmlElement.scrollIntoView({ behavior: 'smooth', block: 'center' });
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
    const token = localStorage.getItem(TOKEN_KEY);
    const endpoint = isSubDAGRun
      ? `${config.apiURL}/dag-runs/${dagRun?.rootDAGRunName}/${dagRun?.rootDAGRunId}/sub-dag-runs/${dagRun?.dagRunId}/steps/${stepName}/log/download`
      : `${config.apiURL}/dag-runs/${dagName}/${dagRunId}/steps/${stepName}/log/download`;

    const url = new URL(endpoint, window.location.origin);
    url.searchParams.set('remoteNode', remoteNode);
    url.searchParams.set('stream', stream);

    try {
      const response = await fetch(url.toString(), {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });

      if (!response.ok) {
        throw new Error(`Download failed: ${response.statusText}`);
      }

      const blob = await response.blob();
      const filename =
        response.headers
          .get('Content-Disposition')
          ?.match(/filename="(.+)"/)?.[1] ||
        `${dagName}-${dagRunId}-${stepName}-${stream}.log`;

      const link = document.createElement('a');
      link.href = URL.createObjectURL(blob);
      link.download = filename;
      link.click();
      URL.revokeObjectURL(link.href);
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

  if (isLoading && !cachedData && isInitialLoad.current) {
    return <LoadingIndicator />;
  }

  // Prioritize SSE data, then REST data, then cached data
  const logData = (sseLogData || data || cachedData) as LogWithPagination;

  // Show error state (but not 404 since that means no log file exists yet)
  const isNotFoundError = error?.message?.includes('not found');
  if (error && !logData && !isNotFoundError) {
    return (
      <div className="w-full h-full flex items-center justify-center">
        <div className="text-error">
          Error loading log data: {error.message || 'Unknown error'}
        </div>
      </div>
    );
  }

  const content =
    logData?.content.replace(new RegExp(ANSI_CODES_REGEX, 'g'), '') || '';
  const totalLines = logData?.totalLines || 0;
  const hasMore = logData?.hasMore || false;
  const isEstimate = logData?.isEstimate || false;

  const rawLines = content ? content.split('\n') : ['<No log output>'];
  const lines =
    rawLines[rawLines.length - 1] === '' ? rawLines.slice(0, -1) : rawLines;
  const effectiveTotalLines =
    totalLines - lines.length <= 1 ? lines.length : totalLines;

  const totalPages = calculateTotalPages(effectiveTotalLines, pageSize);

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
    <div className="w-full h-full flex flex-col">
      {/* Controls for log navigation */}
      <div className="flex flex-col gap-2 mb-2 p-4 bg-muted rounded">
        <div className="flex flex-wrap items-center gap-2">
          {/* Responsive button container */}
          <div className="flex flex-wrap gap-1">
            <Button
              size="sm"
              variant={viewMode === 'tail' ? 'primary' : 'default'}
              onClick={() => handleViewModeChange('tail')}
              disabled={isNavigating}
            >
              Show End
            </Button>
            <Button
              size="sm"
              variant={viewMode === 'head' ? 'primary' : 'default'}
              onClick={() => handleViewModeChange('head')}
              disabled={isNavigating}
            >
              Show Beginning
            </Button>
            <Button
              size="sm"
              variant={viewMode === 'page' ? 'primary' : 'default'}
              onClick={() => handleViewModeChange('page')}
              disabled={isNavigating}
            >
              Page View
            </Button>
          </div>

          <select
            className="h-7 px-2 text-xs border border-border rounded-md bg-surface text-foreground flex-shrink-0 focus:outline-none focus:border-ring"
            value={pageSize}
            onChange={(e) => setPageSize(Number(e.target.value))}
            disabled={isNavigating}
          >
            <option value="100">100 lines</option>
            <option value="500">500 lines</option>
            <option value="1000">1000 lines</option>
            <option value="5000">5000 lines</option>
            <option value="10000">10000 lines</option>
          </select>

          {/* Wrap toggle, Live mode toggle and reload button */}
          <div className="flex items-center gap-2 ml-auto">
            {/* Wrap toggle */}
            <div className="flex items-center gap-1.5">
              <span className="text-xs text-muted-foreground">Wrap</span>
              <Switch
                checked={preferences.logWrap}
                onCheckedChange={(checked) =>
                  updatePreference('logWrap', checked)
                }
              />
            </div>

            {/* Reload button */}
            <ReloadButton
              onReload={async () => {
                if (mutate) {
                  await mutate();
                }
              }}
              isLoading={isNavigating || isLoading}
              title="Reload logs"
            />

            {/* Download button */}
            <Button
              size="sm"
              variant="outline"
              onClick={handleDownload}
              disabled={isNavigating}
              title="Download full log"
            >
              <Download className="h-4 w-4" />
            </Button>

            {/* Live mode toggle - only show when the node is active */}
            {isActive && (
              <Button
                size="sm"
                variant={isLiveMode ? 'primary' : 'default'}
                onClick={() => setIsLiveMode(!isLiveMode)}
              >
                <span
                  className={`inline-block w-2 h-2 rounded-full ${isLiveMode ? 'bg-white animate-pulse' : 'bg-muted-foreground'}`}
                />
                LIVE
              </Button>
            )}
          </div>
        </div>

        {/* Stats line - full width on mobile */}
        <div className="text-xs text-muted-foreground flex items-center">
          Showing {lines.length} of {effectiveTotalLines} lines{' '}
          {isEstimate ? '(estimated)' : ''} {hasMore ? '(more available)' : ''}
        </div>

        {/* Page navigation controls */}
        {viewMode === 'page' && effectiveTotalLines > 0 && (
          <div className="flex items-center gap-2 mt-2">
            <Button
              size="sm"
              onClick={() => handlePageChange(Math.max(1, currentPage - 1))}
              disabled={currentPage <= 1 || isNavigating}
            >
              Previous
            </Button>
            <span className="text-xs">
              Page {currentPage} of {totalPages}
            </span>
            <Button
              size="sm"
              onClick={() =>
                handlePageChange(Math.min(totalPages, currentPage + 1))
              }
              disabled={currentPage >= totalPages || isNavigating}
            >
              Next
            </Button>
          </div>
        )}

        {/* Jump to line controls */}
        <div className="flex items-center gap-2 mt-2">
          <span className="text-xs text-muted-foreground">Jump to line:</span>
          <Input
            type="number"
            min={1}
            max={effectiveTotalLines}
            value={jumpToLine}
            onChange={(e) =>
              setJumpToLine(e.target.value === '' ? '' : Number(e.target.value))
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
            Go
          </Button>
        </div>
      </div>

      {/* Log content with overlay loading indicator when navigating */}
      <div
        ref={logContainerRef}
        className={`flex-1 rounded-lg bg-muted pt-4 pr-4 pb-4 relative ${preferences.logWrap ? 'overflow-auto' : 'overflow-x-auto overflow-y-auto'}`}
      >
        {isNavigating && (
          <div className="absolute inset-0 bg-black bg-opacity-20 flex items-center justify-center z-10 pointer-events-none">
            <div className="bg-card rounded-lg p-2">
              <div className="h-5 w-5 animate-spin rounded-full border-3 border-primary border-t-transparent"></div>
            </div>
          </div>
        )}
        <pre
          className={`h-full font-mono text-sm text-foreground log-content ${preferences.logWrap ? '' : 'min-w-max'}`}
        >
          {lines.map((line, index) => (
            <div key={index} className="flex pr-2 py-0.5">
              <span
                className="text-muted-foreground mr-4 select-none w-14 text-right flex-shrink-0 self-start sticky left-0 bg-muted pl-4 pr-2 z-10"
                data-line-number={getLineNumber(index)}
              >
                {getLineNumber(index)}
              </span>
              <span
                className={`flex-grow select-text cursor-text ${preferences.logWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'}`}
              >
                {line || ' '}
              </span>
            </div>
          ))}
        </pre>
      </div>
    </div>
  );
}

export default StepLog;
