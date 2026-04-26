/**
 * NodeStatusTableRow component renders a single row in the node status table.
 *
 * @module features/dags/components/dag-details
 */
import { Button } from '@/components/ui/button';
import { CommandDisplay } from '@/components/ui/command-display';
import { useErrorModal } from '@/components/ui/error-modal';
import { ScriptBadge } from '@/components/ui/script-dialog';
import { TableCell } from '@/components/ui/table';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import { getExecutorCommand } from '@/lib/executor-utils';
import { isHarnessStep } from '@/lib/harness-step';
import { isActiveNodeStatus } from '@/lib/status-utils';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  AlertCircle,
  ChevronDown,
  ChevronRight,
  Code,
  GitBranch,
  Play,
  RefreshCw,
  X,
} from 'lucide-react';
import { useContext, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  components,
  NodeStatus,
  Status,
  Stream,
} from '../../../../api/v1/schema';
import StyledTableRow from '@/components/ui/styled-table-row';
import { DAGContext } from '../../contexts/DAGContext';
import { NodeStatusChip } from '../common';
import { InlineLogViewer } from '../common/InlineLogViewer';
import StatusUpdateModal from '../dag-execution/StatusUpdateModal';
import HarnessStepSummary from './HarnessStepSummary';
import { SubDAGRunsList } from './SubDAGRunsList';
import PushBackHistory from '../common/PushBackHistory';

/**
 * Props for the NodeStatusTableRow component
 */
type Props = {
  /** Row number for display */
  rownum: number;
  /** Node data to display */
  node: components['schemas']['Node'];
  /** DAG file name */
  name: string;
  /** Function to open log viewer */
  onViewLog?: (
    stepName: string,
    dagRunId: string,
    node?: components['schemas']['Node']
  ) => void;
  /** Function called after this row's status update succeeds */
  onNodeStatusUpdated?: (stepName: string, status: NodeStatus) => void;
  /** Full dagRun details (optional) - used to determine if this is a sub dagRun */
  dagRun: components['schemas']['DAGRunDetails'];
  /** View mode: desktop or mobile */
  view?: 'desktop' | 'mobile';
};

/**
 * Format timestamp for better readability
 */
const formatTimestamp = (timestamp: string | undefined) => {
  if (!timestamp || timestamp == '-') return '-';
  try {
    return dayjs(timestamp).format('YYYY-MM-DD HH:mm:ss Z');
  } catch {
    return timestamp;
  }
};

/**
 * Calculate duration between two timestamps
 * If endTime is not provided, calculate duration from startTime to now (for running tasks)
 */
const calculateDuration = (
  startTime: string | undefined,
  endTime: string | undefined
) => {
  if (!startTime) return '-';

  try {
    const start = new Date(startTime).getTime();
    const end = endTime ? new Date(endTime).getTime() : new Date().getTime();

    if (isNaN(start) || isNaN(end)) return '-';

    const durationMs = end - start;

    // Format duration
    if (durationMs < 0) return '-';
    if (durationMs < 1000) return `${durationMs}ms`;

    const seconds = Math.floor(durationMs / 1000);
    if (seconds < 60) return `${seconds}s`;

    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    if (minutes < 60) return `${minutes}m ${remainingSeconds}s`;

    const hours = Math.floor(minutes / 60);
    const remainingMinutes = minutes % 60;
    return `${hours}h ${remainingMinutes}m ${remainingSeconds}s`;
  } catch {
    return '-';
  }
};

/**
 * NodeStatusTableRow displays information about a single node's execution status
 */
function NodeStatusTableRow({
  name,
  rownum,
  node,
  onViewLog,
  onNodeStatusUpdated,
  dagRun,
  view = 'desktop',
}: Props) {
  const { dagRunId, name: dagName } = dagRun;
  const navigate = useNavigate();
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const dagContext = useContext(DAGContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const { showError } = useErrorModal();
  // State to store the current duration for running tasks
  const [currentDuration, setCurrentDuration] = useState<string>('-');
  // State for expanding/collapsing parallel executions
  const [isExpanded, setIsExpanded] = useState(false);
  const [showDialog, setShowDialog] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  // State for inline log expansion
  const [isLogExpanded, setIsLogExpanded] = useState(false);
  const [activeLogTab, setActiveLogTab] = useState<'stdout' | 'stderr'>(
    'stdout'
  );
  // State for status update modal
  const [showStatusModal, setShowStatusModal] = useState(false);
  // Check if this is a sub dagRun node
  // Include both regular and repeated sub runs
  const allSubRuns = [...(node.subRuns || []), ...(node.subRunsRepeated || [])];
  // Use step.call OR fallback to first subRun's dagName (for chat tools, etc.)
  const subDagName = node.step.call || allSubRuns[0]?.dagName;
  const hasSubDAGRun = !!subDagName && allSubRuns.length > 0;
  const isActiveNode = isActiveNodeStatus(node.status);
  const activeDotClass =
    node.status === NodeStatus.Retrying ? 'bg-warning' : 'bg-success';

  // Update duration every second for active tasks.
  useEffect(() => {
    if (isActiveNode && node.startedAt) {
      // Initial calculation
      setCurrentDuration(calculateDuration(node.startedAt, node.finishedAt));

      // Set up interval to update duration every second
      const intervalId = setInterval(() => {
        setCurrentDuration(calculateDuration(node.startedAt, node.finishedAt));
      }, 1000);

      // Clean up interval on unmount or when status changes
      return () => clearInterval(intervalId);
    } else {
      // For non-running tasks, calculate once
      setCurrentDuration(calculateDuration(node.startedAt, node.finishedAt));
    }
  }, [isActiveNode, node.startedAt, node.finishedAt]);

  // Build URL for log viewing
  const searchParams = new URLSearchParams();
  searchParams.set('remoteNode', remoteNode);
  if (node.step) {
    searchParams.set('step', node.step.name);
  }
  if (dagRunId) {
    searchParams.set('dagRunId', dagRunId);
  }

  const url = `/dags/${name}/log?${searchParams.toString()}`;

  // Determine row highlight based on status
  const getRowHighlight = () => {
    switch (node.status) {
      case NodeStatus.Running:
        return 'bg-success-muted';
      case NodeStatus.Retrying:
        return 'bg-warning-muted';
      case NodeStatus.Failed:
        return 'bg-error-muted';
      case NodeStatus.Waiting:
        return 'bg-warning/5';
      default:
        return '';
    }
  };

  // Handle sub dagRun navigation
  const handleSubDAGRunNavigation = (
    subRunIndex: number = 0,
    e?: React.MouseEvent
  ) => {
    if (hasSubDAGRun && allSubRuns[subRunIndex]) {
      const subDAGRunId = allSubRuns[subRunIndex].dagRunId;

      // Check if we're in a dagRun context or a DAG context
      // More reliable detection by checking the current URL path or the dagRun object
      const currentPath = window.location.pathname;
      const isModal = document.querySelector('.dagRun-modal-content') !== null;
      const isDAGRunContext =
        dagRun && (currentPath.startsWith('/dag-runs/') || isModal);

      if (isDAGRunContext) {
        // For dagRuns, navigate to /dag-runs/{root-dag-name}/{root-dag-run-id}?subDAGRunId=...
        const searchParams = new URLSearchParams();
        searchParams.set('subDAGRunId', subDAGRunId);

        // Determine root DAG information
        let rootDAGRunId: string;
        let rootDAGName: string;

        if (dagRun && dagRun.rootDAGRunId) {
          // If this is already a sub dagRun, use its root information
          rootDAGRunId = dagRun.rootDAGRunId;
          rootDAGName = dagRun.rootDAGRunName;
          searchParams.set('dagRunId', dagRun.rootDAGRunId);
          searchParams.set('dagRunName', dagRun.rootDAGRunName);
        } else {
          // Otherwise, use the current dagRun as the root
          rootDAGRunId = dagRunId || '';
          rootDAGName = dagRun?.name || name;
          searchParams.set('dagRunId', dagRunId || '');
          searchParams.set('dagRunName', dagRun?.name || name);
        }

        searchParams.set('step', node.step.name);
        const url = `/dag-runs/${rootDAGName}/${rootDAGRunId}?${searchParams.toString()}`;

        // If Cmd/Ctrl key is pressed, open in new tab
        if (e && (e.metaKey || e.ctrlKey)) {
          window.open(url, '_blank');
        } else {
          navigate(url);
        }
      } else {
        // For DAGs, use the existing approach with query parameters
        const searchParams = new URLSearchParams();
        searchParams.set('subDAGRunId', subDAGRunId);

        // Use root dagRun information from the dagRun prop if available
        if (dagRun && dagRun.rootDAGRunId) {
          // If this is already a sub dagRun, use its root information
          searchParams.set('dagRunId', dagRun.rootDAGRunId);
        } else {
          // Otherwise, use the current dagRun as the root
          searchParams.set('dagRunId', dagRunId || '');
        }

        // Add dagRunName parameter to avoid waiting for DAG details
        // Use the root dagRun name or current dagRun name
        if (dagRun) {
          searchParams.set('dagRunName', dagRun.rootDAGRunName);
        }

        searchParams.set('step', node.step.name);
        const url = `/dags/${name}?${searchParams.toString()}`;

        // If Cmd/Ctrl key is pressed, open in new tab
        if (e && (e.metaKey || e.ctrlKey)) {
          window.open(url, '_blank');
        } else {
          navigate(url);
        }
      }
    }
  };

  // Handle log viewing
  const handleViewLog = (e: React.MouseEvent<HTMLAnchorElement>) => {
    // If Cmd (Mac) or Ctrl (Windows/Linux) key is pressed, let the default behavior happen
    // which will open the link in a new tab
    if (!(e.metaKey || e.ctrlKey) && onViewLog) {
      e.preventDefault();
      onViewLog(node.step.name, dagRunId || '', node);
    }
  };

  const handleRetry = async () => {
    setLoading(true);
    setError(null);
    try {
      await client.POST('/dag-runs/{name}/{dagRunId}/retry', {
        params: {
          path: { name: dagName, dagRunId },
          query: { remoteNode },
        },
        body: { dagRunId, stepName: node.step.name },
      });
      setSuccess(true);
      setShowDialog(false);
    } catch (e) {
      const error = e as { data?: { message?: string }; message?: string };
      setError(error?.data?.message || error.message || 'Retry failed');
    } finally {
      setLoading(false);
    }
  };

  // Handle status update
  const handleStatusUpdate = async (
    step: components['schemas']['Step'],
    status: NodeStatus
  ) => {
    // Check if this is a sub DAG-run
    const isSubDAGRun =
      dagRun.rootDAGRunId &&
      dagRun.rootDAGRunName &&
      dagRun.rootDAGRunId !== dagRun.dagRunId;

    // Define path parameters
    const pathParams = {
      name: isSubDAGRun ? dagRun.rootDAGRunName : dagName,
      dagRunId: isSubDAGRun ? dagRun.rootDAGRunId : dagRunId || '',
      stepName: step.name,
      ...(isSubDAGRun ? { subDAGRunId: dagRun.dagRunId } : {}),
    };

    // Use the appropriate endpoint
    const endpoint = isSubDAGRun
      ? '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/status'
      : '/dag-runs/{name}/{dagRunId}/steps/{stepName}/status';

    const { error } = await client.PATCH(endpoint, {
      params: {
        path: pathParams,
        query: {
          remoteNode,
        },
      },
      body: {
        status,
      },
    });

    if (error) {
      showError(
        error.message || 'Failed to update status',
        'Please try again or check the server connection.'
      );
      return;
    }

    onNodeStatusUpdated?.(step.name, status);
    dagContext.refresh();
    setShowStatusModal(false);
  };

  // Determine if logs are available
  const hasStdout = !!node.stdout;
  const hasStderr = !!node.stderr;
  const hasLogs = hasStdout || hasStderr;

  // Determine which stream to show based on active tab
  const currentStream: components['schemas']['Stream'] =
    activeLogTab === 'stderr' && hasStderr ? Stream.stderr : Stream.stdout;

  // Render desktop view (table row)
  if (view === 'desktop') {
    return (
      <>
        <StyledTableRow
          className={cn(
            'hover:bg-muted/50 transition-colors duration-200 h-auto cursor-pointer',
            getRowHighlight()
          )}
          onClick={() => {
            if (hasLogs) {
              setIsLogExpanded(!isLogExpanded);
              // Set default tab based on what's available
              if (!isLogExpanded) {
                setActiveLogTab(hasStdout ? 'stdout' : 'stderr');
              }
            }
          }}
        >
          <TableCell className="text-center py-2">
            <div className="flex items-center justify-center gap-2">
              {hasLogs && (
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    setIsLogExpanded(!isLogExpanded);
                    if (!isLogExpanded) {
                      setActiveLogTab(hasStdout ? 'stdout' : 'stderr');
                    }
                  }}
                  className="text-muted-foreground hover:text-foreground/90"
                >
                  {isLogExpanded ? (
                    <ChevronDown className="h-4 w-4" />
                  ) : (
                    <ChevronRight className="h-4 w-4" />
                  )}
                </button>
              )}
              <span className="font-semibold text-foreground/90 text-xs">
                {rownum}
              </span>
            </div>
          </TableCell>

          {/* Combined Step Name & Description */}
          <TableCell>
            <div className="space-y-0.5">
              <div className="text-sm font-semibold text-foreground text-wrap break-all flex items-center gap-1.5">
                {node.step.name}
                {hasSubDAGRun && (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="inline-flex items-center text-primary cursor-pointer">
                        <GitBranch className="h-4 w-4" />
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>
                      <span className="text-xs">Sub DAG Run: {subDagName}</span>
                    </TooltipContent>
                  </Tooltip>
                )}
              </div>
              {node.step.description && (
                <div className="text-xs text-muted-foreground leading-tight">
                  {node.step.description}
                </div>
              )}

              {/* Repeat Policy */}
              {node.step.repeatPolicy?.repeat && (
                <div className="flex items-start gap-1 mt-1">
                  <span
                    className={`inline-flex items-center gap-1 text-xs font-medium uppercase tracking-wider px-1.5 py-0.5 rounded ${
                      node.step.repeatPolicy.repeat === 'while'
                        ? 'bg-info-muted text-info'
                        : node.step.repeatPolicy.repeat === 'until'
                          ? 'bg-info-muted text-info'
                          : 'bg-primary/15 text-primary'
                    }`}
                  >
                    <RefreshCw className="h-2.5 w-2.5" />
                    {node.step.repeatPolicy.repeat === 'while'
                      ? 'WHILE'
                      : node.step.repeatPolicy.repeat === 'until'
                        ? 'UNTIL'
                        : 'REPEAT'}
                    {node.step.repeatPolicy.interval && (
                      <span className="opacity-75">
                        {node.step.repeatPolicy.interval}s
                      </span>
                    )}
                    {node.step.repeatPolicy.limit && (
                      <span className="opacity-75">
                        ×{node.step.repeatPolicy.limit}
                      </span>
                    )}
                  </span>

                  {node.step.repeatPolicy.condition && (
                    <span className="text-xs text-muted-foreground font-mono">
                      {node.step.repeatPolicy.condition.condition}
                      {node.step.repeatPolicy.condition.expected && (
                        <span className="text-success">
                          ={node.step.repeatPolicy.condition.expected}
                        </span>
                      )}
                    </span>
                  )}

                  {node.step.repeatPolicy.exitCode &&
                    node.step.repeatPolicy.exitCode.length > 0 && (
                      <span className="text-xs text-muted-foreground font-mono">
                        exit:[{node.step.repeatPolicy.exitCode.join(',')}]
                      </span>
                    )}
                </div>
              )}

              {hasSubDAGRun && (
                <SubDAGRunsList
                  dagName={dagRun.name}
                  dagRunId={dagRun.dagRunId}
                  rootDagName={dagRun.rootDAGRunName}
                  rootDagRunId={dagRun.rootDAGRunId || ''}
                  subDagName={subDagName || ''}
                  allSubRuns={allSubRuns}
                  isExpanded={isExpanded}
                  onToggleExpand={() => setIsExpanded(!isExpanded)}
                  onNavigate={handleSubDAGRunNavigation}
                />
              )}
            </div>
          </TableCell>

          {/* Combined Command & Args */}
          <TableCell>
            <div className="space-y-1.5">
              {isHarnessStep(node.step) ? (
                <HarnessStepSummary step={node.step} />
              ) : node.step.commands && node.step.commands.length > 0 ? (
                <CommandDisplay
                  commands={node.step.commands}
                  icon="code"
                  maxLength={50}
                />
              ) : (
                (() => {
                  const execCmd = getExecutorCommand(node.step);
                  if (execCmd) {
                    return (
                      <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
                        <Code className="h-3.5 w-3.5 text-primary flex-shrink-0" />
                        <span
                          className="font-mono text-xs truncate max-w-[200px]"
                          title={execCmd}
                        >
                          {execCmd.length > 50
                            ? execCmd.slice(0, 47) + '...'
                            : execCmd}
                        </span>
                      </div>
                    );
                  }
                  return null;
                })()
              )}
              {node.step.script && (
                <ScriptBadge
                  script={node.step.script}
                  stepName={node.step.name}
                />
              )}
            </div>
          </TableCell>

          {/* Last Run & Duration */}
          <TableCell>
            <div className="space-y-0.5">
              <div className="font-medium text-foreground/90 text-sm">
                {formatTimestamp(node.startedAt)}
              </div>
              {node.startedAt && (
                <div className="text-xs text-muted-foreground flex items-center gap-1.5 leading-tight">
                  <span className="font-medium flex items-center">
                    Duration:
                    {isActiveNode && (
                      <span
                        className={`inline-block w-2 h-2 rounded-full ml-1.5 animate-pulse ${activeDotClass}`}
                      />
                    )}
                  </span>
                  {currentDuration}
                </div>
              )}
              {/* Approval info */}
              {node.approvedBy && (
                <div className="text-xs text-muted-foreground leading-tight">
                  <span className="font-medium">Approved by:</span>{' '}
                  <span className="text-info">{node.approvedBy}</span>
                  {node.approvedAt && (
                    <span className="ml-1">
                      at {formatTimestamp(node.approvedAt)}
                    </span>
                  )}
                </div>
              )}
              {node.approvalInputs &&
                Object.keys(node.approvalInputs).length > 0 && (
                  <div className="text-xs text-muted-foreground leading-tight">
                    <span className="font-medium">Inputs:</span>{' '}
                    <span className="font-mono text-foreground/80">
                      {JSON.stringify(node.approvalInputs)}
                    </span>
                  </div>
                )}
              {node.pushBackHistory && node.pushBackHistory.length > 0 && (
                <PushBackHistory
                  history={node.pushBackHistory}
                  className="pt-1"
                />
              )}
              {/* Rejection info */}
              {node.rejectedBy && (
                <div className="text-xs text-muted-foreground leading-tight">
                  <span className="font-medium">Rejected by:</span>{' '}
                  <span className="text-error">{node.rejectedBy}</span>
                  {node.rejectedAt && (
                    <span className="ml-1">
                      at {formatTimestamp(node.rejectedAt)}
                    </span>
                  )}
                </div>
              )}
              {node.rejectionReason && (
                <div className="text-xs text-muted-foreground leading-tight">
                  <span className="font-medium">Reason:</span>{' '}
                  <span className="text-foreground/80">
                    {node.rejectionReason}
                  </span>
                </div>
              )}
            </div>
          </TableCell>

          {/* Status */}
          <TableCell className="text-center">
            <div
              onClick={(e) => {
                e.stopPropagation();
                setShowStatusModal(true);
              }}
              className="inline-block cursor-pointer"
              title="Click to update status"
            >
              <NodeStatusChip status={node.status} size="sm">
                {node.statusLabel}
              </NodeStatusChip>
            </div>
          </TableCell>

          {/* Error / Logs */}
          <TableCell>
            <div className="space-y-1.5">
              {/* Logs */}
              {(node.stdout || node.stderr) && (
                <div className="inline-flex items-center rounded-md overflow-hidden border border-border shadow-sm">
                  {/* stdout button */}
                  {node.stdout && (
                    <>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <a
                            href={url}
                            onClick={(e) => {
                              e.stopPropagation();
                              handleViewLog(e);
                            }}
                            className="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium transition-colors bg-card hover:bg-muted cursor-pointer"
                            title="View stdout log (Cmd/Ctrl+Click for new tab)"
                          >
                            <Code className="h-3.5 w-3.5 text-muted-foreground" />
                            <span>stdout</span>
                          </a>
                        </TooltipTrigger>
                        <TooltipContent>
                          <span className="text-xs">Standard Output Log</span>
                        </TooltipContent>
                      </Tooltip>
                      {node.stderr && <div className="w-px h-5 bg-border" />}
                    </>
                  )}

                  {/* stderr button */}
                  {node.stderr && (
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <a
                          href={`${url}&stream=stderr`}
                          onClick={(e) => {
                            e.stopPropagation();
                            if (!(e.metaKey || e.ctrlKey) && onViewLog) {
                              e.preventDefault();
                              onViewLog(
                                `${node.step.name}_stderr`,
                                dagRunId || ''
                              );
                            }
                          }}
                          className="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium transition-colors bg-card hover:bg-warning/10 cursor-pointer"
                          title="View stderr log (Cmd/Ctrl+Click for new tab)"
                        >
                          <AlertCircle className="h-3.5 w-3.5 text-warning" />
                          <span className="text-warning">stderr</span>
                        </a>
                      </TooltipTrigger>
                      <TooltipContent>
                        <span className="text-xs">Error Output Log</span>
                      </TooltipContent>
                    </Tooltip>
                  )}
                </div>
              )}

              {/* Errors - Simplified */}
              {node.error && (
                <div className="text-xs text-error leading-relaxed whitespace-normal break-words">
                  {node.error}
                </div>
              )}
              {node.step.preconditions?.some((cond) => cond.error) && (
                <div className="text-xs text-warning leading-relaxed">
                  Precondition unmet
                </div>
              )}
            </div>
          </TableCell>
          {dagRunId && (
            <TableCell className="text-center">
              <div className="flex items-center justify-center gap-1">
                {/* Retry button - hidden for Waiting and Rejected steps */}
                {node.status !== NodeStatus.Waiting &&
                  node.status !== NodeStatus.Rejected && (
                    <Button
                      size="icon-sm"
                      variant="secondary"
                      title="Retry from this step"
                      onClick={(e) => {
                        e.stopPropagation();
                        setShowDialog(true);
                      }}
                      disabled={loading || dagRun.status === Status.Running}
                    >
                      <Play className="h-4 w-4 text-success" />
                    </Button>
                  )}
              </div>
              <Dialog open={showDialog} onOpenChange={setShowDialog}>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Retry from this step?</DialogTitle>
                  </DialogHeader>
                  <div className="py-2 text-sm">
                    This will re-execute <b>{node.step.name}</b>. Are you sure?
                    {error && <div className="text-error mt-2">{error}</div>}
                    {success && (
                      <div className="text-success mt-2">Retry started!</div>
                    )}
                  </div>
                  <DialogFooter>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setShowDialog(false)}
                      disabled={loading}
                    >
                      <X className="h-4 w-4" />
                      Cancel
                    </Button>
                    <Button size="sm" onClick={handleRetry} disabled={loading}>
                      <Play className="h-4 w-4" />
                      {loading ? 'Retrying...' : 'Retry'}
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
            </TableCell>
          )}
        </StyledTableRow>

        {/* Inline log viewer row - spans entire table width */}
        {isLogExpanded && hasLogs && (
          <StyledTableRow className="bg-muted">
            <TableCell colSpan={dagRunId ? 7 : 6} className="p-3">
              <div className="w-full">
                {/* Header with tabs and expand button */}
                <div className="flex items-center justify-between mb-2">
                  {/* Simple tabs for out/err */}
                  {hasStdout && hasStderr ? (
                    <div className="flex items-center">
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setActiveLogTab('stdout');
                        }}
                        className={cn(
                          'px-3 py-1.5 text-xs font-medium transition-all border-b-2',
                          activeLogTab === 'stdout'
                            ? 'text-foreground border-primary'
                            : 'text-muted-foreground border-transparent hover:text-foreground'
                        )}
                      >
                        stdout
                      </button>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setActiveLogTab('stderr');
                        }}
                        className={cn(
                          'px-3 py-1.5 text-xs font-medium transition-all border-b-2',
                          activeLogTab === 'stderr'
                            ? 'text-foreground border-primary'
                            : 'text-muted-foreground border-transparent hover:text-foreground'
                        )}
                      >
                        stderr
                      </button>
                    </div>
                  ) : (
                    <div className="text-xs font-medium text-muted-foreground">
                      {hasStdout ? 'stdout' : 'stderr'}
                    </div>
                  )}

                  {/* Expand to modal button */}
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      if (onViewLog) {
                        onViewLog(
                          currentStream === 'stderr'
                            ? `${node.step.name}_stderr`
                            : node.step.name,
                          dagRunId || '',
                          node
                        );
                      }
                    }}
                    className="inline-flex items-center gap-1 px-2 py-1 text-xs font-medium text-primary hover:text-primary transition-colors"
                    title="Open in full modal"
                  >
                    <Code className="h-3 w-3" />
                    <span>Full view</span>
                  </button>
                </div>

                {/* Simple inline log viewer - no controls */}
                <InlineLogViewer
                  dagName={dagRun?.name || name}
                  dagRunId={dagRunId || ''}
                  stepName={node.step.name}
                  stream={currentStream}
                  dagRun={dagRun}
                />
              </div>
            </TableCell>
          </StyledTableRow>
        )}

        {/* Status Update Modal */}
        <StatusUpdateModal
          visible={showStatusModal}
          dismissModal={() => setShowStatusModal(false)}
          step={node.step}
          onSubmit={handleStatusUpdate}
        />
      </>
    );
  }

  // Render mobile view (card)
  return (
    <div
      className={cn(
        'p-4 rounded-2xl border border-border bg-card hover:',
        getRowHighlight()
      )}
    >
      {/* Header with number and status */}
      <div className="flex justify-between items-center mb-3">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-foreground/90 text-sm bg-muted rounded-full w-6 h-6 flex items-center justify-center">
            {rownum}
          </span>
          <h3 className="font-semibold text-foreground">
            {node.step.name}
            {hasSubDAGRun && (
              <span className="inline-flex items-center text-primary ml-1.5">
                <GitBranch className="h-4 w-4" />
              </span>
            )}
          </h3>
        </div>
        <NodeStatusChip status={node.status} size="sm">
          {node.statusLabel}
        </NodeStatusChip>
      </div>

      {/* Description */}
      {node.step.description && (
        <div className="text-xs text-muted-foreground mb-3">
          {node.step.description}
        </div>
      )}

      {/* Sub dagRun link */}
      {hasSubDAGRun && (
        <>
          {allSubRuns.length === 1 ? (
            // Single sub DAG run
            <>
              <div
                className="text-xs text-primary font-medium cursor-pointer hover:underline mb-1"
                onClick={(e) => handleSubDAGRunNavigation(0, e)}
              >
                View Sub DAG Run: {subDagName}
              </div>
              {allSubRuns[0]?.params && (
                <div className="text-xs text-muted-foreground mb-3">
                  Parameters:{' '}
                  <span className="font-mono">{allSubRuns[0].params}</span>
                </div>
              )}
            </>
          ) : (
            // Multiple sub DAG runs (parallel execution or repeated)
            <div className="mb-3">
              <SubDAGRunsList
                dagName={dagRun.name}
                dagRunId={dagRun.dagRunId}
                rootDagName={dagRun.rootDAGRunName}
                rootDagRunId={dagRun.rootDAGRunId || ''}
                subDagName={subDagName || ''}
                allSubRuns={allSubRuns}
                isExpanded={isExpanded}
                onToggleExpand={() => setIsExpanded(!isExpanded)}
                onNavigate={handleSubDAGRunNavigation}
              />
            </div>
          )}
        </>
      )}

      {/* Command section */}
      <div className="mb-3">
        <div className="text-xs font-medium text-foreground/90 mb-1">
          {isHarnessStep(node.step)
            ? 'Execution:'
            : node.step.commands && node.step.commands.length > 1
              ? 'Commands:'
              : 'Command:'}
        </div>
        <div className="space-y-1.5">
          {isHarnessStep(node.step) ? (
            <HarnessStepSummary step={node.step} />
          ) : node.step.commands && node.step.commands.length > 0 ? (
            <div className="space-y-1.5">
              {node.step.commands.map((entry, idx) => {
                const fullCmd =
                  entry.args && entry.args.length > 0
                    ? `${entry.command} ${entry.args.join(' ')}`
                    : entry.command;
                return (
                  <div key={idx} className="flex items-start gap-1.5 text-xs">
                    {node.step.commands && node.step.commands.length > 1 ? (
                      <span className="text-muted-foreground font-mono w-4 text-right flex-shrink-0 mt-1">
                        {idx + 1}.
                      </span>
                    ) : (
                      <Code className="h-4 w-4 text-primary flex-shrink-0 mt-0.5" />
                    )}
                    <code className="bg-muted rounded-md px-2 py-1 text-foreground/90 break-all whitespace-pre-wrap font-mono text-xs flex-1">
                      {fullCmd}
                    </code>
                  </div>
                );
              })}
            </div>
          ) : (
            // Executor-specific display for mobile
            (() => {
              const execCmd = getExecutorCommand(node.step);
              if (execCmd) {
                return (
                  <div className="flex items-start gap-1.5 text-xs">
                    <Code className="h-4 w-4 text-primary flex-shrink-0 mt-0.5" />
                    <code className="bg-muted rounded-md px-2 py-1 text-foreground/90 break-all whitespace-pre-wrap font-mono text-xs flex-1">
                      {execCmd}
                    </code>
                  </div>
                );
              }
              return null;
            })()
          )}

          {node.step.script && (
            <ScriptBadge script={node.step.script} stepName={node.step.name} />
          )}
        </div>
      </div>

      {/* Timing section */}
      <div className="mb-3">
        <div className="text-xs font-medium text-foreground/90 mb-1">
          Timing:
        </div>
        <div className="space-y-0.5">
          <div className="text-xs text-muted-foreground">
            Started: {formatTimestamp(node.startedAt)}
          </div>
          {node.startedAt && (
            <div className="text-xs text-muted-foreground flex items-center gap-1.5">
              <span className="font-medium flex items-center">
                Duration:
                {isActiveNode && (
                  <span
                    className={`inline-block w-2 h-2 rounded-full ml-1.5 animate-pulse ${activeDotClass}`}
                  />
                )}
              </span>
              {currentDuration}
            </div>
          )}
          {/* Approval info */}
          {node.approvedBy && (
            <div className="text-xs text-muted-foreground">
              <span className="font-medium">Approved by:</span>{' '}
              <span className="text-info">{node.approvedBy}</span>
              {node.approvedAt && (
                <span className="ml-1">
                  at {formatTimestamp(node.approvedAt)}
                </span>
              )}
            </div>
          )}
          {node.approvalInputs &&
            Object.keys(node.approvalInputs).length > 0 && (
              <div className="text-xs text-muted-foreground">
                <span className="font-medium">Inputs:</span>{' '}
                <span className="font-mono text-foreground/80">
                  {JSON.stringify(node.approvalInputs)}
                </span>
              </div>
            )}
          {node.pushBackHistory && node.pushBackHistory.length > 0 && (
            <PushBackHistory history={node.pushBackHistory} className="pt-1" />
          )}
          {/* Rejection info */}
          {node.rejectedBy && (
            <div className="text-xs text-muted-foreground">
              <span className="font-medium">Rejected by:</span>{' '}
              <span className="text-error">{node.rejectedBy}</span>
              {node.rejectedAt && (
                <span className="ml-1">
                  at {formatTimestamp(node.rejectedAt)}
                </span>
              )}
            </div>
          )}
          {node.rejectionReason && (
            <div className="text-xs text-muted-foreground">
              <span className="font-medium">Reason:</span>{' '}
              <span className="text-foreground/80">{node.rejectionReason}</span>
            </div>
          )}
        </div>
      </div>

      {/* Error section */}
      {(node.error || node.step.preconditions?.some((cond) => cond.error)) && (
        <div className="mb-3">
          <div className="text-xs font-medium text-foreground/90 mb-1">
            Errors:
          </div>

          {node.error && (
            <div className="text-xs bg-error-muted border border-error/20 rounded-md p-1.5 mb-2 whitespace-pre-wrap break-words text-error leading-tight">
              {node.error}
            </div>
          )}

          {node.step.preconditions?.some((cond) => cond.error) && (
            <div>
              <div className="text-xs font-medium text-warning mb-1">
                Precondition Unmet:
              </div>
              {node.step.preconditions
                .filter((cond) => cond.error)
                .map((cond, idx) => (
                  <div
                    key={idx}
                    className="text-xs bg-warning-muted border border-warning/20 rounded-md p-1.5 mb-1 whitespace-pre-wrap break-words text-warning leading-tight"
                  >
                    <div className="font-medium">
                      Condition: {cond.condition}
                    </div>
                    <div>Expected: {cond.expected}</div>
                    <div>Error: {cond.error}</div>
                  </div>
                ))}
            </div>
          )}
        </div>
      )}

      {/* Log buttons */}
      {(node.stdout || node.stderr) && (
        <div className="flex justify-end">
          <div className="inline-flex items-center rounded-md overflow-hidden border border-border shadow-sm">
            {/* stdout button */}
            {node.stdout && (
              <>
                <a
                  href={url}
                  onClick={handleViewLog}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors bg-card hover:bg-muted cursor-pointer"
                  title="View stdout log (Cmd/Ctrl+Click for new tab)"
                >
                  <Code className="h-3.5 w-3.5 text-muted-foreground" />
                  <span>stdout</span>
                </a>
                {node.stderr && <div className="w-px h-5 bg-border" />}
              </>
            )}

            {/* stderr button */}
            {node.stderr && (
              <a
                href={`${url}&stream=stderr`}
                onClick={(e) => {
                  if (!(e.metaKey || e.ctrlKey) && onViewLog) {
                    e.preventDefault();
                    onViewLog(`${node.step.name}_stderr`, dagRunId || '', node);
                  }
                }}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors bg-card hover:bg-warning/10 cursor-pointer"
                title="View stderr log (Cmd/Ctrl+Click for new tab)"
              >
                <AlertCircle className="h-3.5 w-3.5 text-warning" />
                <span className="text-warning">stderr</span>
              </a>
            )}
          </div>
        </div>
      )}

      {dagRunId && (
        <div className="flex justify-end mt-4 gap-2">
          {/* Retry button - hidden for Waiting and Rejected steps */}
          {node.status !== NodeStatus.Waiting &&
            node.status !== NodeStatus.Rejected && (
              <button
                className="p-2 rounded-full hover:bg-accent disabled:opacity-50 disabled:cursor-not-allowed"
                title="Retry from this step"
                onClick={() => setShowDialog(true)}
                disabled={loading || dagRun.status === Status.Running}
              >
                <Play className="h-6 w-6 text-success" />
              </button>
            )}
          <Dialog open={showDialog} onOpenChange={setShowDialog}>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Retry from this step?</DialogTitle>
              </DialogHeader>
              <div className="py-2 text-sm">
                This will re-execute <b>{node.step.name}</b>. Are you sure?
                {error && <div className="text-error mt-2">{error}</div>}
                {success && (
                  <div className="text-success mt-2">Retry started!</div>
                )}
              </div>
              <DialogFooter>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setShowDialog(false)}
                  disabled={loading}
                >
                  <X className="h-4 w-4" />
                  Cancel
                </Button>
                <Button size="sm" onClick={handleRetry} disabled={loading}>
                  <Play className="h-4 w-4" />
                  {loading ? 'Retrying...' : 'Retry'}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      )}
    </div>
  );
}

export default NodeStatusTableRow;
