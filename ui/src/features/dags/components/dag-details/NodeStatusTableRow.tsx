// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * NodeStatusTableRow component renders a single row in the node status table.
 *
 * @module features/dags/components/dag-details
 */
import { Button } from '@/components/ui/button';
import { CommandDisplay } from '@/components/ui/command-display';
import { useErrorModal } from '@/components/ui/error-modal';
import RelativeTime from '@/components/ui/relative-time';
import { ScriptBadge } from '@/components/ui/script-dialog';
import { TableCell } from '@/components/ui/table';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useClock } from '@/hooks/useClock';
import {
  formatLogStepOutput,
  getExecutorCommand,
  getLogStepMessage,
} from '@/lib/executor-utils';
import { isHarnessStep } from '@/lib/harness-step';
import { isActiveNodeStatus } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  AlertCircle,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Code,
  EllipsisVertical,
  GitBranch,
  Play,
  RefreshCw,
} from 'lucide-react';
import { useContext, useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  buildDAGPageURL,
  buildDAGRunPageURL,
} from '../../../dag-runs/lib/dagRunUrls';
import { DAGRunContext } from '../../../dag-runs/contexts/DAGRunContext';
import {
  components,
  NodeStatus,
  Status,
  Stream,
} from '../../../../api/v1/schema';
import StyledTableRow from '@/components/ui/styled-table-row';
import { DAGContext } from '../../contexts/DAGContext';
import NodeStatusChip from '../common/NodeStatusChip';
import { useOpenSubRun } from '../common/SubRunStackModal';
import { InlineLogViewer } from '../common/InlineLogViewer';
import { StepRetryDialog } from '../common/StepRetryDialog';
import { canChangeStepStatus, canRetryStep } from '../../lib/stepStatusGate';
import StatusUpdateModal from '../dag-execution/StatusUpdateModal';
import HarnessStepSummary from './HarnessStepSummary';
import { LogStepMessage } from './LogStepMessage';
import { SubDAGRunsList } from './SubDAGRunsList';
import PushBackHistory from '../common/PushBackHistory';
import { ManualActionSubject } from '../common/ManualActionSubject';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

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
  /** Hide the actions menu for nodes the step APIs cannot address */
  hideActions?: boolean;
  /** Whether the inline log starts expanded */
  defaultLogExpanded?: boolean;
  /** Whether this step's details are open */
  selected?: boolean;
  /** Opens this step's details; without it a row click toggles the log */
  onInspect?: (stepName: string) => void;
};

type BuildExecution = components['schemas']['BuildExecution'];

function BuildDecisionBadge({
  build,
  remoteNode,
}: {
  build?: BuildExecution;
  remoteNode: string;
}) {
  if (!build) return null;

  const label =
    build.decision === 'reuse'
      ? 'reused'
      : build.decision === 'always'
        ? 'always run'
        : build.decision;
  const producer = build.producerRun;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn(
            'inline-flex rounded-full border px-2 py-0.5 text-[11px] font-medium',
            build.decision === 'reuse'
              ? 'border-success/30 bg-success/10 text-success'
              : 'border-border bg-muted text-muted-foreground'
          )}
        >
          {label}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-80 space-y-1 text-xs">
        <div className="font-medium">{build.reason.replace(/_/g, ' ')}</div>
        {build.decision === 'reuse' && (
          <div>
            <I18nText text={'No executor ran.'} />
          </div>
        )}
        {build.detail && <div>{build.detail}</div>}
        {producer?.name && producer.id && (
          <Link
            className="block underline underline-offset-2"
            to={buildDAGRunPageURL({
              rootDAGRunName: producer.name,
              rootDAGRunId: producer.id,
              remoteNode,
            })}
            onClick={(event) => event.stopPropagation()}
          >
            <I18nText text={'Produced by'} /> {producer.name}:{producer.id}
          </Link>
        )}
      </TooltipContent>
    </Tooltip>
  );
}

/** Shows how many times a step was retried, if it was. */
function RetryCountBadge({ count }: { count: number }) {
  if (count <= 0) return null;

  return (
    <span className="inline-flex rounded-full border border-warning/30 bg-warning/10 px-2 py-0.5 text-[11px] font-medium text-warning">
      <I18nText text={'Retried {count}×'} values={{ count }} />
    </span>
  );
}

/**
 * Calculate duration between two timestamps
 * If endTime is not provided, calculate duration from startTime to now (for running tasks)
 */
const calculateDuration = (
  startTime: string | undefined,
  endTime: string | undefined,
  now: number
) => {
  if (!startTime) return '-';

  try {
    const start = new Date(startTime).getTime();
    const end = endTime ? new Date(endTime).getTime() : now;

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

function ManualActionDetails({
  node,
  className,
}: {
  node: components['schemas']['Node'];
  className?: string;
}) {
  const detailClassName = cn('text-xs text-muted-foreground', className);

  return (
    <>
      {node.humanTaskCompletedBy || node.humanTaskCompletedById ? (
        <div className={detailClassName}>
          <span className="font-medium">
            <I18nText text={'Completed by:'} />
          </span>{' '}
          <ManualActionSubject
            name={node.humanTaskCompletedBy}
            id={node.humanTaskCompletedById}
            className="text-info"
          />
          {node.finishedAt && (
            <RelativeTime className="ml-1" timestamp={node.finishedAt} />
          )}
        </div>
      ) : null}
      {node.approvedBy || node.approvedById ? (
        <div className={detailClassName}>
          <span className="font-medium">
            <I18nText text={'Approved by:'} />
          </span>{' '}
          <ManualActionSubject
            name={node.approvedBy}
            id={node.approvedById}
            className="text-info"
          />
          {node.approvedAt && (
            <RelativeTime className="ml-1" timestamp={node.approvedAt} />
          )}
        </div>
      ) : null}
      {node.approvalInputs && Object.keys(node.approvalInputs).length > 0 && (
        <div className={detailClassName}>
          <span className="font-medium">
            <I18nText text={'Inputs:'} />
          </span>{' '}
          <span className="whitespace-normal break-words font-mono text-foreground/80">
            {JSON.stringify(node.approvalInputs)}
          </span>
        </div>
      )}
      {node.pushBackHistory && node.pushBackHistory.length > 0 && (
        <PushBackHistory history={node.pushBackHistory} className="pt-1" />
      )}
      {node.rejectedBy || node.rejectedById ? (
        <div className={detailClassName}>
          <span className="font-medium">
            <I18nText text={'Rejected by:'} />
          </span>{' '}
          <ManualActionSubject
            name={node.rejectedBy}
            id={node.rejectedById}
            className="text-error"
          />
          {node.rejectedAt && (
            <RelativeTime className="ml-1" timestamp={node.rejectedAt} />
          )}
        </div>
      ) : null}
      {node.rejectionReason && (
        <div className={detailClassName}>
          <span className="font-medium">
            <I18nText text={'Reason:'} />
          </span>{' '}
          <span className="whitespace-normal break-words text-foreground/80">
            {node.rejectionReason}
          </span>
        </div>
      )}
    </>
  );
}

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
  hideActions = false,
  defaultLogExpanded = false,
  selected = false,
  onInspect,
}: Props) {
  const { ts } = useI18n();
  const { dagRunId, name: dagName } = dagRun;
  const navigate = useNavigate();
  const openSubRun = useOpenSubRun();
  const client = useClient();
  const config = useConfig();
  const dagContext = useContext(DAGContext);
  const dagRunContext = useContext(DAGRunContext);
  const remoteNode = useRemoteNode();
  const { showError } = useErrorModal();
  // State for expanding/collapsing parallel executions
  const [isExpanded, setIsExpanded] = useState(false);
  const [showDialog, setShowDialog] = useState(false);
  // State for inline log expansion
  const [isLogExpanded, setIsLogExpanded] = useState(defaultLogExpanded);
  const [activeLogTab, setActiveLogTab] = useState<'stdout' | 'stderr'>(
    defaultLogExpanded && node.stderr ? 'stderr' : 'stdout'
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
  const logMessage = getLogStepMessage(node.step);
  const hasStdout = !!node.stdout;
  const hasStderr = !!node.stderr;
  const hasLogs = hasStdout || hasStderr;
  const isSubDAGRun =
    !!dagRun.rootDAGRunId &&
    !!dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId;
  const shouldFetchLogStepOutput =
    logMessage !== null && hasStdout && !!dagRunId;

  // Expand the log when this row becomes the first failed step while already
  // mounted (live polling). Applied once per false-to-true transition so a
  // manual collapse afterwards is respected.
  const autoExpandApplied = useRef(defaultLogExpanded);
  useEffect(() => {
    if (!defaultLogExpanded) {
      autoExpandApplied.current = false;
      return;
    }
    if (autoExpandApplied.current || !hasLogs) return;
    autoExpandApplied.current = true;
    setIsLogExpanded(true);
    setActiveLogTab(hasStderr ? 'stderr' : 'stdout');
  }, [defaultLogExpanded, hasLogs, hasStderr]);

  const showStepActions = Boolean(
    !hideActions && dagRunId && config.permissions.runDags
  );
  const canUpdateStepStatus =
    showStepActions &&
    canChangeStepStatus(dagRun, node, { runDags: config.permissions.runDags })
      .ok;
  const rootRunning =
    isSubDAGRun && dagRunContext.rootStatus === Status.Running;
  const retryGate = canRetryStep(dagRun, node, {
    runDags: config.permissions.runDags,
    rootRunning,
  });
  const canRetry = showStepActions && retryGate.ok;
  const retryDisabled = retryGate.disabled;
  const retryTitle = rootRunning ? retryGate.reason : 'Retry this step';

  const subDAGLogQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/log',
    whenEnabled(shouldFetchLogStepOutput && isSubDAGRun, {
      params: {
        query: {
          remoteNode,
          stream: Stream.stdout,
        },
        path: {
          name: dagRun.rootDAGRunName,
          dagRunId: dagRun.rootDAGRunId,
          subDAGRunId: dagRun.dagRunId,
          stepName: node.step.name,
        },
      },
    }),
    {
      refreshInterval: isActiveNode ? 2000 : 0,
      revalidateOnFocus: false,
    }
  );

  const dagRunLogQuery = useQuery(
    '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log',
    whenEnabled(shouldFetchLogStepOutput && !isSubDAGRun, {
      params: {
        query: {
          remoteNode,
          stream: Stream.stdout,
        },
        path: {
          name: dagRun.name,
          dagRunId: dagRunId || '',
          stepName: node.step.name,
        },
      },
    }),
    {
      refreshInterval: isActiveNode ? 2000 : 0,
      revalidateOnFocus: false,
    }
  );

  const logOutputContent = isSubDAGRun
    ? subDAGLogQuery.data?.content
    : dagRunLogQuery.data?.content;
  const logQueryError = isSubDAGRun
    ? subDAGLogQuery.error
    : dagRunLogQuery.error;
  const logStepDisplayMessage =
    typeof logOutputContent === 'string'
      ? formatLogStepOutput(logOutputContent)
      : shouldFetchLogStepOutput && !logQueryError
        ? 'Loading log output...'
        : logMessage;

  // Active tasks read the shared one-second clock; the others never tick.
  // The time is cut to the second, so each read within a tick is the same.
  const currentDuration = useClock(
    isActiveNode && node.startedAt ? 1000 : null,
    (now) =>
      calculateDuration(
        node.startedAt,
        node.finishedAt,
        Math.floor(now / 1000) * 1000
      )
  );

  const url = buildDAGPageURL({
    fileName: name,
    remoteNode,
    tab: 'log',
    step: node.step.name,
    rootDAGRunId: isSubDAGRun ? dagRun.rootDAGRunId : dagRunId,
    rootDAGRunName: isSubDAGRun ? dagRun.rootDAGRunName : undefined,
    subDAGRunId: isSubDAGRun ? dagRun.dagRunId : undefined,
  });

  // Handle sub dagRun navigation
  const handleSubDAGRunNavigation = (
    subRunIndex: number = 0,
    e?: React.MouseEvent
  ) => {
    if (hasSubDAGRun && allSubRuns[subRunIndex]) {
      const subDAGRunId = allSubRuns[subRunIndex].dagRunId;

      // A plain click stacks the child run over the current view. A modifier
      // click still opens a real page, so it can go to another tab.
      const modifierClick = Boolean(e && (e.metaKey || e.ctrlKey));
      if (openSubRun && !modifierClick) {
        openSubRun({
          name:
            allSubRuns[subRunIndex].dagName || node.step.call || node.step.name,
          dagRunId: subDAGRunId,
        });
        return;
      }

      // Child runs open on the run page under their root run.
      const url = buildDAGRunPageURL({
        rootDAGRunName: dagRun?.rootDAGRunId
          ? dagRun.rootDAGRunName
          : dagRun?.name || name,
        rootDAGRunId: dagRun?.rootDAGRunId || dagRunId || '',
        subDAGRunId,
        remoteNode,
      });
      if (modifierClick) {
        window.open(url, '_blank');
      } else {
        navigate(url);
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

  // Handle status update
  const handleStatusUpdate = async (
    step: components['schemas']['Step'],
    status: NodeStatus
  ) => {
    // Check if this is a sub DAG-run
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

  // Determine which stream to show based on active tab
  const currentStream: components['schemas']['Stream'] =
    activeLogTab === 'stderr' && hasStderr ? Stream.stderr : Stream.stdout;

  const retryDialog = (
    <StepRetryDialog
      open={showDialog}
      onOpenChange={setShowDialog}
      dagRun={dagRun}
      stepName={node.step.name}
      disabled={retryDisabled || !canRetry}
    />
  );

  const stepActionsMenu =
    canRetry || canUpdateStepStatus ? (
      <DropdownMenu>
        <Tooltip>
          <TooltipTrigger asChild>
            <DropdownMenuTrigger asChild>
              <Button
                aria-label={ts('Step actions')}
                size="icon-sm"
                title={ts('Step actions')}
                variant="ghost"
                onClick={(event) => event.stopPropagation()}
              >
                <EllipsisVertical className="h-4 w-4" />
              </Button>
            </DropdownMenuTrigger>
          </TooltipTrigger>
          <TooltipContent>
            <I18nText text={'Step actions'} />
          </TooltipContent>
        </Tooltip>
        <DropdownMenuContent
          align="end"
          onClick={(event) => event.stopPropagation()}
        >
          {canRetry && (
            <DropdownMenuItem
              disabled={retryDisabled}
              onSelect={() => setShowDialog(true)}
              title={retryTitle}
            >
              <Play className="mr-2 h-4 w-4 text-success" />
              <I18nText text={'Retry step'} />
            </DropdownMenuItem>
          )}
          {canRetry && canUpdateStepStatus && <DropdownMenuSeparator />}
          {canUpdateStepStatus && (
            <DropdownMenuItem onSelect={() => setShowStatusModal(true)}>
              <CircleDot className="mr-2 h-4 w-4" />
              <I18nText text={'Change status'} />
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    ) : null;

  // A button, so keyboard and touch users reach the details a row click
  // opens. It stops the click so the row does not handle it again.
  const stepName = onInspect ? (
    <button
      type="button"
      onClick={(event) => {
        event.stopPropagation();
        onInspect(node.step.name);
      }}
      className="rounded-sm text-left hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      {node.step.name}
    </button>
  ) : (
    node.step.name
  );

  // Render desktop view (table row)
  if (view === 'desktop') {
    return (
      <>
        <StyledTableRow
          className={cn(
            'hover:bg-muted/50 transition-colors duration-200 h-auto cursor-pointer',
            selected && 'bg-primary/5 hover:bg-primary/10'
          )}
          data-selected={selected || undefined}
          onClick={() => {
            if (onInspect) {
              onInspect(node.step.name);
              return;
            }
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
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    setIsLogExpanded(!isLogExpanded);
                    if (!isLogExpanded) {
                      setActiveLogTab(hasStdout ? 'stdout' : 'stderr');
                    }
                  }}
                  className="text-muted-foreground hover:text-foreground/90"
                  aria-expanded={isLogExpanded}
                  aria-label={ts(isLogExpanded ? 'Hide logs' : 'Show logs')}
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
                {stepName}
                {hasSubDAGRun && (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="inline-flex items-center text-primary cursor-pointer">
                        <GitBranch className="h-4 w-4" />
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>
                      <span className="text-xs">
                        <I18nText text={'Sub DAG Run:'} /> {subDagName}
                      </span>
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
            <div className="min-w-0 space-y-1.5">
              {isHarnessStep(node.step) ? (
                <HarnessStepSummary step={node.step} />
              ) : logStepDisplayMessage !== null ? (
                <LogStepMessage message={logStepDisplayMessage} compact />
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
                <RelativeTime timestamp={node.startedAt} />
              </div>
              {node.startedAt && (
                <div className="text-xs text-muted-foreground flex items-center gap-1.5 leading-tight">
                  <span className="font-medium flex items-center">
                    <I18nText text={'Duration:'} />
                    {isActiveNode && (
                      <span
                        className={`inline-block w-2 h-2 rounded-full ml-1.5 animate-pulse ${activeDotClass}`}
                      />
                    )}
                  </span>
                  {currentDuration}
                </div>
              )}
              <ManualActionDetails node={node} className="leading-tight" />
            </div>
          </TableCell>

          {/* Status */}
          <TableCell className="text-center">
            <div className="inline-flex flex-col items-center gap-1">
              <I18nProps>
                <div
                  onClick={(e) => {
                    e.stopPropagation();
                    if (!canUpdateStepStatus) return;
                    setShowStatusModal(true);
                  }}
                  className={cn(canUpdateStepStatus && 'cursor-pointer')}
                  title={
                    canUpdateStepStatus ? 'Click to update status' : undefined
                  }
                >
                  <NodeStatusChip status={node.status} size="sm">
                    {node.statusLabel}
                  </NodeStatusChip>
                </div>
              </I18nProps>
              <BuildDecisionBadge build={node.build} remoteNode={remoteNode} />
              <RetryCountBadge count={node.retryCount} />
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
                            title={ts(
                              'View stdout log (Cmd/Ctrl+Click for new tab)'
                            )}
                          >
                            <Code className="h-3.5 w-3.5 text-muted-foreground" />
                            <span>
                              <I18nText text={'stdout'} />
                            </span>
                          </a>
                        </TooltipTrigger>
                        <TooltipContent>
                          <span className="text-xs">
                            <I18nText text={'Standard Output Log'} />
                          </span>
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
                          title={ts(
                            'View stderr log (Cmd/Ctrl+Click for new tab)'
                          )}
                        >
                          <AlertCircle className="h-3.5 w-3.5 text-warning" />
                          <span className="text-warning">
                            <I18nText text={'stderr'} />
                          </span>
                        </a>
                      </TooltipTrigger>
                      <TooltipContent>
                        <span className="text-xs">
                          <I18nText text={'Error Output Log'} />
                        </span>
                      </TooltipContent>
                    </Tooltip>
                  )}
                </div>
              )}

              {/* Errors - Simplified */}
              {node.error && (
                <div
                  className="line-clamp-3 text-xs leading-relaxed whitespace-pre-wrap break-words text-error"
                  title={node.error}
                >
                  {node.error}
                </div>
              )}
              {node.step.preconditions?.some((cond) => cond.error) && (
                <div className="text-xs text-warning leading-relaxed">
                  <I18nText text={'Precondition unmet'} />
                </div>
              )}
            </div>
          </TableCell>
          {showStepActions && (
            <TableCell className="text-center">
              <div className="flex items-center justify-center gap-1">
                {stepActionsMenu}
              </div>
              {retryDialog}
            </TableCell>
          )}
        </StyledTableRow>

        {/* Inline log viewer row - spans entire table width */}
        {isLogExpanded && hasLogs && (
          <StyledTableRow className="bg-muted">
            <TableCell colSpan={showStepActions ? 7 : 6} className="p-3">
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
                          'px-3 py-1.5 text-xs font-medium transition-colors border-b-2',
                          activeLogTab === 'stdout'
                            ? 'text-foreground border-primary'
                            : 'text-muted-foreground border-transparent hover:text-foreground'
                        )}
                      >
                        <I18nText text={'stdout'} />
                      </button>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setActiveLogTab('stderr');
                        }}
                        className={cn(
                          'px-3 py-1.5 text-xs font-medium transition-colors border-b-2',
                          activeLogTab === 'stderr'
                            ? 'text-foreground border-primary'
                            : 'text-muted-foreground border-transparent hover:text-foreground'
                        )}
                      >
                        <I18nText text={'stderr'} />
                      </button>
                    </div>
                  ) : (
                    <div className="text-xs font-medium text-muted-foreground">
                      {hasStdout ? (
                        <I18nText text={'stdout'} />
                      ) : (
                        <I18nText text={'stderr'} />
                      )}
                    </div>
                  )}

                  {/* Expand to modal button */}
                  <I18nProps>
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
                      <span>
                        <I18nText text={'Full view'} />
                      </span>
                    </button>
                  </I18nProps>
                </div>

                {/* Simple inline log viewer - no controls */}
                <InlineLogViewer
                  dagName={dagRun?.name || name}
                  dagRunId={dagRunId || ''}
                  stepName={node.step.name}
                  stream={currentStream}
                  dagRun={dagRun}
                  live={isActiveNode}
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
    <div className="p-4 rounded-lg border border-border bg-card">
      {/* Header with number and status */}
      <div className="flex justify-between items-center mb-3">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-foreground/90 text-sm bg-muted rounded-full w-6 h-6 flex items-center justify-center">
            {rownum}
          </span>
          <p className="font-semibold text-foreground">
            {stepName}
            {hasSubDAGRun && (
              <span className="inline-flex items-center text-primary ml-1.5">
                <GitBranch className="h-4 w-4" />
              </span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-1">
          <NodeStatusChip status={node.status} size="sm">
            {node.statusLabel}
          </NodeStatusChip>
          <BuildDecisionBadge build={node.build} remoteNode={remoteNode} />
          {stepActionsMenu}
        </div>
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
                <I18nText text={'View Sub DAG Run:'} /> {subDagName}
              </div>
              {allSubRuns[0]?.params && (
                <div className="text-xs text-muted-foreground mb-3">
                  <I18nText text={'Parameters:'} />{' '}
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
          {logMessage !== null ? (
            <I18nText text={'Message:'} />
          ) : isHarnessStep(node.step) ? (
            <I18nText text={'Execution:'} />
          ) : node.step.commands && node.step.commands.length > 1 ? (
            <I18nText text={'Commands:'} />
          ) : (
            <I18nText text={'Command:'} />
          )}
        </div>
        <div className="space-y-1.5">
          {isHarnessStep(node.step) ? (
            <HarnessStepSummary step={node.step} />
          ) : logStepDisplayMessage !== null ? (
            <LogStepMessage message={logStepDisplayMessage} />
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
          <I18nText text={'Timing:'} />
        </div>
        <div className="space-y-0.5">
          <div className="text-xs text-muted-foreground">
            <I18nText text={'Started:'} />{' '}
            <RelativeTime timestamp={node.startedAt} />
          </div>
          {node.startedAt && (
            <div className="text-xs text-muted-foreground flex items-center gap-1.5">
              <span className="font-medium flex items-center">
                <I18nText text={'Duration:'} />
                {isActiveNode && (
                  <span
                    className={`inline-block w-2 h-2 rounded-full ml-1.5 animate-pulse ${activeDotClass}`}
                  />
                )}
              </span>
              {currentDuration}
            </div>
          )}
          <RetryCountBadge count={node.retryCount} />
          <ManualActionDetails node={node} />
        </div>
      </div>

      {/* Error section */}
      {(node.error || node.step.preconditions?.some((cond) => cond.error)) && (
        <div className="mb-3">
          <div className="text-xs font-medium text-foreground/90 mb-1">
            <I18nText text={'Errors:'} />
          </div>

          {node.error && (
            <div className="text-xs bg-error-muted border border-error/20 rounded-md p-1.5 mb-2 whitespace-pre-wrap break-words text-error leading-tight">
              {node.error}
            </div>
          )}

          {node.step.preconditions?.some((cond) => cond.error) && (
            <div>
              <div className="text-xs font-medium text-warning mb-1">
                <I18nText text={'Precondition Unmet:'} />
              </div>
              {node.step.preconditions
                .filter((cond) => cond.error)
                .map((cond, idx) => (
                  <div
                    key={idx}
                    className="text-xs bg-warning-muted border border-warning/20 rounded-md p-1.5 mb-1 whitespace-pre-wrap break-words text-warning leading-tight"
                  >
                    <div className="font-medium">
                      <I18nText text={'Condition:'} /> {cond.condition}
                    </div>
                    <div>
                      <I18nText text={'Expected:'} />{' '}
                      {cond.expectedAny?.join(' | ') ?? cond.expected}
                    </div>
                    <div>
                      <I18nText text={'Error:'} /> {cond.error}
                    </div>
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
                <I18nProps>
                  <a
                    href={url}
                    onClick={handleViewLog}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors bg-card hover:bg-muted cursor-pointer"
                    title="View stdout log (Cmd/Ctrl+Click for new tab)"
                  >
                    <Code className="h-3.5 w-3.5 text-muted-foreground" />
                    <span>
                      <I18nText text={'stdout'} />
                    </span>
                  </a>
                </I18nProps>
                {node.stderr && <div className="w-px h-5 bg-border" />}
              </>
            )}

            {/* stderr button */}
            {node.stderr && (
              <I18nProps>
                <a
                  href={`${url}&stream=stderr`}
                  onClick={(e) => {
                    if (!(e.metaKey || e.ctrlKey) && onViewLog) {
                      e.preventDefault();
                      onViewLog(
                        `${node.step.name}_stderr`,
                        dagRunId || '',
                        node
                      );
                    }
                  }}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors bg-card hover:bg-warning/10 cursor-pointer"
                  title="View stderr log (Cmd/Ctrl+Click for new tab)"
                >
                  <AlertCircle className="h-3.5 w-3.5 text-warning" />
                  <span className="text-warning">
                    <I18nText text={'stderr'} />
                  </span>
                </a>
              </I18nProps>
            )}
          </div>
        </div>
      )}
      {retryDialog}
      <StatusUpdateModal
        visible={showStatusModal}
        dismissModal={() => setShowStatusModal(false)}
        step={node.step}
        onSubmit={handleStatusUpdate}
      />
    </div>
  );
}

export default NodeStatusTableRow;
