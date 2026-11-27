// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Column,
  ColumnFiltersState,
  createColumnHelper,
  ExpandedState,
  flexRender,
  getCoreRowModel,
  getExpandedRowModel,
  getFilteredRowModel,
  Row,
  RowData,
  Updater,
  useReactTable,
} from '@tanstack/react-table';
import {
  ArrowDown,
  ArrowUp,
  Calendar,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  PencilLine,
  Search,
  Trash2,
} from 'lucide-react';
import React, {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { Link } from 'react-router-dom';
import { components, ServiceDAGState, Status } from '../../../../api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import { RunStatusChip } from '@/features/dag-runs/components/common/RunStatusChip';
import { CreateDAGButton, DAGPagination } from '../common';
import DAGActions from '../common/DAGActions';
import LiveSwitch from '../common/LiveSwitch';
import ActivityBars from './ActivityBars';
import ServiceActivityStrip from './ServiceActivityStrip';
import { DAGRecentRuns } from './DAGRecentRuns';
import { LastRunCell, ServiceStateChip } from './LastRunCell';
import { ScheduleLine } from './ScheduleSummary';
import { ServiceDAGNextRun } from '@/features/service-dags/components/ServiceDAGState';
import { isServiceDAG } from '../../../../lib/dagSchedule';
import {
  GroupScheduleCell,
  GroupWebhookCell,
  ScheduleCell,
  WebhookCell,
} from './ActiveCell';
import PinToggle from '../common/PinToggle';

// Import shadcn/ui components
import { PanelWidthContext } from '../../../../components/SplitLayout';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { Input } from '@/components/ui/input';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { LabelCombobox } from '@/components/ui/label-combobox';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { AppBarContext } from '../../../../contexts/AppBarContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useCanManageWebhooks } from '@/contexts/AuthContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { PeekLink } from '@/features/peek/PeekLink';
import {
  PEEK_SHOWN_ROW,
  usePeekOrOpen,
  usePeekSequence,
  usePeekShown,
  useRevealShownRow,
} from '@/features/peek/usePeekOrOpen';
import {
  isRunSortField,
  maxRunSortDAGs,
  type WorkflowFilterView,
} from './workflowViews';
import { useQuery } from '../../../../hooks/api';
import { parseLabelParts } from '../../../../lib/utils';
import {
  isWorkspaceLabel,
  withoutWorkspaceLabels,
} from '../../../../lib/workspace';
import { useServiceDAGs } from '@/features/service-dags/hooks/useServiceDAGs';
import { ViewSelector } from '@/features/views/ViewSelector';
import { DAGBackfillChip } from '@/features/backfills/components/DAGBackfillChip';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import {
  activityColumnClasses,
  CARD_VIEW_THRESHOLD,
  useActivityPlacement,
} from './activityVisibility';

/**
 * The name of a workflow. A plain click shows its peek, like a click on the
 * row; it is a real link, so a middle click or a Ctrl click opens a new tab.
 */
function DAGNameLink({
  fileName,
  children,
}: {
  fileName: string;
  children: React.ReactNode;
}) {
  return (
    <PeekLink
      peek={{ dag: fileName }}
      className="block truncate text-sm font-medium text-foreground hover:underline"
      onClick={(event) => event.stopPropagation()}
    >
      {children}
    </PeekLink>
  );
}

/**
 * A key handler that opens on Enter pressed on the element itself. Enter on a
 * link or a control inside it stays with that control.
 */
function onOwnEnter(open: (event: React.KeyboardEvent) => void) {
  return (event: React.KeyboardEvent) => {
    if (event.key !== 'Enter' || event.target !== event.currentTarget) return;
    event.preventDefault();
    open(event);
  };
}

// Reusable DAG Card component for mobile and narrow panel views
interface DAGCardProps {
  dag: components['schemas']['DAGFile'];
  onOpen: (event: React.MouseEvent | React.KeyboardEvent) => void;
  onLabelClick: (label: string) => void;
  refreshFn: () => void;
  canDeleteDAGs: boolean;
  isDeleteSelected: boolean;
  onToggleDeleteSelection: (fileName: string) => void;
  canEdit: boolean;
  canPin: boolean;
  isPinPending: boolean;
  onTogglePin: (dag: components['schemas']['DAGFile']) => void;
  /** The overlay shows the workflow, or showed it last. */
  isShown?: boolean;
  /**
   * The workflow as a service DAG, when it is one and its state is known. The
   * chip shows the state in place of the run status.
   */
  service?: components['schemas']['ServiceDAG'];
  /** The activity of the workflow, once the list has read it. */
  activity?: components['schemas']['DAGListActivity'];
  /**
   * The cards show activity: the card keeps the room of its strip until the
   * activity arrives.
   */
  showActivity?: boolean;
  /** The list does not gather by group: the group shows before the name. */
  showGroup?: boolean;
  className?: string;
}

/**
 * Opens the workflow page with the YAML editor. It is a real link, so a
 * middle click or a Ctrl click opens a new tab.
 */
function EditDAGButton({ dag }: { dag: components['schemas']['DAGFile'] }) {
  const remoteNode = useRemoteNode();
  return (
    <I18nProps>
      <Button
        asChild
        variant="ghost"
        size="icon-sm"
        aria-label={`Edit workflow ${dag.dag.name}`}
        title="Edit workflow"
      >
        <Link
          to={buildDAGPageURL({
            fileName: dag.fileName,
            remoteNode,
            yaml: true,
          })}
        >
          <PencilLine className="h-4 w-4" />
        </Link>
      </Button>
    </I18nProps>
  );
}

/** The chevron that unfolds the recent runs under the row of a workflow. */
function ActivityToggle({
  expanded,
  onToggle,
}: {
  expanded: boolean;
  onToggle: () => void;
}) {
  const { ts } = useI18n();
  const label = ts(expanded ? 'Hide recent runs' : 'Show recent runs');
  return (
    <button
      type="button"
      aria-expanded={expanded}
      aria-label={label}
      title={label}
      data-testid="dag-activity-toggle"
      onClick={(event) => {
        event.stopPropagation();
        onToggle();
      }}
      className="flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      {expanded ? (
        <ChevronDown className="h-4 w-4" />
      ) : (
        <ChevronRight className="h-4 w-4" />
      )}
    </button>
  );
}

/**
 * The mark of the card that the overlay shows: PEEK_SHOWN_ROW with a tint.
 * card-obsidian is plain CSS, which wins over the utility layer, so the
 * mark needs !important; the class stays literal for the Tailwind scan.
 */
const SHOWN_CARD = 'shadow-[inset_3px_0_0_0_var(--primary)]! bg-muted/20!';

function DAGCard({
  dag,
  onOpen,
  onLabelClick,
  refreshFn,
  canDeleteDAGs,
  isDeleteSelected,
  onToggleDeleteSelection,
  canEdit,
  canPin,
  isPinPending,
  onTogglePin,
  isShown = false,
  service,
  activity,
  showActivity = false,
  showGroup = false,
  className = '',
}: DAGCardProps) {
  const fileName = dag.fileName;
  const title = dag.dag.name;
  const status = dag.latestDAGRun?.status;
  const statusLabel = dag.latestDAGRun?.statusLabel;
  const labels = withoutWorkspaceLabels(dag.dag.labels ?? dag.dag.tags ?? []);
  const description = dag.dag.description;
  const hasSchedule = (dag.dag.schedule ?? []).length > 0;
  const isService = isServiceDAG(dag.dag);
  // An operator's stop shows as the label Stopped in place of Enabled.
  const serviceState = service?.state;
  const isStopped = serviceState === ServiceDAGState.stopped;

  // The outline marks the focus: the shadow of card-obsidian hides a ring.
  return (
    <div
      tabIndex={0}
      data-peek-shown={isShown || undefined}
      className={`card-obsidian p-2.5 cursor-pointer hover:bg-white/[0.05] transition-colors duration-300 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring ${status === Status.Running ? 'animate-running-row' : ''} ${isShown ? SHOWN_CARD : ''} ${className}`}
      onClick={onOpen}
      onKeyDown={onOwnEnter(onOpen)}
    >
      {/* Header: Name + Status */}
      <div className="flex justify-between items-start gap-2 mb-1.5">
        <div className="flex min-w-0 flex-1 items-start gap-2">
          {canDeleteDAGs && (
            <div
              className="flex h-6 w-6 flex-shrink-0 items-center justify-center"
              onClick={(event) => event.stopPropagation()}
            >
              <Checkbox
                aria-label={`Select workflow ${title}`}
                checked={isDeleteSelected}
                onCheckedChange={() => onToggleDeleteSelection(fileName)}
              />
            </div>
          )}
          <div className="flex min-w-0 flex-1 items-baseline gap-1">
            {showGroup && dag.dag.group && (
              <span
                data-testid="dag-group-prefix"
                title={dag.dag.group}
                className="max-w-[40%] shrink-0 truncate text-xs text-muted-foreground"
              >
                {dag.dag.group} /
              </span>
            )}
            <PeekLink
              peek={{ dag: fileName }}
              className="font-medium text-xs truncate flex-1 min-w-0 hover:underline"
              onClick={(event) => event.stopPropagation()}
            >
              {title}
            </PeekLink>
          </div>
        </div>
        <div className="flex flex-shrink-0 items-center gap-1">
          <PinToggle
            name={title}
            pinned={dag.pinned}
            canToggle={canPin}
            pending={isPinPending}
            onToggle={() => onTogglePin(dag)}
            className="size-6"
          />
          {isService && serviceState ? (
            <ServiceStateChip state={serviceState} />
          ) : (
            <RunStatusChip
              name={title}
              dagRunId={dag.latestDAGRun?.dagRunId}
              status={status}
              size="xs"
            >
              {statusLabel}
            </RunStatusChip>
          )}
        </div>
      </div>

      {/* Description */}
      {description && (
        <div className="text-xs text-muted-foreground mb-1.5 line-clamp-1">
          {description}
        </div>
      )}

      {/* Schedule & Last Run */}
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground mb-1.5">
        <ScheduleLine dag={dag} emptyLabel={false} />
        {service && <ServiceDAGNextRun dag={service} className="text-xs" />}
        {dag.latestDAGRun.startedAt && dag.latestDAGRun.startedAt !== '-' && (
          <span className="flex items-center gap-0.5">
            <Calendar className="h-2.5 w-2.5" />
            <RelativeTime
              className="text-xs"
              timestamp={dag.latestDAGRun.startedAt}
              absolute={dag.latestDAGRun.startedAt}
            />
          </span>
        )}
      </div>

      {/* Until its activity arrives, a service card keeps the room of the
          bars too. */}
      {showActivity &&
        (isService && activity ? (
          activity.buckets && (
            <ServiceActivityStrip
              activity={activity}
              fileName={fileName}
              className="mb-1.5"
            />
          )
        ) : (
          <ActivityBars activity={activity} className="mb-1.5" />
        ))}

      {/* Labels */}
      {labels.length > 0 && (
        <div className="flex flex-wrap gap-0.5 mb-1.5">
          {[...labels]
            .sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()))
            .map((label) => (
              <Badge
                key={label}
                variant="outline"
                className="h-[18px] rounded border-transparent bg-muted px-1.5 py-0 text-[11px] font-normal text-muted-foreground"
                onClick={(e) => {
                  e.stopPropagation();
                  onLabelClick(label);
                }}
              >
                {label}
              </Badge>
            ))}
        </div>
      )}

      {dag.backfill && (
        <div className="mb-1.5">
          <DAGBackfillChip backfill={dag.backfill} />
        </div>
      )}

      {/* Actions row: LiveSwitch + DAGActions */}
      <div className="flex flex-wrap items-center justify-between gap-1 pt-1.5 border-t border-border/50 min-w-0">
        <div
          className="flex items-center gap-1 flex-shrink-0 min-w-0"
          onClick={(e) => e.stopPropagation()}
        >
          {(hasSchedule || isService) && (
            <LiveSwitch dag={dag} refresh={refreshFn} />
          )}
          <span className="text-xs text-muted-foreground truncate">
            {dag.suspended ? (
              <I18nText text={'Suspended'} />
            ) : isService ? (
              <I18nText text={isStopped ? 'Stopped' : 'Enabled'} />
            ) : hasSchedule ? (
              <I18nText text={'Live'} />
            ) : (
              <I18nText text={'No schedule'} />
            )}
          </span>
        </div>
        <div
          className="flex flex-shrink-0 items-center"
          onClick={(e) => e.stopPropagation()}
        >
          <DAGActions
            dag={dag.dag}
            status={dag.latestDAGRun}
            fileName={fileName}
            label={false}
            refresh={refreshFn}
            service={service}
          />
          {canEdit && (
            <ActionSlot>
              <EditDAGButton dag={dag} />
            </ActionSlot>
          )}
        </div>
      </div>
    </div>
  );
}

// The actions of a row sit in fixed slots of 40 px, as in DAGActions: Start,
// Retry, Stop and Edit, plus the padding of the cell.
const ACTIONS_WIDTH = 4 * 40 + 16;

/** The fixed slot of Edit, beside the slots of DAGActions. */
function ActionSlot({ children }: { children: React.ReactNode }) {
  return <span className="flex w-10 shrink-0 justify-center">{children}</span>;
}

/**
 * The strip of a table row. Its click handler keeps its identity while
 * `onOpenRun` does, so a render of the table alone does not redraw the strip.
 */
function ActivityCell({
  fileName,
  activity,
  onOpenRun,
  hasRun,
  expanded,
  onToggleActivity,
}: {
  fileName: string;
  activity?: components['schemas']['DAGListActivity'];
  onOpenRun?: (
    fileName: string,
    runId: string,
    event: React.MouseEvent
  ) => void;
  /** The workflow has a run, so its row can unfold its recent runs. */
  hasRun: boolean;
  expanded: boolean;
  onToggleActivity?: (fileName: string) => void;
}) {
  const openRun = useCallback(
    (runId: string, event: React.MouseEvent) =>
      onOpenRun?.(fileName, runId, event),
    [fileName, onOpenRun]
  );
  const toggle = useCallback(
    () => onToggleActivity?.(fileName),
    [fileName, onToggleActivity]
  );
  return (
    <div className="flex items-center gap-1">
      <ActivityBars activity={activity} fluid onOpenRun={openRun} />
      {hasRun || (activity?.runs.length ?? 0) > 0 ? (
        <ActivityToggle expanded={expanded} onToggle={toggle} />
      ) : (
        // A workflow with no run has nothing to unfold; the space stays.
        <span aria-hidden="true" className="size-6 shrink-0" />
      )}
    </div>
  );
}

// Pinned groups and DAGs lead the rows; the card view puts a divider after them.
function countLeadingPinnedRows(rows: Row<Data>[]): number {
  let count = 0;
  for (const row of rows) {
    const item = row.original;
    const pinned =
      item.kind === ItemKind.Group
        ? (item.subRows ?? []).length > 0 &&
          (item.subRows ?? []).every(
            (sub) => sub.kind === ItemKind.DAG && sub.dag.pinned
          )
        : (item as DAGRow).dag.pinned;
    // The rows of an unfolded pinned group belong to the leading part.
    if (row.depth > 0 ? count === 0 : !pinned) {
      break;
    }
    count++;
  }
  return count;
}

/**
 * Props for the DAGTable component
 */
type Props = {
  /** List of DAG files to display */
  dags: components['schemas']['DAGFile'][];
  /**
   * The activity of the listed workflows, by file name. A workflow with no
   * entry draws no strip. Without it, as on a node that gives no activity,
   * the table leaves the Activity column out and the cards keep no room for a
   * strip.
   */
  activity?: ReadonlyMap<string, components['schemas']['DAGListActivity']>;
  /** Current group filter */
  group: string;
  /** Function to refresh the data */
  refreshFn: () => void;
  /** Current search text */
  searchText: string;
  /** Handler for search text changes */
  handleSearchTextChange: (searchText: string) => void;
  /** Current label filter (multi-select) */
  searchLabels: string[];
  /** Handler for label filter changes */
  handleSearchLabelsChange: (labels: string[]) => void;
  /** Whether only scheduled, unsuspended workflows are shown */
  activeOnly: boolean;
  /** Handler for the active workflow filter */
  handleActiveOnlyChange: (activeOnly: boolean) => void;
  /** Loading state */
  isLoading?: boolean;
  /** Pagination props */
  pagination?: {
    /** Total number of pages */
    totalPages: number;
    /** Current page number */
    page: number;
    /** Number of items per page */
    pageLimit: number;
    /** Callback for page change */
    pageChange: (page: number) => void;
    /** Callback for page limit change */
    onPageLimitChange: (pageLimit: number) => void;
  };
  /** Current sort field */
  sortField?: string;
  /** Current sort order */
  sortOrder?: string;
  /** Handler for sort changes */
  onSortChange?: (field: string, order: string) => void;
  /** Saved filter views available in the current context */
  workflowViews: WorkflowFilterView[];
  /** Selected saved view, if any */
  activeWorkflowViewId: string | null;
  /** Saved view that opens by default */
  defaultWorkflowViewId?: string;
  /** Whether the built-in unfiltered view is selected */
  isAllWorkflowsView: boolean;
  /** Whether the selected saved view differs from its stored filters */
  isWorkflowViewEdited: boolean;
  areWorkflowFiltersSaved: boolean;
  /** Whether the current user can mutate shared views in this scope */
  canManageWorkflowViews: boolean;
  /** Whether the current user can delete workflows in this scope */
  canDeleteDAGs: boolean;
  /** Latest shared-view mutation error */
  workflowViewError?: string | null;
  onSelectWorkflowView: (viewId: string) => void;
  onShowAllWorkflows: () => void;
  onResetWorkflowView: () => void;
  onSaveWorkflowView: (
    name: string,
    makeDefault: boolean,
    pinned: boolean
  ) => Promise<void>;
  onUpdateWorkflowView: () => Promise<void>;
  onSetDefaultWorkflowView: (viewId: string | undefined) => Promise<void>;
  onSetPinnedWorkflowView: (viewId: string, pinned: boolean) => Promise<void>;
  onDeleteWorkflowView: (viewId: string) => Promise<void>;
  onRenameWorkflowView: (viewId: string, name: string) => Promise<void>;
  onDeleteDAGs: (fileNames: string[]) => Promise<DAGDeleteResult[]>;
  /** Whether the current user can edit a workflow */
  canEditDAG: (dag: components['schemas']['DAGFile']) => boolean;
  /** Whether the current user can pin a workflow for everyone */
  canPinDAG: (dag: components['schemas']['DAGFile']) => boolean;
  /** Pins or unpins a workflow for all users */
  onSetDAGPinned: (fileName: string, pinned: boolean) => Promise<void>;
  onSetDAGsPinned: (fileNames: string[], pinned: boolean) => Promise<void>;
  /** Total workflows matching the server-side filters */
  resultCount?: number;
  /**
   * Whether the request of the list applies a filter. The request follows the
   * typed filters after a delay, so the two can differ for a moment.
   */
  isRequestFiltered: boolean;
};

export type DAGDeleteResult = {
  fileName: string;
  error?: string;
};

/**
 * Types for table rows
 */
type RowItem = DAGRow | GroupRow;
type DAGRow = {
  kind: ItemKind;
  name: string;
  dag: components['schemas']['DAGFile'];
};
type GroupRow = {
  kind: ItemKind.Group;
  name: string;
};
enum ItemKind {
  DAG = 0,
  Group,
}
type Data = RowItem & { subRows?: RowItem[] };

declare module '@tanstack/react-table' {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  interface TableMeta<TData extends RowData> {
    group: string;
    refreshFn: () => void;
    // Add label click handler to meta for direct access in cell
    onLabelClick?: (label: string) => void;
    getDeleteSelectionState?: () => boolean | 'indeterminate';
    isDeleteSelected?: (fileName: string) => boolean;
    onToggleDeleteSelection?: (fileName: string) => void;
    onToggleGroupDeleteSelection?: (fileNames: string[]) => void;
    onToggleAllDeleteSelection?: (checked: boolean) => void;
    canEditDAG?: (dag: components['schemas']['DAGFile']) => boolean;
    canPinDAG?: (dag: components['schemas']['DAGFile']) => boolean;
    onToggleGroupPin?: (dags: components['schemas']['DAGFile'][]) => void;
    /**
     * Every loaded workflow of each group, by group name. A pinned workflow
     * that leads the list on its own row still counts.
     */
    groupMembers?: ReadonlyMap<string, components['schemas']['DAGFile'][]>;
    /** Workflows are not gathered by group: each name shows its group first. */
    showGroupPrefix?: boolean;
    isPinPending?: (fileName: string) => boolean;
    onTogglePin?: (dag: components['schemas']['DAGFile']) => void;
    /** Each service DAG that the server lists, by file name. */
    services?: ReadonlyMap<string, components['schemas']['ServiceDAG']>;
    /** The activity of the listed workflows, by file name. */
    activity?: ReadonlyMap<string, components['schemas']['DAGListActivity']>;
    isActivityExpanded?: (fileName: string) => boolean;
    onToggleActivity?: (fileName: string) => void;
    onOpenRun?: (
      fileName: string,
      runId: string,
      event: React.MouseEvent
    ) => void;
  }
}

const columnHelper = createColumnHelper<Data>();
// --- End Helper Functions ---

const deleteSelectionColumn = columnHelper.display({
  id: 'Select',
  size: 24,
  minSize: 24,
  maxSize: 24,
  header: ({ table }) => (
    <div className="flex h-8 items-center justify-center">
      <I18nProps>
        <Checkbox
          className="size-3.5"
          aria-label="Select all loaded workflows"
          checked={table.options.meta?.getDeleteSelectionState?.() ?? false}
          onCheckedChange={(checked) =>
            table.options.meta?.onToggleAllDeleteSelection?.(checked === true)
          }
        />
      </I18nProps>
    </div>
  ),
  cell: ({ row, table }) => {
    const data = row.original;
    if (data.kind === ItemKind.Group) {
      const fileNames = (data.subRows ?? [])
        .filter((item): item is DAGRow => item.kind === ItemKind.DAG)
        .map((item) => item.dag.fileName);
      if (fileNames.length === 0) {
        return null;
      }
      const selected = fileNames.filter((name) =>
        table.options.meta?.isDeleteSelected?.(name)
      ).length;
      return (
        <div
          className="flex h-8 items-center justify-center"
          onClick={(event) => event.stopPropagation()}
        >
          <I18nProps>
            <Checkbox
              className="size-3.5"
              aria-label={`Select group ${data.name}`}
              checked={
                selected === 0
                  ? false
                  : selected === fileNames.length
                    ? true
                    : 'indeterminate'
              }
              onCheckedChange={() =>
                table.options.meta?.onToggleGroupDeleteSelection?.(fileNames)
              }
            />
          </I18nProps>
        </div>
      );
    }
    const fileName = data.dag.fileName;
    return (
      <div
        className="flex h-8 items-center justify-center"
        onClick={(event) => event.stopPropagation()}
      >
        <Checkbox
          className="size-3.5"
          aria-label={`Select workflow ${data.dag.dag.name}`}
          checked={table.options.meta?.isDeleteSelected?.(fileName) ?? false}
          onCheckedChange={() =>
            table.options.meta?.onToggleDeleteSelection?.(fileName)
          }
        />
      </div>
    );
  },
});

const defaultColumns = [
  columnHelper.display({
    id: 'Pin',
    size: 24,
    minSize: 24,
    maxSize: 24,
    header: () => (
      <span className="sr-only">
        <I18nText text={'Pinned'} />
      </span>
    ),
    cell: ({ row, table }) => {
      const meta = table.options.meta;
      if (row.original.kind === ItemKind.Group) {
        const members = (row.original.subRows ?? [])
          .filter((item): item is DAGRow => item.kind === ItemKind.DAG)
          .map((item) => item.dag);
        if (members.length === 0) {
          return null;
        }
        return (
          <div className="flex min-h-[2.5rem] items-center justify-center">
            <PinToggle
              name={row.original.name}
              subject="group"
              pinned={members.every((dag) => dag.pinned)}
              canToggle={members.every((dag) => meta?.canPinDAG?.(dag))}
              pending={members.some((dag) =>
                meta?.isPinPending?.(dag.fileName)
              )}
              revealOnHover
              className="size-5"
              onToggle={() => meta?.onToggleGroupPin?.(members)}
            />
          </div>
        );
      }
      const dag = (row.original as DAGRow).dag;
      return (
        <div className="flex min-h-[2.5rem] items-center justify-center">
          <PinToggle
            name={dag.dag.name}
            pinned={dag.pinned}
            canToggle={meta?.canPinDAG?.(dag) ?? false}
            pending={meta?.isPinPending?.(dag.fileName)}
            revealOnHover
            className="size-5"
            onToggle={() => meta?.onTogglePin?.(dag)}
          />
        </div>
      );
    },
  }),
  columnHelper.accessor('name', {
    id: 'Name',
    header: () => (
      <span className="text-xs">
        <I18nText text={'Name'} />
      </span>
    ),
    cell: ({ row, getValue, table }) => {
      const data = row.original!;

      if (data.kind === ItemKind.Group) {
        // The chevron that unfolds the group sits in the name cell of its row,
        // so that the rows of the workflows give no width to it.
        return (
          <div
            role="button"
            tabIndex={0}
            aria-expanded={row.getIsExpanded()}
            onClick={(e) => {
              e.stopPropagation();
              row.toggleExpanded();
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.stopPropagation();
                e.preventDefault();
                row.toggleExpanded();
              }
            }}
            className="flex min-h-[2.5rem] cursor-pointer items-center gap-1 rounded text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {row.getIsExpanded() ? (
              <ChevronUp className="h-4 w-4 shrink-0" />
            ) : (
              <ChevronDown className="h-4 w-4 shrink-0" />
            )}
            <span className="truncate font-normal">{getValue()}</span>
          </div>
        );
      } else {
        // DAG Row: Render link with description and labels below
        const labels = withoutWorkspaceLabels(
          data.dag.dag.labels ?? data.dag.dag.tags ?? []
        );
        const description = data.dag.dag.description;

        return (
          <div
            style={{ paddingLeft: `${row.depth * 1.5}rem` }}
            className="space-y-0.5 min-w-0"
          >
            <div className="flex min-w-0 items-baseline gap-1">
              {table.options.meta?.showGroupPrefix && data.dag.dag.group && (
                <span
                  data-testid="dag-group-prefix"
                  title={data.dag.dag.group}
                  // Bounded, so a long group leaves room for the name.
                  className="max-w-[40%] shrink-0 truncate text-xs text-muted-foreground"
                >
                  {data.dag.dag.group} /
                </span>
              )}
              <DAGNameLink fileName={data.dag.fileName}>
                {getValue()}
              </DAGNameLink>
            </div>

            {description && (
              <div
                title={description}
                className="truncate text-xs text-muted-foreground leading-tight"
              >
                {description}
              </div>
            )}

            {labels.length > 0 && (
              <div className="flex flex-wrap gap-0.5">
                {[...labels]
                  .sort((a, b) =>
                    a.toLowerCase().localeCompare(b.toLowerCase())
                  )
                  .map((label) => {
                    const { key, value } = parseLabelParts(label);
                    return (
                      <Badge
                        key={label}
                        variant="outline"
                        className="h-[18px] gap-0.5 rounded border-transparent bg-muted px-1.5 py-0 text-[11px] font-normal text-muted-foreground transition-colors duration-150 cursor-pointer whitespace-normal break-words hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        role="button"
                        tabIndex={0}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            e.stopPropagation();
                            const handleLabelClick =
                              table.options.meta?.onLabelClick;
                            if (handleLabelClick) handleLabelClick(label);
                          }
                        }}
                        onClick={(e) => {
                          e.stopPropagation();
                          e.preventDefault();
                          const handleLabelClick =
                            table.options.meta?.onLabelClick;
                          if (handleLabelClick) handleLabelClick(label);
                        }}
                      >
                        {value !== null ? (
                          <>
                            <span className="font-medium text-foreground/80">
                              {key}
                            </span>
                            <span className="opacity-60">=</span>
                            <span>{value}</span>
                          </>
                        ) : (
                          key
                        )}
                      </Badge>
                    );
                  })}
              </div>
            )}

            {data.dag.backfill && (
              <DAGBackfillChip backfill={data.dag.backfill} />
            )}
          </div>
        );
      }
    },
    filterFn: (row, _, filterValue) => {
      // Use row instead of props
      const data = row.original!;
      if (data.kind === ItemKind.Group) {
        return true; // Always show group rows during filtering
      }
      if (data.kind === ItemKind.DAG) {
        const name = data.dag.dag.name.toLowerCase();
        const fileName = data.dag.fileName.toLowerCase();
        const description = (data.dag.dag.description || '').toLowerCase();
        const filterObject =
          typeof filterValue === 'object' &&
          filterValue !== null &&
          !Array.isArray(filterValue)
            ? (filterValue as { searchText?: string; labels?: string[] })
            : undefined;
        const searchValue = filterObject
          ? (filterObject.searchText ?? '').toLowerCase()
          : Array.isArray(filterValue)
            ? ''
            : String(filterValue ?? '').toLowerCase();

        const labelFilters = (
          filterObject?.labels ??
          (Array.isArray(filterValue) ? filterValue : [])
        ).map((label) => label.toLowerCase());

        const labels = withoutWorkspaceLabels(
          data.dag.dag.labels ?? data.dag.dag.tags ?? []
        );
        const rowLabels = labels.map((label) => label.toLowerCase());

        const matchesText =
          searchValue === '' ||
          fileName.includes(searchValue) ||
          name.includes(searchValue) ||
          description.includes(searchValue) ||
          rowLabels.some((label) => label.includes(searchValue));
        const matchesLabels =
          labelFilters.length === 0 ||
          labelFilters.every((label) => rowLabels.includes(label));

        return matchesText && matchesLabels;
      }
      return false;
    },
  }),
  // Labels column removed as labels are now displayed under the name
  // The filter functionality is preserved in the Name column
  // Removed Started At and Finished At columns
  columnHelper.accessor('kind', {
    id: 'LastRun',
    size: 88,
    minSize: 76,
    header: () => (
      <span className="text-xs">
        <I18nText text={'Last Run'} />
      </span>
    ),
    cell: ({ row, table }) => {
      const data = row.original!;
      if (data.kind !== ItemKind.DAG) {
        return null;
      }
      return (
        <LastRunCell
          dag={data.dag}
          serviceState={
            table.options.meta?.services?.get(data.dag.fileName)?.state
          }
        />
      );
    },
  }),
  columnHelper.accessor('kind', {
    id: 'Activity',
    size: 244,
    minSize: 244,
    header: () => (
      <span className="block text-center text-xs">
        <I18nText text={'Activity'} />
      </span>
    ),
    cell: ({ row, table }) => {
      const data = row.original!;
      if (data.kind !== ItemKind.DAG) {
        return null;
      }
      const meta = table.options.meta;
      const fileName = data.dag.fileName;
      const activity = meta?.activity?.get(fileName);
      // A service DAG draws the buckets of its run history, not its runs as
      // bars, and has no recent runs to unfold.
      if (isServiceDAG(data.dag.dag)) {
        if (!activity) {
          // The room of the strip until the activity arrives.
          return <ActivityBars activity={undefined} fluid />;
        }
        return activity.buckets ? (
          <div className="flex items-center">
            <ServiceActivityStrip
              activity={activity}
              fileName={fileName}
              fluid
            />
          </div>
        ) : null;
      }
      return (
        <ActivityCell
          fileName={fileName}
          activity={activity}
          onOpenRun={meta?.onOpenRun}
          hasRun={!!data.dag.latestDAGRun?.dagRunId}
          expanded={meta?.isActivityExpanded?.(fileName) ?? false}
          onToggleActivity={meta?.onToggleActivity}
        />
      );
    },
  }),
  columnHelper.accessor('kind', {
    id: 'Schedule',
    size: 220,
    minSize: 180,
    header: () => (
      <span className="text-xs">
        <I18nText text={'Schedule'} />
      </span>
    ),
    cell: ({ row, table }) => {
      const data = row.original!;
      if (data.kind === ItemKind.Group) {
        return (
          <GroupScheduleCell
            name={data.name}
            members={table.options.meta?.groupMembers?.get(data.name) ?? []}
            refresh={table.options.meta?.refreshFn}
          />
        );
      }
      return (
        <ScheduleCell
          dag={data.dag}
          service={table.options.meta?.services?.get(data.dag.fileName)}
          refresh={table.options.meta?.refreshFn}
        />
      );
    },
  }),
  columnHelper.accessor('kind', {
    id: 'Webhook',
    size: 88,
    minSize: 88,
    header: () => (
      <span className="text-xs">
        <I18nText text={'Webhook'} />
      </span>
    ),
    cell: ({ row, table }) => {
      const data = row.original!;
      if (data.kind === ItemKind.Group) {
        return (
          <GroupWebhookCell
            name={data.name}
            members={table.options.meta?.groupMembers?.get(data.name) ?? []}
          />
        );
      }
      return <WebhookCell dag={data.dag} />;
    },
  }),
  columnHelper.display({
    id: 'Actions',
    size: ACTIONS_WIDTH,
    minSize: ACTIONS_WIDTH,
    maxSize: ACTIONS_WIDTH,
    header: () => (
      <span className="sr-only">
        <I18nText text={'Actions'} />
      </span>
    ),
    cell: ({ row, table }) => {
      // Use row and table
      const data = row.original!;
      if (data.kind === ItemKind.Group) {
        return null;
      }
      // Assuming DAGActions is refactored or compatible
      return (
        // Wrap DAGActions in a div and stop propagation on its click
        <div
          className="flex items-center justify-center"
          onClick={(e) => e.stopPropagation()}
        >
          <DAGActions
            dag={data.dag.dag}
            status={data.dag.latestDAGRun}
            fileName={data.dag.fileName}
            label={false}
            refresh={table.options.meta?.refreshFn}
            service={table.options.meta?.services?.get(data.dag.fileName)}
          />
          {table.options.meta?.canEditDAG?.(data.dag) && (
            <ActionSlot>
              <EditDAGButton dag={data.dag} />
            </ActionSlot>
          )}
        </div>
      );
    },
  }),
];

// Mapping between column IDs and backend sort fields
const columnToSortField: Record<string, string> = {
  Name: 'name',
  Schedule: 'nextRun',
  LastRun: 'lastRun',
};

const cardSortOptions = [
  { value: 'name', label: 'Name' },
  { value: 'nextRun', label: 'Next run' },
  { value: 'status', label: 'Status' },
  { value: 'lastRun', label: 'Last run' },
] as const;

function getCardSortLabel(sort: string): string {
  return (
    cardSortOptions.find((option) => option.value === sort)?.label ?? 'Name'
  );
}

function getDefaultSortOrder(field: string): string {
  if (field === 'nextRun') {
    return 'asc';
  }
  return 'asc';
}

function WorkflowsEmptyState({
  icon,
  heading,
  description,
  descriptionValues,
  showAllButton,
  onShowAllWorkflows,
}: {
  icon: string;
  heading: string;
  description: string;
  descriptionValues?: Record<string, string>;
  showAllButton: boolean;
  onShowAllWorkflows?: () => void;
}) {
  return (
    <>
      <div className="text-6xl mb-4">{icon}</div>
      <h3 className="text-lg font-medium text-foreground mb-2">
        <I18nText text={heading} />
      </h3>
      <p className="text-sm text-muted-foreground text-center max-w-md mb-4 whitespace-normal break-words">
        <I18nText text={description} values={descriptionValues} />
      </p>
      <div className="flex flex-wrap items-center justify-center gap-2">
        {showAllButton && (
          <Button type="button" variant="outline" onClick={onShowAllWorkflows}>
            <I18nText text={'Show all workflows'} />
          </Button>
        )}
        <CreateDAGButton />
      </div>
    </>
  );
}

function buildGrepSearchUrl(searchText: string): string {
  const params = new URLSearchParams();
  const query = searchText.trim();

  if (query) {
    params.set('q', query);
    params.set('scope', 'dags');
    return `/search?${params.toString()}`;
  }

  return '/search';
}

// --- Header Component for Server-side Sorting ---
const SortableHeader = ({
  column,
  children,
  currentSort,
  currentOrder,
  onSort,
}: {
  column: Column<Data, unknown>;
  children: React.ReactNode;
  currentSort?: string;
  currentOrder?: string;
  onSort?: (field: string, order: string) => void;
}) => {
  const serverSortField = columnToSortField[column.id];
  const isActive = Boolean(serverSortField) && currentSort === serverSortField;

  if (!serverSortField || !onSort) {
    return <>{children}</>;
  }

  const handleClick = () => {
    const newOrder = isActive && currentOrder === 'asc' ? 'desc' : 'asc';
    onSort(serverSortField, newOrder);
  };

  const displayOrder = isActive ? currentOrder || '' : '';

  return (
    <Button
      variant="ghost"
      onClick={handleClick}
      className="-ml-4 h-8 cursor-pointer" // Adjust spacing
    >
      {children}
      {isActive && displayOrder === 'asc' && (
        <ArrowUp className="ml-2 h-4 w-4" />
      )}
      {isActive && displayOrder === 'desc' && (
        <ArrowDown className="ml-2 h-4 w-4" />
      )}
    </Button>
  );
};

// Tooltip body for a run sort (status or last run) that is disabled because
// more workflows match than the server sorts by run.
const RunSortDisabledTooltip = () => (
  <TooltipContent className="bg-muted text-muted-foreground border">
    <p className="text-xs">
      <I18nText
        text={
          'Sorting by this column is disabled for more than {count} workflows'
        }
        values={{ count: maxRunSortDAGs }}
      />
    </p>
  </TooltipContent>
);

/**
 * DAGTable component displays a table of DAGs with filtering, sorting, and grouping capabilities
 */
function DAGTable({
  dags = [],
  activity,
  group = '', // Keep group prop if needed for external filtering/logic
  refreshFn,
  searchText,
  handleSearchTextChange,
  searchLabels,
  handleSearchLabelsChange,
  activeOnly,
  handleActiveOnlyChange,
  isLoading = false,
  pagination,
  sortField = 'name',
  sortOrder = 'asc',
  onSortChange,
  workflowViews,
  activeWorkflowViewId,
  defaultWorkflowViewId,
  isAllWorkflowsView,
  isWorkflowViewEdited,
  areWorkflowFiltersSaved,
  canManageWorkflowViews,
  canDeleteDAGs,
  workflowViewError,
  onSelectWorkflowView,
  onShowAllWorkflows,
  onResetWorkflowView,
  onSaveWorkflowView,
  onUpdateWorkflowView,
  onSetDefaultWorkflowView,
  onSetPinnedWorkflowView,
  onDeleteWorkflowView,
  onRenameWorkflowView,
  onDeleteDAGs,
  canEditDAG,
  canPinDAG,
  onSetDAGPinned,
  onSetDAGsPinned,
  resultCount,
  isRequestFiltered,
}: Props) {
  const { ts } = useI18n();
  // Status and LastRun sorts are disabled when the list total exceeds the
  // server cap, because the server would sort by name instead.
  const runSortDisabled = (resultCount ?? 0) > maxRunSortDAGs;
  const peekOrOpen = usePeekOrOpen();
  const isShown = usePeekShown();
  useRevealShownRow();
  // The webhook cells read webhooks only for a user who may manage them, on
  // the local server. For anyone else the column would hold only dashes.
  const canManageWebhooks = useCanManageWebhooks();
  const remoteNode = useRemoteNode();
  const showWebhooks = canManageWebhooks && remoteNode === 'local';
  const columns = useMemo(() => {
    const shown = showWebhooks
      ? defaultColumns
      : defaultColumns.filter((column) => column.id !== 'Webhook');
    return canDeleteDAGs ? [deleteSelectionColumn, ...shown] : shown;
  }, [canDeleteDAGs, showWebhooks]);
  const tableInstanceRef = useRef<ReturnType<typeof useReactTable> | null>(
    null
  );
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);
  const [deleteSelection, setDeleteSelection] = useState<Set<string>>(
    () => new Set()
  );
  // The workflows whose recent runs are unfolded under their row.
  const [expandedActivity, setExpandedActivity] = useState<Set<string>>(
    () => new Set()
  );
  const toggleActivity = useCallback((fileName: string) => {
    setExpandedActivity((current) => {
      const next = new Set(current);
      if (!next.delete(fileName)) next.add(fileName);
      return next;
    });
  }, []);
  const openRun = useCallback(
    (fileName: string, run: string, event: React.MouseEvent) =>
      peekOrOpen({ dag: fileName, run }, event),
    [peekOrOpen]
  );
  // The view off screen stays in the DOM, hidden: only the view on screen
  // draws strips.
  const placement = useActivityPlacement();
  const rowActivity = placement.column ? activity : undefined;
  const cardActivity = placement.cards ? activity : undefined;
  const [deleteTargets, setDeleteTargets] = useState<
    components['schemas']['DAGFile'][] | null
  >(null);
  const [isDeleting, setIsDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleteFailures, setDeleteFailures] = useState<Record<string, string>>(
    {}
  );
  const [pendingPins, setPendingPins] = useState<Set<string>>(() => new Set());

  const togglePin = useCallback(
    async (dag: components['schemas']['DAGFile']) => {
      const { fileName } = dag;
      setPendingPins((current) => new Set(current).add(fileName));
      try {
        await onSetDAGPinned(fileName, !dag.pinned);
      } finally {
        setPendingPins((current) => {
          const next = new Set(current);
          next.delete(fileName);
          return next;
        });
      }
    },
    [onSetDAGPinned]
  );
  // A group is pinned when every workflow of it is: pinning it pins them all
  // at once, and unpinning it unpins them all.
  const toggleGroupPin = useCallback(
    async (members: components['schemas']['DAGFile'][]) => {
      const pin = !members.every((dag) => dag.pinned);
      const fileNames = members
        .filter((dag) => dag.pinned !== pin)
        .map((dag) => dag.fileName);
      if (fileNames.length === 0) return;
      setPendingPins((current) => new Set([...current, ...fileNames]));
      try {
        await onSetDAGsPinned(fileNames, pin);
      } finally {
        setPendingPins((current) => {
          const next = new Set(current);
          fileNames.forEach((fileName) => next.delete(fileName));
          return next;
        });
      }
    },
    [onSetDAGsPinned]
  );
  const [expanded, setExpanded] = useState<ExpandedState>(() => {
    try {
      const saved = localStorage.getItem('dagu_dag_table_expanded');
      return saved ? JSON.parse(saved) : {};
    } catch {
      return {};
    }
  });

  const handleExpandedChange = useCallback(
    (updater: Updater<ExpandedState>) => {
      setExpanded((prev) => {
        const next = typeof updater === 'function' ? updater(prev) : updater;
        localStorage.setItem('dagu_dag_table_expanded', JSON.stringify(next));
        return next;
      });
    },
    []
  );

  // Gather the workflows by group, or list them flat with the group before the
  // name. The choice stays in the browser.
  const [groupByGroup, setGroupByGroup] = useState<boolean>(() => {
    try {
      return localStorage.getItem('dagu_workflows_group_by_group') !== 'false';
    } catch {
      return true;
    }
  });
  const handleGroupByGroupChange = useCallback((checked: boolean) => {
    setGroupByGroup(checked);
    try {
      localStorage.setItem('dagu_workflows_group_by_group', String(checked));
    } catch {
      // The choice then lasts until the page closes.
    }
  }, []);

  useEffect(() => {
    const loadedFileNames = new Set(dags.map((dag) => dag.fileName));
    setDeleteSelection((current) => {
      const next = new Set(
        [...current].filter((fileName) => loadedFileNames.has(fileName))
      );
      return next.size === current.size ? current : next;
    });
  }, [dags]);

  const toggleDeleteSelection = useCallback((fileName: string) => {
    setDeleteSelection((current) => {
      const next = new Set(current);
      if (next.has(fileName)) {
        next.delete(fileName);
      } else {
        next.add(fileName);
      }
      return next;
    });
  }, []);

  // The checkbox of a group selects all its workflows, or clears them.
  const toggleGroupDeleteSelection = useCallback((fileNames: string[]) => {
    setDeleteSelection((current) => {
      const next = new Set(current);
      if (fileNames.every((fileName) => next.has(fileName))) {
        fileNames.forEach((fileName) => next.delete(fileName));
      } else {
        fileNames.forEach((fileName) => next.add(fileName));
      }
      return next;
    });
  }, []);

  const getFilteredDAGFileNames = useCallback(
    () =>
      (tableInstanceRef.current?.getFilteredRowModel().flatRows ?? [])
        .filter((row) => (row.original as Data).kind === ItemKind.DAG)
        .map((row) => (row.original as DAGRow).dag.fileName),
    []
  );

  const toggleAllDeleteSelection = useCallback(
    (checked: boolean) => {
      const filteredFileNames = getFilteredDAGFileNames();
      setDeleteSelection((current) => {
        const next = new Set(current);
        filteredFileNames.forEach((fileName) => {
          if (checked) {
            next.add(fileName);
          } else {
            next.delete(fileName);
          }
        });
        return next;
      });
    },
    [getFilteredDAGFileNames]
  );

  const getDeleteSelectionState = useCallback((): boolean | 'indeterminate' => {
    const filteredFileNames = getFilteredDAGFileNames();
    const selectedCount = filteredFileNames.filter((fileName) =>
      deleteSelection.has(fileName)
    ).length;
    if (selectedCount === 0) {
      return false;
    }
    return selectedCount === filteredFileNames.length ? true : 'indeterminate';
  }, [deleteSelection, getFilteredDAGFileNames]);

  const handleDeleteSubmit = useCallback(async () => {
    if (!deleteTargets || deleteTargets.length === 0) {
      return;
    }
    setIsDeleting(true);
    setDeleteError(null);
    setDeleteFailures({});
    try {
      const results = await onDeleteDAGs(
        deleteTargets.map((dag) => dag.fileName)
      );
      const failedFileNames = new Set(
        results
          .filter((result) => result.error)
          .map((result) => result.fileName)
      );
      const deletedFileNames = new Set(
        results
          .filter((result) => !result.error)
          .map((result) => result.fileName)
      );
      setDeleteSelection(
        (current) =>
          new Set(
            [...current].filter((fileName) => !deletedFileNames.has(fileName))
          )
      );
      if (failedFileNames.size > 0) {
        setDeleteTargets(
          deleteTargets.filter((dag) => failedFileNames.has(dag.fileName))
        );
        setDeleteFailures(
          Object.fromEntries(
            results.flatMap((result) =>
              result.error ? [[result.fileName, result.error]] : []
            )
          )
        );
        setDeleteError(
          failedFileNames.size === 1
            ? 'The workflow could not be deleted. Review the error below and retry.'
            : 'Some workflows could not be deleted. Review the errors below and retry.'
        );
      } else {
        setDeleteTargets(null);
      }
    } catch (error) {
      setDeleteError(
        error instanceof Error ? error.message : 'Failed to delete workflows'
      );
    } finally {
      setIsDeleting(false);
    }
  }, [deleteTargets, onDeleteDAGs]);

  useEffect(() => {
    const nameFilter = columnFilters.find((f) => f.id === 'Name');

    // Combine searchText and searchLabels for the Name filter
    const combinedFilter =
      searchText || searchLabels.length > 0
        ? { searchText, labels: searchLabels }
        : '';
    const currentValue = nameFilter?.value || '';
    const currentFilterKey = JSON.stringify(currentValue);
    const combinedFilterKey = JSON.stringify(combinedFilter);

    let updated = false;
    const newFilters = [...columnFilters];

    if (combinedFilterKey !== currentFilterKey) {
      const idx = newFilters.findIndex((f) => f.id === 'Name');
      if (combinedFilter) {
        if (idx > -1) newFilters[idx] = { id: 'Name', value: combinedFilter };
        else newFilters.push({ id: 'Name', value: combinedFilter });
      } else if (idx > -1) {
        newFilters.splice(idx, 1);
      }
      updated = true;
    }

    if (updated) {
      setColumnFilters(newFilters);
    }
  }, [searchText, searchLabels, columnFilters]);

  // Handler for clicking a label to add it to the filter
  const handleLabelClick = useCallback(
    (label: string) => {
      const normalizedLabel = label.toLowerCase();
      if (isWorkspaceLabel(normalizedLabel)) {
        return;
      }
      if (!searchLabels.includes(normalizedLabel)) {
        handleSearchLabelsChange([...searchLabels, normalizedLabel]);
      }
    },
    [searchLabels, handleSearchLabelsChange]
  );

  // The loaded workflows of each group, in the server order. A Map keeps a
  // group name such as "constructor" apart from the properties of Object.
  const groupMembers = useMemo(() => {
    const members = new Map<string, components['schemas']['DAGFile'][]>();
    dags.forEach((dag) => {
      const groupName = dag.dag.group;
      if (!groupName) return;
      const list = members.get(groupName);
      if (list) list.push(dag);
      else members.set(groupName, [dag]);
    });
    return members;
  }, [dags]);

  // The list holds every workflow of each group: no filter narrows it, typed
  // or in the request, and no page is left to load. While a request loads,
  // the rows are still those of the previous request.
  const hasWholeGroups =
    !searchText &&
    searchLabels.length === 0 &&
    !activeOnly &&
    !isRequestFiltered &&
    !isLoading &&
    (resultCount === undefined || dags.length >= resultCount);

  // Transform the flat list of DAGs into a hierarchical structure with groups.
  // The order: pinned groups, pinned workflows, groups, workflows. The rows
  // keep the server order.
  const data = useMemo(() => {
    const sortedDags = [...dags];

    // Not gathered by group: pinned workflows lead, then the others, flat. By
    // name, the order follows what the row shows: the group, then the name.
    if (!groupByGroup) {
      if (sortField === 'name') {
        const direction = sortOrder === 'desc' ? -1 : 1;
        const label = (dag: (typeof sortedDags)[number]) =>
          `${dag.dag.group ? `${dag.dag.group} / ` : ''}${dag.dag.name}`.toLowerCase();
        sortedDags.sort((a, b) => {
          if (a.pinned !== b.pinned) return a.pinned ? -1 : 1;
          return direction * label(a).localeCompare(label(b));
        });
      }
      return [
        ...sortedDags.filter((dag) => dag.pinned),
        ...sortedDags.filter((dag) => !dag.pinned),
      ].map((dag): Data => ({ kind: ItemKind.DAG, name: dag.dag.name, dag }));
    }

    // A group whose workflows are all pinned stays a group, and leads the
    // list; any other pinned workflow leads as a flat row. A partly loaded
    // group never counts as pinned, so its pinned workflows keep their rows.
    const pinnedGroupNames = new Set(
      hasWholeGroups
        ? [...groupMembers]
            .filter(([, members]) => members.every((dag) => dag.pinned))
            .map(([name]) => name)
        : []
    );
    const inPinnedGroup = (dag: (typeof sortedDags)[number]) =>
      !!dag.dag.group && pinnedGroupNames.has(dag.dag.group);

    const pinnedRows: Data[] = sortedDags
      .filter((dag) => dag.pinned && !inPinnedGroup(dag))
      .map((dag) => ({ kind: ItemKind.DAG, name: dag.dag.name, dag }));
    const unpinnedDags = sortedDags.filter(
      (dag) => !dag.pinned || inPinnedGroup(dag)
    );

    const groups = new Map<string, Data>();
    unpinnedDags.forEach((dag) => {
      const groupName = dag.dag.group;
      if (groupName) {
        let group = groups.get(groupName);
        if (!group) {
          group = { kind: ItemKind.Group, name: groupName, subRows: [] };
          groups.set(groupName, group);
        }
        group.subRows!.push({
          kind: ItemKind.DAG,
          name: dag.dag.name,
          dag: dag,
        });
      }
    });

    const groupRows = [...groups.values()];
    const hierarchicalData: Data[] = [
      ...groupRows.filter((group) => pinnedGroupNames.has(group.name)),
      ...pinnedRows,
      ...groupRows.filter((group) => !pinnedGroupNames.has(group.name)),
    ];
    // Add DAGs without a group
    unpinnedDags
      .filter((dag) => !dag.dag.group)
      .forEach((dag) => {
        hierarchicalData.push({
          kind: ItemKind.DAG,
          name: dag.dag.name,
          dag: dag,
        });
      });
    return hierarchicalData;
  }, [dags, groupByGroup, groupMembers, hasWholeGroups, sortField, sortOrder]);

  const appBarContext = useContext(AppBarContext);
  // One read of the service states for all rows; it starts when a service
  // DAG is on the page.
  const hasServiceDAG = dags.some((dag) => isServiceDAG(dag.dag));
  const { serviceDAGs } = useServiceDAGs({ enabled: hasServiceDAG });
  const services = useMemo(
    () => new Map(serviceDAGs.map((dag) => [dag.fileName, dag])),
    [serviceDAGs]
  );

  const instance = useReactTable<Data>({
    data,
    columns,
    // Use stable IDs for persistence
    getRowId: (row) =>
      row.kind === ItemKind.Group
        ? `group:${row.name}`
        : `dag:${(row as DAGRow).dag.fileName}`,
    getSubRows: (row) => row.subRows,
    getCoreRowModel: getCoreRowModel<Data>(),
    manualSorting: true,
    getFilteredRowModel: getFilteredRowModel<Data>(),
    onColumnFiltersChange: setColumnFilters,
    getExpandedRowModel: getExpandedRowModel<Data>(),
    autoResetExpanded: false,
    state: {
      expanded,
      columnFilters,
    },
    onExpandedChange: handleExpandedChange,
    meta: {
      group,
      refreshFn,
      onLabelClick: handleLabelClick,
      getDeleteSelectionState,
      isDeleteSelected: (fileName) => deleteSelection.has(fileName),
      onToggleDeleteSelection: toggleDeleteSelection,
      onToggleGroupDeleteSelection: toggleGroupDeleteSelection,
      onToggleAllDeleteSelection: toggleAllDeleteSelection,
      canEditDAG,
      canPinDAG,
      isPinPending: (fileName) => pendingPins.has(fileName),
      onTogglePin: (dag) => void togglePin(dag),
      onToggleGroupPin: (dags) => void toggleGroupPin(dags),
      groupMembers,
      showGroupPrefix: !groupByGroup,
      isActivityExpanded: (fileName) => expandedActivity.has(fileName),
      onToggleActivity: toggleActivity,
      onOpenRun: openRun,
      services,
      activity: rowActivity,
    },
  });
  // The peek steps through the workflows that the table shows, in their
  // order: the rows of the open groups too.
  usePeekSequence(
    instance
      .getRowModel()
      .rows.filter((row) => row.original?.kind === ItemKind.DAG)
      .map((row) => ({ dag: (row.original as DAGRow).dag.fileName }))
  );

  tableInstanceRef.current = instance as ReturnType<typeof useReactTable>;
  const pinnedRowCount = countLeadingPinnedRows(instance.getRowModel().rows);
  const filteredDAGFileNames = new Set(getFilteredDAGFileNames());
  const selectedDAGsForDelete = dags.filter(
    (dag) =>
      filteredDAGFileNames.has(dag.fileName) &&
      deleteSelection.has(dag.fileName)
  );

  const panelWidth = useContext(PanelWidthContext);

  const useCardView = panelWidth !== null && panelWidth < CARD_VIEW_THRESHOLD;
  const activityColumn = activityColumnClasses(
    panelWidth,
    activity !== undefined
  );

  const { data: uniqueLabels } = useQuery('/dags/labels', {
    params: {
      query: {
        remoteNode: appBarContext?.selectedRemoteNode || 'local',
      },
    },
  });
  const availableLabels = withoutWorkspaceLabels(uniqueLabels?.labels ?? []);
  const activeWorkflowViewName = workflowViews.find(
    (view) => view.id === activeWorkflowViewId
  )?.name;
  const isPristineAllView =
    isAllWorkflowsView &&
    !searchText &&
    searchLabels.length === 0 &&
    !activeOnly;
  const emptyState = activeWorkflowViewName
    ? {
        icon: '🔍',
        heading: 'No workflows found',
        description:
          'No workflows match the “{view}” view. Try adjusting its filters or show all workflows.',
        descriptionValues: { view: activeWorkflowViewName },
      }
    : isPristineAllView
      ? {
          icon: '🌱',
          heading: 'No workflows yet',
          description: 'Create your first workflow to get started.',
        }
      : {
          icon: '🔍',
          heading: 'No workflows found',
          description:
            'There are no workflows matching your current filters. Try adjusting your search criteria or labels.',
        };

  return (
    <div className="space-y-2">
      {/* Search, Filter and Pagination Controls */}
      <div
        data-testid="workflow-controls"
        className={`mb-4 space-y-2 ${
          isLoading ? 'opacity-70 pointer-events-none' : ''
        }`}
      >
        <div className="flex flex-wrap items-center gap-2">
          <ViewSelector
            kind="workflow"
            views={workflowViews}
            activeViewId={activeWorkflowViewId}
            defaultViewId={defaultWorkflowViewId}
            isAllView={isAllWorkflowsView}
            isActiveViewEdited={isWorkflowViewEdited}
            filtersAlreadySaved={areWorkflowFiltersSaved}
            canManageViews={canManageWorkflowViews}
            error={workflowViewError}
            onSelectView={onSelectWorkflowView}
            onShowAll={onShowAllWorkflows}
            onResetView={onResetWorkflowView}
            onSaveView={onSaveWorkflowView}
            onUpdateView={onUpdateWorkflowView}
            onSetDefault={onSetDefaultWorkflowView}
            onSetPinned={onSetPinnedWorkflowView}
            onDeleteView={onDeleteWorkflowView}
            onRenameView={onRenameWorkflowView}
          />

          {/* Search input */}
          <I18nProps>
            <Input
              type="text"
              placeholder="Filter by workflow name..."
              value={searchText}
              onChange={(e) => handleSearchTextChange(e.target.value)}
              className="w-[180px]"
            />
          </I18nProps>
          <Button asChild variant="outline" className="px-4 font-medium">
            <Link to={buildGrepSearchUrl(searchText)}>
              <Search className="mr-1.5 h-4 w-4" />
              <I18nText text={'Grep'} />
            </Link>
          </Button>

          {/* Label filter */}
          <I18nProps>
            <LabelCombobox
              selectedLabels={searchLabels}
              onLabelsChange={handleSearchLabelsChange}
              availableLabels={availableLabels}
              placeholder="Filter by labels..."
              className="h-9 min-w-[150px] max-w-[180px]"
            />
          </I18nProps>

          <label className="flex h-9 cursor-pointer items-center gap-2 rounded-md border border-input bg-card px-3 text-sm font-medium text-foreground shadow-sm transition-colors hover:border-border-strong hover:bg-muted">
            <span>
              <I18nText text={'Active only'} />
            </span>
            <I18nProps>
              <Switch
                checked={activeOnly}
                onCheckedChange={handleActiveOnlyChange}
                aria-label="Active only"
                className="focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
              />
            </I18nProps>
          </label>

          <label className="flex h-9 cursor-pointer items-center gap-2 rounded-md border border-input bg-card px-3 text-sm font-medium text-foreground shadow-sm transition-colors hover:border-border-strong hover:bg-muted">
            <span>
              <I18nText text={'Group'} />
            </span>
            <I18nProps>
              <Switch
                checked={groupByGroup}
                onCheckedChange={handleGroupByGroupChange}
                aria-label="Gather the workflows by group"
                className="focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
              />
            </I18nProps>
          </label>

          {canDeleteDAGs && selectedDAGsForDelete.length > 0 && (
            <Button
              type="button"
              variant="destructive"
              // Last in the row, floated to the right, apart from the views buttons.
              className="order-last ml-auto"
              disabled={isDeleting}
              onClick={() => {
                setDeleteError(null);
                setDeleteFailures({});
                setDeleteTargets(selectedDAGsForDelete);
              }}
            >
              <Trash2 className="h-4 w-4" />
              <I18nText text={'Delete ('} />
              {selectedDAGsForDelete.length})
            </Button>
          )}

          {/* Pagination */}
          {pagination && (
            <div className="flex-shrink-0">
              <DAGPagination
                totalPages={pagination.totalPages}
                page={pagination.page}
                pageChange={pagination.pageChange}
                onPageLimitChange={pagination.onPageLimitChange}
                pageLimit={pagination.pageLimit}
              />
            </div>
          )}
        </div>

        {workflowViewError && (
          <p role="alert" className="text-xs text-destructive">
            {workflowViewError}
          </p>
        )}

        {resultCount !== undefined && (
          <div className="text-xs text-muted-foreground">
            {ts(resultCount === 1 ? '{count} workflow' : '{count} workflows', {
              count: resultCount.toLocaleString(),
            })}
          </div>
        )}

        {useCardView && onSortChange && (
          <div className="flex flex-wrap items-center gap-2 pt-1 pl-1">
            <div className="flex flex-wrap items-center gap-2 min-w-0">
              <span className="text-xs font-medium text-muted-foreground whitespace-nowrap">
                <I18nText text={'Sort'} />
              </span>
              <Select
                value={sortField}
                onValueChange={(value) =>
                  onSortChange(
                    value,
                    value === sortField ? sortOrder : getDefaultSortOrder(value)
                  )
                }
              >
                <I18nProps>
                  <SelectTrigger
                    className="h-8 min-w-[132px] max-w-full text-xs"
                    aria-label="Sort DAG cards"
                  >
                    <I18nProps>
                      <SelectValue placeholder="Sort by">
                        <I18nText text={getCardSortLabel(sortField)} />
                      </SelectValue>
                    </I18nProps>
                  </SelectTrigger>
                </I18nProps>
                <SelectContent>
                  {cardSortOptions.map((option) => {
                    const disabled =
                      runSortDisabled && isRunSortField(option.value);
                    const item = (
                      <SelectItem
                        key={option.value}
                        value={option.value}
                        className="text-xs"
                        disabled={disabled}
                      >
                        <I18nText text={option.label} />
                      </SelectItem>
                    );
                    return disabled ? (
                      <Tooltip key={option.value}>
                        <TooltipTrigger asChild>
                          <div className="cursor-not-allowed">{item}</div>
                        </TooltipTrigger>
                        <RunSortDisabledTooltip />
                      </Tooltip>
                    ) : (
                      item
                    );
                  })}
                </SelectContent>
              </Select>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-7 px-2 text-xs shrink-0"
                onClick={() =>
                  onSortChange(sortField, sortOrder === 'asc' ? 'desc' : 'asc')
                }
                aria-label={ts(
                  sortOrder === 'asc'
                    ? 'Switch to descending sort'
                    : 'Switch to ascending sort'
                )}
              >
                {sortOrder === 'asc' ? (
                  <ArrowUp className="h-3.5 w-3.5" />
                ) : (
                  <ArrowDown className="h-3.5 w-3.5" />
                )}
                <span className="ml-1">
                  {sortOrder === 'asc' ? (
                    <I18nText text={'Asc'} />
                  ) : (
                    <I18nText text={'Desc'} />
                  )}
                </span>
              </Button>
            </div>
          </div>
        )}
      </div>

      {/* Desktop Table View - Hidden on mobile or when panel is narrow */}
      <div
        className={`w-full overflow-hidden ${useCardView ? 'hidden' : 'hidden md:block'}`}
      >
        <Table
          className={`w-full text-xs ${isLoading ? 'opacity-70' : ''}`}
          style={{ tableLayout: 'fixed' }}
        >
          {/* Column widths: Select 24px, Pin 24px, Name 28%, LastRun 88px, Activity the rest when shown, Schedule 220px, Webhook 88px when shown, Actions ACTIONS_WIDTH */}
          <colgroup>
            {canDeleteDAGs && <col style={{ width: '24px' }} />}
            <col style={{ width: '24px' }} />
            <col style={{ width: '28%' }} />
            <col style={{ width: '88px' }} />
            <col className={activityColumn.col} />
            <col style={{ width: '220px' }} />
            {showWebhooks && <col style={{ width: '88px' }} />}
            <col style={{ width: `${ACTIONS_WIDTH}px` }} />
          </colgroup>
          <TableHeader>
            {instance.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    className={`py-1 text-muted-foreground text-xs overflow-hidden ${header.column.id === 'Select' || header.column.id === 'Pin' ? 'px-0' : 'px-2'} ${header.column.id === 'Activity' ? activityColumn.cell : ''}`}
                  >
                    {header.isPlaceholder ? null : (
                      <div>
                        {' '}
                        {/* Wrap header content */}
                        {columnToSortField[header.column.id] ? (
                          runSortDisabled &&
                          isRunSortField(
                            columnToSortField[header.column.id]
                          ) ? (
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <span className="cursor-not-allowed">
                                  {flexRender(
                                    header.column.columnDef.header,
                                    header.getContext()
                                  )}
                                </span>
                              </TooltipTrigger>
                              <RunSortDisabledTooltip />
                            </Tooltip>
                          ) : (
                            <SortableHeader
                              column={header.column}
                              currentSort={sortField}
                              currentOrder={sortOrder}
                              onSort={onSortChange}
                            >
                              {flexRender(
                                header.column.columnDef.header,
                                header.getContext()
                              )}
                            </SortableHeader>
                          )
                        ) : (
                          flexRender(
                            header.column.columnDef.header,
                            header.getContext()
                          )
                        )}
                      </div>
                    )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {instance.getRowModel().rows.length ? (
              instance.getRowModel().rows.map((row, index) => {
                // For DAG rows, make the entire row clickable
                const isDAGRow = row.original?.kind === ItemKind.DAG;
                const endsPinnedSection =
                  index === pinnedRowCount - 1 &&
                  index < instance.getRowModel().rows.length - 1;
                const isDeleteSelected =
                  isDAGRow &&
                  'dag' in row.original &&
                  deleteSelection.has((row.original as DAGRow).dag.fileName);
                const rowShown =
                  isDAGRow &&
                  'dag' in row.original &&
                  isShown({ dag: (row.original as DAGRow).dag.fileName });
                // Type guard to ensure we only access dag property when it exists

                // The table off screen reads no runs: rowActivity is unset.
                // A service DAG has no chevron, so it shows no panel either.
                const recentRunsOf =
                  rowActivity !== undefined &&
                  isDAGRow &&
                  'dag' in row.original &&
                  !isServiceDAG((row.original as DAGRow).dag.dag) &&
                  expandedActivity.has((row.original as DAGRow).dag.fileName)
                    ? (row.original as DAGRow).dag.fileName
                    : null;

                return (
                  <React.Fragment key={row.id}>
                    <TableRow
                      data-state={isDeleteSelected ? 'selected' : undefined}
                      data-peek-shown={rowShown || undefined}
                      className={`text-[0.8125rem] ${rowShown ? PEEK_SHOWN_ROW : ''} ${
                        row.original?.kind === ItemKind.Group
                          ? 'bg-muted/50 font-semibold cursor-pointer hover:bg-muted/70'
                          : 'cursor-pointer hover:bg-muted/50'
                      } ${isDAGRow && 'dag' in row.original && (row.original as DAGRow).dag.latestDAGRun?.status === Status.Running ? 'animate-running-row' : ''} group/row ${isDAGRow ? 'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring' : ''} ${endsPinnedSection && recentRunsOf === null ? 'border-b-2' : ''}`}
                      tabIndex={isDAGRow ? 0 : undefined}
                      onKeyDown={
                        isDAGRow && 'dag' in row.original
                          ? onOwnEnter((e) =>
                              peekOrOpen(
                                { dag: (row.original as DAGRow).dag.fileName },
                                e
                              )
                            )
                          : undefined
                      }
                      onClick={(e) => {
                        // Handle group row clicks - toggle expanded state
                        if ((row.original as Data)?.kind === ItemKind.Group) {
                          row.toggleExpanded();
                        }
                        // Handle DAG row clicks - show the peek, or a new tab with Cmd/Ctrl
                        else if (isDAGRow && 'dag' in row.original) {
                          const dagRow = row.original as DAGRow;
                          peekOrOpen({ dag: dagRow.dag.fileName }, e);
                        }
                      }}
                    >
                      {row.getVisibleCells().map((cell) => (
                        <TableCell
                          key={cell.id}
                          className={`py-1 align-middle overflow-hidden truncate ${cell.column.id === 'Select' || cell.column.id === 'Pin' ? 'px-0' : 'px-2'} ${cell.column.id === 'Activity' ? activityColumn.cell : ''}`}
                        >
                          {flexRender(
                            cell.column.columnDef.cell,
                            cell.getContext()
                          )}
                        </TableCell>
                      ))}
                    </TableRow>
                    {recentRunsOf !== null && (
                      <TableRow
                        className={`bg-muted/20 hover:bg-muted/20 ${endsPinnedSection ? 'border-b-2' : ''}`}
                      >
                        <TableCell
                          colSpan={instance.getVisibleLeafColumns().length}
                          className="px-2 py-1 pl-12"
                        >
                          <DAGRecentRuns
                            fileName={recentRunsOf}
                            activity={rowActivity?.get(recentRunsOf)}
                            onOpenRun={openRun}
                          />
                        </TableCell>
                      </TableRow>
                    )}
                  </React.Fragment>
                );
              })
            ) : (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className="h-64 text-center"
                >
                  <div className="flex flex-col items-center justify-center py-8">
                    <WorkflowsEmptyState
                      {...emptyState}
                      showAllButton={!isAllWorkflowsView}
                      onShowAllWorkflows={onShowAllWorkflows}
                    />
                  </div>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {/* Card View - Visible on mobile or when panel is narrow */}
      <div
        data-testid="workflow-card-view"
        className={`space-y-2 ${useCardView ? 'block' : 'md:hidden'}`}
      >
        {instance.getRowModel().rows.length ? (
          instance.getRowModel().rows.map((row, index) => {
            // Render group rows with collapsible header
            if (row.original?.kind === ItemKind.Group) {
              const groupRow = row.original as GroupRow;
              const isExpanded = row.getIsExpanded();
              // The rows of an unfolded group follow it in the row model.
              const lastIndex = index + (isExpanded ? row.subRows.length : 0);
              const endsPinnedSection =
                lastIndex === pinnedRowCount - 1 &&
                lastIndex < instance.getRowModel().rows.length - 1;

              return (
                <div key={row.id} className="space-y-1.5">
                  {/* Group Header */}
                  <div
                    className="flex items-center justify-between px-3 py-2 bg-muted/70 rounded-md border border-muted-foreground/20 cursor-pointer active:bg-muted"
                    onClick={() => row.toggleExpanded()}
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      <div className="flex-shrink-0">
                        {isExpanded ? (
                          <ChevronUp className="h-4 w-4 text-muted-foreground" />
                        ) : (
                          <ChevronDown className="h-4 w-4 text-muted-foreground" />
                        )}
                      </div>
                      <span className="text-xs font-semibold text-muted-foreground truncate">
                        {groupRow.name}
                      </span>
                    </div>
                    <Badge
                      variant="secondary"
                      className="text-xs px-1.5 py-0 h-4 flex-shrink-0"
                    >
                      {row.subRows?.length || 0}
                    </Badge>
                  </div>

                  {/* Group Members - only shown when expanded */}
                  {isExpanded && row.subRows && row.subRows.length > 0 && (
                    <div className="space-y-1.5 pl-2 border-l-2 border-muted-foreground/20 ml-3">
                      {row.subRows.map((subRow) => {
                        if (
                          subRow.original?.kind === ItemKind.DAG &&
                          'dag' in subRow.original
                        ) {
                          const dagRow = subRow.original as DAGRow;
                          return (
                            <DAGCard
                              key={subRow.id}
                              dag={dagRow.dag}
                              onOpen={(e) =>
                                peekOrOpen({ dag: dagRow.dag.fileName }, e)
                              }
                              onLabelClick={handleLabelClick}
                              refreshFn={refreshFn}
                              canDeleteDAGs={canDeleteDAGs}
                              isDeleteSelected={deleteSelection.has(
                                dagRow.dag.fileName
                              )}
                              onToggleDeleteSelection={toggleDeleteSelection}
                              canEdit={canEditDAG(dagRow.dag)}
                              canPin={canPinDAG(dagRow.dag)}
                              isPinPending={pendingPins.has(
                                dagRow.dag.fileName
                              )}
                              onTogglePin={(dag) => void togglePin(dag)}
                              isShown={isShown({ dag: dagRow.dag.fileName })}
                              service={services.get(dagRow.dag.fileName)}
                              activity={cardActivity?.get(dagRow.dag.fileName)}
                              showActivity={cardActivity !== undefined}
                              className="ml-2"
                            />
                          );
                        }
                        return null;
                      })}
                    </div>
                  )}
                  {endsPinnedSection && (
                    <div
                      role="separator"
                      className="border-t-2 border-border"
                    />
                  )}
                </div>
              );
            }

            // Render standalone DAG rows (not in a group)
            // Skip if this row has a parent (it's already rendered within a group)
            if (
              row.original?.kind === ItemKind.DAG &&
              'dag' in row.original &&
              row.depth === 0
            ) {
              const dagRow = row.original as DAGRow;
              const endsPinnedSection =
                index === pinnedRowCount - 1 &&
                index < instance.getRowModel().rows.length - 1;
              return (
                <React.Fragment key={row.id}>
                  <DAGCard
                    dag={dagRow.dag}
                    onOpen={(e) => peekOrOpen({ dag: dagRow.dag.fileName }, e)}
                    onLabelClick={handleLabelClick}
                    refreshFn={refreshFn}
                    canDeleteDAGs={canDeleteDAGs}
                    isDeleteSelected={deleteSelection.has(dagRow.dag.fileName)}
                    onToggleDeleteSelection={toggleDeleteSelection}
                    canEdit={canEditDAG(dagRow.dag)}
                    canPin={canPinDAG(dagRow.dag)}
                    isPinPending={pendingPins.has(dagRow.dag.fileName)}
                    onTogglePin={(dag) => void togglePin(dag)}
                    isShown={isShown({ dag: dagRow.dag.fileName })}
                    service={services.get(dagRow.dag.fileName)}
                    activity={cardActivity?.get(dagRow.dag.fileName)}
                    showActivity={cardActivity !== undefined}
                    showGroup={!groupByGroup}
                  />
                  {endsPinnedSection && (
                    <div
                      role="separator"
                      className="border-t-2 border-border"
                    />
                  )}
                </React.Fragment>
              );
            }

            return null;
          })
        ) : (
          <div className="flex flex-col items-center justify-center py-12 px-4 border rounded-md bg-card">
            <WorkflowsEmptyState
              {...emptyState}
              showAllButton={!isAllWorkflowsView}
              onShowAllWorkflows={onShowAllWorkflows}
            />
          </div>
        )}
      </div>

      <ConfirmDialog
        title={
          deleteTargets?.length === 1
            ? ts('Delete workflow?')
            : ts('Delete {count} workflows?', {
                count: deleteTargets?.length ?? 0,
              })
        }
        buttonText={ts(isDeleting ? 'Deleting...' : 'Delete')}
        visible={deleteTargets !== null}
        dismissModal={() => {
          if (!isDeleting) {
            setDeleteTargets(null);
            setDeleteError(null);
            setDeleteFailures({});
          }
        }}
        submitDisabled={isDeleting}
        onSubmit={() => void handleDeleteSubmit()}
      >
        <div className="space-y-3 text-sm">
          <p>
            {deleteTargets?.length === 1 ? (
              <I18nText
                text={'The workflow definition file will be removed.'}
              />
            ) : (
              <I18nText
                text={'The selected workflow definition files will be removed.'}
              />
            )}{' '}
            <I18nText
              text={
                'Past run history will be kept. This action cannot be undone.'
              }
            />
          </p>
          <ul className="max-h-48 space-y-1 overflow-y-auto rounded-md border bg-muted/30 p-2">
            {(deleteTargets ?? []).map((dag) => (
              <li key={dag.fileName} className="min-w-0">
                <div className="truncate font-medium">{dag.dag.name}</div>
                {dag.dag.name !== dag.fileName && (
                  <div className="truncate font-mono text-xs text-muted-foreground">
                    {dag.fileName}
                  </div>
                )}
                {deleteFailures[dag.fileName] && (
                  <div className="text-xs text-destructive">
                    {deleteFailures[dag.fileName]}
                  </div>
                )}
              </li>
            ))}
          </ul>
          {deleteError && (
            <p role="alert" className="text-sm text-destructive">
              {deleteError}
            </p>
          )}
        </div>
      </ConfirmDialog>
    </div>
  );
}

export default DAGTable;
