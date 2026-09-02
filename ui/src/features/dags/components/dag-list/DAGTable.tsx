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
  RowData,
  Updater,
  useReactTable,
} from '@tanstack/react-table';
import {
  ArrowDown,
  ArrowUp,
  Calendar,
  ChevronDown,
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
import { Link, useNavigate } from 'react-router-dom';
import { DAGNameInputModal } from '@/components/DAGNameInputModal';
import { components, Status } from '../../../../api/v1/schema';
import type { Config } from '../../../../contexts/ConfigContext';
import dayjs from '../../../../lib/dayjs';
import {
  getScheduleKey,
  getScheduleLabel,
  parseNextRun,
} from '../../../../lib/dagSchedule';
import RelativeTime from '@/components/ui/relative-time';
import StatusChip from '@/components/ui/status-chip';
import Ticker from '@/components/ui/ticker';
import VisuallyHidden from '@/components/ui/visually-hidden';
import { CreateDAGModal, DAGPagination } from '../common';
import DAGActions from '../common/DAGActions';
import LiveSwitch from '../common/LiveSwitch';

declare const getConfig: () => Config;

// Helper to format milliseconds into d/h/m/s
function formatMs(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  const parts: string[] = [];
  if (days > 0) parts.push(`${days}d`);
  if (hours > 0) parts.push(`${hours}h`);
  if (minutes > 0) parts.push(`${minutes}m`);
  // Only show seconds if no larger units or if seconds > 0
  if (secs > 0 || parts.length === 0) parts.push(`${secs}s`);
  return parts.join(' ');
}

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
import type { WorkflowFilterView } from './workflowViews';
import { useQuery } from '../../../../hooks/api';
import { parseLabelParts } from '../../../../lib/utils';
import {
  isWorkspaceLabel,
  withoutWorkspaceLabels,
} from '../../../../lib/workspace';
import { WorkflowViewSelector } from './WorkflowViewSelector';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

// Threshold in pixels below which we switch to card view
// Set higher than table's comfortable minimum width (~700px for all columns)
const CARD_VIEW_THRESHOLD = 700;

// Reusable DAG Card component for mobile and narrow panel views
interface DAGCardProps {
  dag: components['schemas']['DAGFile'];
  isSelected: boolean;
  onSelect: (fileName: string, title: string) => void;
  onLabelClick: (label: string) => void;
  refreshFn: () => void;
  canDeleteDAGs: boolean;
  isDeleteSelected: boolean;
  onToggleDeleteSelection: (fileName: string) => void;
  canRenameDAGs: boolean;
  onRenameDAG: (dag: components['schemas']['DAGFile']) => void;
  onDeleteDAG: (dag: components['schemas']['DAGFile']) => void;
  className?: string;
}

function RenameDAGButton({
  dag,
  onRename,
}: {
  dag: components['schemas']['DAGFile'];
  onRename: (dag: components['schemas']['DAGFile']) => void;
}) {
  return (
    <I18nProps>
      <Button
        type="button"
        variant="secondary"
        size="icon-sm"
        aria-label={`Rename workflow ${dag.dag.name}`}
        title="Rename workflow"
        onClick={() => onRename(dag)}
      >
        <PencilLine className="h-4 w-4" />
      </Button>
    </I18nProps>
  );
}

function DeleteDAGButton({
  dag,
  onDelete,
}: {
  dag: components['schemas']['DAGFile'];
  onDelete: (dag: components['schemas']['DAGFile']) => void;
}) {
  return (
    <I18nProps>
      <Button
        type="button"
        variant="secondary"
        size="icon-sm"
        className="text-muted-foreground hover:border-destructive/40 hover:bg-destructive/10 hover:text-destructive focus-visible:text-destructive"
        aria-label={`Delete workflow ${dag.dag.name}`}
        title="Delete workflow"
        onClick={() => onDelete(dag)}
      >
        <Trash2 className="h-4 w-4" />
      </Button>
    </I18nProps>
  );
}

function DAGCard({
  dag,
  isSelected,
  onSelect,
  onLabelClick,
  refreshFn,
  canDeleteDAGs,
  isDeleteSelected,
  onToggleDeleteSelection,
  canRenameDAGs,
  onRenameDAG,
  onDeleteDAG,
  className = '',
}: DAGCardProps) {
  const fileName = dag.fileName;
  const title = dag.dag.name;
  const status = dag.latestDAGRun?.status;
  const statusLabel = dag.latestDAGRun?.statusLabel;
  const labels = withoutWorkspaceLabels(dag.dag.labels ?? dag.dag.tags ?? []);
  const description = dag.dag.description;
  const schedules = dag.dag.schedule || [];
  const hasSchedule = schedules.length > 0;
  const nextRun = parseNextRun(dag.nextRun);

  const handleCardClick = (e: React.MouseEvent) => {
    if (e.metaKey || e.ctrlKey) {
      window.open(`/dags/${fileName}`, '_blank');
    } else {
      onSelect(fileName, title);
    }
  };

  return (
    <div
      className={`card-obsidian p-2.5 cursor-pointer hover:bg-white/[0.05] transition-all duration-300 ${isSelected ? 'shadow-[inset_3px_0_0_0_var(--primary)] bg-white/[0.04]' : ''} ${status === Status.Running ? 'animate-running-row' : ''} ${className}`}
      onClick={handleCardClick}
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
          <Link
            to={`/dags/${fileName}`}
            className="font-medium text-xs truncate flex-1 min-w-0 hover:underline"
            onClick={(event) => event.stopPropagation()}
          >
            {title}
          </Link>
        </div>
        <StatusChip status={status} size="xs">
          {statusLabel}
        </StatusChip>
      </div>

      {/* Description */}
      {description && (
        <div className="text-xs text-muted-foreground mb-1.5 line-clamp-1">
          {description}
        </div>
      )}

      {/* Schedule & Last Run */}
      <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground mb-1.5">
        {schedules.map((schedule, idx) => (
          <Badge
            key={getScheduleKey(schedule, idx)}
            variant="outline"
            className={`text-xs font-normal px-1 py-0 h-3 normal-case tracking-normal ${schedule.kind === 'at' ? 'border-0' : ''}`}
          >
            {getScheduleLabel(schedule)}
          </Badge>
        ))}
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

      {hasSchedule && !dag.suspended && (
        <div className="text-xs text-muted-foreground mb-1.5 leading-tight">
          {nextRun ? (
            <Ticker intervalMs={1000}>
              {() => {
                const ms = nextRun.getTime() - new Date().getTime();
                if (ms <= 0) {
                  return (
                    <span>
                      <I18nText text={'Due now'} />
                    </span>
                  );
                }
                return (
                  <span>
                    <I18nText text={'Run in'} /> {formatMs(ms)}
                  </span>
                );
              }}
            </Ticker>
          ) : (
            <span>
              <I18nText text={'No upcoming run'} />
            </span>
          )}
        </div>
      )}

      {/* Labels */}
      {labels.length > 0 && (
        <div className="flex flex-wrap gap-0.5 mb-1.5">
          {[...labels]
            .sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()))
            .map((label) => (
              <Badge
                key={label}
                variant="outline"
                className="text-xs px-1 py-0 h-3 rounded-sm border-primary/30 bg-primary/10 text-primary"
                onClick={(e) => {
                  e.stopPropagation();
                  onLabelClick(label);
                }}
              >
                <div className="h-1 w-1 rounded-full bg-primary/70 mr-0.5"></div>
                {label}
              </Badge>
            ))}
        </div>
      )}

      {/* Actions row: LiveSwitch + DAGActions */}
      <div className="flex flex-wrap items-center justify-between gap-1 pt-1.5 border-t border-border/50 min-w-0">
        <div
          className={`flex items-center gap-1 flex-shrink-0 min-w-0 ${!hasSchedule ? 'opacity-40 pointer-events-none' : ''}`}
          onClick={(e) => e.stopPropagation()}
        >
          <LiveSwitch dag={dag} refresh={refreshFn} />
          <span className="text-xs text-muted-foreground truncate">
            {dag.suspended ? (
              <I18nText text={'Suspended'} />
            ) : hasSchedule ? (
              <I18nText text={'Live'} />
            ) : (
              <I18nText text={'No schedule'} />
            )}
          </span>
        </div>
        <div
          className="flex flex-shrink-0 items-center gap-1"
          onClick={(e) => e.stopPropagation()}
        >
          <DAGActions
            dag={dag.dag}
            status={dag.latestDAGRun}
            fileName={fileName}
            label={false}
            refresh={refreshFn}
          />
          {canRenameDAGs && (
            <RenameDAGButton dag={dag} onRename={onRenameDAG} />
          )}
          {canDeleteDAGs && (
            <DeleteDAGButton dag={dag} onDelete={onDeleteDAG} />
          )}
        </div>
      </div>
    </div>
  );
}

/**
 * Props for the DAGTable component
 */
type Props = {
  /** List of DAG files to display */
  dags: components['schemas']['DAGFile'][];
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
  /** Whether the current user can mutate shared views in this scope */
  canManageWorkflowViews: boolean;
  /** Whether the current user can delete workflows in this scope */
  canDeleteDAGs: boolean;
  /** Whether the current user can rename workflows in this scope */
  canRenameDAGs: boolean;
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
  onDeleteDAGs: (fileNames: string[]) => Promise<DAGDeleteResult[]>;
  onRenameDAG: (fileName: string, newFileName: string) => Promise<void>;
  /** Total workflows matching the server-side filters */
  resultCount?: number;
  /** Currently selected DAG file name */
  selectedDAG?: string | null;
  /** Handler for DAG selection changes */
  onSelectDAG?: (fileName: string, title: string) => void;
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
    onToggleAllDeleteSelection?: (checked: boolean) => void;
    canRenameDAGs?: boolean;
    onOpenRenameDAG?: (dag: components['schemas']['DAGFile']) => void;
    canDeleteDAGs?: boolean;
    onOpenDeleteDAG?: (dag: components['schemas']['DAGFile']) => void;
  }
}

const columnHelper = createColumnHelper<Data>();
// --- End Helper Functions ---

const deleteSelectionColumn = columnHelper.display({
  id: 'Select',
  size: 40,
  minSize: 40,
  maxSize: 40,
  header: ({ table }) => (
    <div className="flex h-8 items-center justify-center">
      <I18nProps>
        <Checkbox
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
    if (data.kind !== ItemKind.DAG) {
      return null;
    }
    const fileName = data.dag.fileName;
    return (
      <div
        className="flex h-8 items-center justify-center"
        onClick={(event) => event.stopPropagation()}
      >
        <Checkbox
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
  columnHelper.accessor('name', {
    id: 'Expand',
    header: ({ table }) => (
      <div
        role="button"
        tabIndex={0}
        onClick={table.getToggleAllRowsExpandedHandler()}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            table.toggleAllRowsExpanded();
          }
        }}
        className="flex items-center justify-center text-muted-foreground cursor-pointer h-6 w-6 focus:outline-none rounded"
      >
        {table.getIsAllRowsExpanded() ? (
          <>
            <VisuallyHidden>
              <I18nText text={'Compress rows'} />
            </VisuallyHidden>
            <ChevronUp className="h-4 w-4" />
          </>
        ) : (
          <>
            <VisuallyHidden>
              <I18nText text={'Expand rows'} />
            </VisuallyHidden>
            <ChevronDown className="h-4 w-4" />
          </>
        )}
      </div>
    ),
    cell: ({ row }) => {
      if (row.getCanExpand()) {
        return (
          <div
            className="flex items-center justify-center min-h-[2.5rem] text-muted-foreground cursor-pointer focus:outline-none rounded"
            role="button"
            tabIndex={0}
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
          >
            {row.getIsExpanded() ? (
              <ChevronUp className="h-4 w-4" />
            ) : (
              <ChevronDown className="h-4 w-4" />
            )}
          </div>
        );
      }
      return null;
    },
    size: 32,
    minSize: 32,
    maxSize: 32,
  }),
  columnHelper.accessor('name', {
    id: 'Name',
    header: () => (
      <div className="flex flex-col py-1">
        <span className="text-xs">
          <I18nText text={'Name'} />
        </span>
        <span className="text-xs font-normal text-muted-foreground">
          <I18nText text={'Description'} />
        </span>
      </div>
    ),
    cell: ({ row, getValue, table }) => {
      const data = row.original!;

      if (data.kind === ItemKind.Group) {
        // Group Row: Render group name directly with vertical centering
        return (
          <div
            style={{ paddingLeft: `${row.depth * 1.5}rem` }}
            className="flex items-center min-h-[2.5rem]"
          >
            <span className="font-normal text-muted-foreground">
              {getValue()}
            </span>
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
            <Link
              to={`/dags/${data.dag.fileName}`}
              className="block font-medium text-foreground tracking-tight text-xs truncate hover:underline"
              onClick={(event) => event.stopPropagation()}
            >
              {getValue()}
            </Link>

            {description && (
              <div className="text-xs text-muted-foreground whitespace-normal leading-tight line-clamp-2">
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
                        className="text-xs px-1 py-0 h-3.5 rounded-sm border-primary/30 bg-primary/10 text-primary hover:bg-primary/15 hover:text-primary transition-colors duration-200 cursor-pointer font-normal whitespace-normal break-words focus-visible:outline-none"
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
                        <div className="h-1 w-1 rounded-full bg-primary/70 mr-0.5"></div>
                        {value !== null ? (
                          <>
                            <span className="font-medium">{key}</span>
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
  columnHelper.accessor('kind', {
    id: 'Status',
    size: 80,
    minSize: 80,
    header: () => (
      <div className="flex flex-col py-1">
        <span className="text-xs">
          <I18nText text={'Status'} />
        </span>
        <span className="text-xs font-normal text-muted-foreground">
          <I18nText text={'Latest status'} />
        </span>
      </div>
    ),
    cell: ({ row }) => {
      // Use row
      const data = row.original!;
      if (data.kind === ItemKind.DAG) {
        // Use the updated StatusChip component with xs size
        return (
          <StatusChip status={data.dag.latestDAGRun.status} size="xs">
            {data.dag.latestDAGRun?.statusLabel}
          </StatusChip>
        );
      }
      return null;
    },
  }),
  // Removed Started At and Finished At columns
  columnHelper.accessor('kind', {
    id: 'LastRun',
    size: 110,
    minSize: 90,
    header: () => (
      <div className="flex flex-col py-1">
        <span className="text-xs">
          <I18nText text={'Last Run'} />
        </span>
        <span className="text-xs font-normal text-muted-foreground">
          {getConfig().tz || <I18nText text={'Local Timezone'} />}
        </span>
      </div>
    ),
    cell: ({ row }) => {
      const data = row.original!;
      if (data.kind !== ItemKind.DAG) {
        return null;
      }

      const { startedAt, finishedAt, status } = data.dag.latestDAGRun;

      if (!startedAt || startedAt === '-') {
        // If no start time, display nothing or a placeholder
        return <span className="font-normal text-muted-foreground">-</span>;
      }

      let durationContent: React.ReactNode = null;

      if (finishedAt && finishedAt !== '-') {
        const start = dayjs(startedAt);
        const end = dayjs(finishedAt);

        if (start.isValid() && end.isValid()) {
          const durationMs = end.diff(start);

          if (durationMs > 0) {
            durationContent = (
              <div className="text-xs text-muted-foreground">
                {formatMs(durationMs)}
              </div>
            );
          }
        }
      } else if (status === Status.Running) {
        durationContent = (
          <div className="text-xs text-muted-foreground">
            <I18nText text={'(Running)'} />
          </div>
        );
      }

      return (
        <div className="space-y-0.5 min-w-0">
          <div className="font-normal text-foreground/70 text-xs truncate">
            <RelativeTime timestamp={startedAt} absolute={startedAt} />
          </div>
          {durationContent}
        </div>
      );
    },
  }),
  columnHelper.accessor('kind', {
    id: 'ScheduleAndNextRun',
    size: 140,
    minSize: 120,
    header: () => (
      <div className="flex flex-col py-1">
        <span className="text-xs">
          <I18nText text={'Live / Schedule'} />
        </span>
        <span className="text-xs font-normal text-muted-foreground">
          <I18nText text={'Toggle & next run'} />
        </span>
      </div>
    ),
    cell: ({ row, table }) => {
      const data = row.original!;
      if (data.kind !== ItemKind.DAG) {
        return null;
      }

      const schedules = data.dag.dag.schedule || [];
      const hasSchedule = schedules.length > 0;

      // LiveSwitch component
      const liveSwitch = (
        <div
          onClick={(e) => e.stopPropagation()}
          className={`flex-shrink-0 p-0.5 ${!hasSchedule ? 'opacity-40 pointer-events-none' : ''}`}
        >
          <LiveSwitch
            dag={data.dag}
            refresh={table.options.meta?.refreshFn}
            aria-label={`Toggle ${data.name}`}
          />
        </div>
      );

      if (!hasSchedule) {
        return (
          <div className="flex items-center gap-2">
            {liveSwitch}
            <span className="text-xs text-muted-foreground">
              <I18nText text={'No schedule'} />
            </span>
          </div>
        );
      }

      // Display schedule expressions
      const scheduleContent = (
        <div className="flex flex-wrap gap-0.5">
          {schedules.map((schedule, index) => (
            <Badge
              key={getScheduleKey(schedule, index)}
              variant="outline"
              className="text-xs font-normal px-1 py-0 h-3.5 normal-case tracking-normal"
            >
              {getScheduleLabel(schedule)}
            </Badge>
          ))}
        </div>
      );

      // Display next run information
      let nextRunContent: React.ReactNode | null = null;
      if (!data.dag.suspended && schedules.length > 0) {
        const nextRun = parseNextRun(data.dag.nextRun);
        if (nextRun) {
          nextRunContent = (
            <div className="text-xs text-muted-foreground font-normal leading-tight">
              <Ticker intervalMs={1000}>
                {() => {
                  const ms = nextRun.getTime() - new Date().getTime();
                  if (ms <= 0) {
                    return (
                      <span>
                        <I18nText text={'Due now'} />
                      </span>
                    );
                  }
                  return (
                    <span>
                      <I18nText text={'Run in'} /> {formatMs(ms)}
                    </span>
                  );
                }}
              </Ticker>
            </div>
          );
        } else {
          nextRunContent = (
            <div className="text-xs text-muted-foreground font-normal leading-tight">
              <I18nText text={'No upcoming run'} />
            </div>
          );
        }
      } else if (data.dag.suspended) {
        nextRunContent = (
          <div className="text-xs text-muted-foreground font-normal leading-tight">
            <I18nText text={'Suspended'} />
          </div>
        );
      }

      return (
        <div className="flex items-start gap-1 min-w-0">
          {liveSwitch}
          <div className="space-y-0.5 min-w-0 overflow-hidden">
            {scheduleContent}
            {nextRunContent}
          </div>
        </div>
      );
    },
  }),
  columnHelper.display({
    id: 'Actions',
    size: 160,
    minSize: 160,
    maxSize: 160,
    header: () => (
      <div className="flex flex-col items-center py-1">
        <span className="text-xs">
          <I18nText text={'Actions'} />
        </span>
        <span className="text-xs font-normal text-muted-foreground">
          <I18nText text={'Operations'} />
        </span>
      </div>
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
          className="flex items-center justify-center gap-1 scale-90" // Scale down for density
          onClick={(e) => e.stopPropagation()}
        >
          <DAGActions
            dag={data.dag.dag}
            status={data.dag.latestDAGRun}
            fileName={data.dag.fileName}
            label={false}
            refresh={table.options.meta?.refreshFn}
          />
          {table.options.meta?.canRenameDAGs && (
            <RenameDAGButton
              dag={data.dag}
              onRename={(dag) => table.options.meta?.onOpenRenameDAG?.(dag)}
            />
          )}
          {table.options.meta?.canDeleteDAGs && (
            <DeleteDAGButton
              dag={data.dag}
              onDelete={(dag) => table.options.meta?.onOpenDeleteDAG?.(dag)}
            />
          )}
        </div>
      );
    },
  }),
];

// Mapping between column IDs and backend sort fields
const columnToSortField: Record<string, string> = {
  Name: 'name',
  ScheduleAndNextRun: 'nextRun',
};

// Client-side sortable columns
const clientSortableColumns = ['Status', 'LastRun'];

const cardSortOptions = [
  { value: 'name', label: 'Name' },
  { value: 'nextRun', label: 'Next run' },
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
  showAllButton,
  onShowAllWorkflows,
}: {
  icon: string;
  heading: string;
  description: string;
  showAllButton: boolean;
  onShowAllWorkflows?: () => void;
}) {
  return (
    <>
      <div className="text-6xl mb-4">{icon}</div>
      <h3 className="text-lg font-medium text-foreground mb-2">{heading}</h3>
      <p className="text-sm text-muted-foreground text-center max-w-md mb-4 whitespace-normal break-words">
        {description}
      </p>
      <div className="flex flex-wrap items-center justify-center gap-2">
        {showAllButton && (
          <Button type="button" variant="outline" onClick={onShowAllWorkflows}>
            <I18nText text={'Show all workflows'} />
          </Button>
        )}
        <CreateDAGModal />
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

// --- Header Component for both Server-side and Client-side Sorting ---
const SortableHeader = ({
  column,
  children,
  currentSort,
  currentOrder,
  onSort,
  clientSort,
  clientOrder,
  onClientSort,
}: {
  column: Column<Data, unknown>;
  children: React.ReactNode;
  currentSort?: string;
  currentOrder?: string;
  onSort?: (field: string, order: string) => void;
  clientSort?: string;
  clientOrder?: string;
  onClientSort?: (field: string, order: string) => void;
}) => {
  const serverSortField = columnToSortField[column.id];
  const isClientSortable = clientSortableColumns.includes(column.id);

  // Check if this column is currently sorted (either server or client)
  const isServerActive = serverSortField && currentSort === serverSortField;
  const isClientActive = isClientSortable && clientSort === column.id;
  const isActive = isServerActive || isClientActive;

  // Determine if column is sortable at all
  const isSortable =
    (serverSortField && onSort) || (isClientSortable && onClientSort);

  if (!isSortable) {
    return <>{children}</>;
  }

  const handleClick = () => {
    if (serverSortField && onSort) {
      // Server-side sorting
      const newOrder =
        isServerActive && currentOrder === 'asc' ? 'desc' : 'asc';
      onSort(serverSortField, newOrder);
      // Clear client sort when server sort is applied
      if (onClientSort) {
        onClientSort('', '');
      }
    } else if (isClientSortable && onClientSort) {
      // Client-side sorting
      const newOrder = isClientActive && clientOrder === 'asc' ? 'desc' : 'asc';
      onClientSort(column.id, newOrder);
    }
  };

  // Determine which order to show
  function getDisplayOrder(): string {
    if (isServerActive) return currentOrder || '';
    if (isClientActive) return clientOrder || '';
    return '';
  }
  const displayOrder = getDisplayOrder();

  const button = (
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

  // Wrap client-sortable columns with tooltip
  if (isClientSortable) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>{button}</TooltipTrigger>
        <TooltipContent className="bg-muted text-muted-foreground border">
          <p className="text-xs">
            <I18nText text={'Sorts current page only'} />
          </p>
        </TooltipContent>
      </Tooltip>
    );
  }

  return button;
};

/**
 * DAGTable component displays a table of DAGs with filtering, sorting, and grouping capabilities
 */
function DAGTable({
  dags = [],
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
  canManageWorkflowViews,
  canDeleteDAGs,
  canRenameDAGs,
  workflowViewError,
  onSelectWorkflowView,
  onShowAllWorkflows,
  onResetWorkflowView,
  onSaveWorkflowView,
  onUpdateWorkflowView,
  onSetDefaultWorkflowView,
  onSetPinnedWorkflowView,
  onDeleteWorkflowView,
  onDeleteDAGs,
  onRenameDAG,
  resultCount,
  selectedDAG = null,
  onSelectDAG,
}: Props) {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const columns = useMemo(
    () =>
      canDeleteDAGs
        ? [deleteSelectionColumn, ...defaultColumns]
        : defaultColumns,
    [canDeleteDAGs]
  );
  const tableInstanceRef = useRef<ReturnType<typeof useReactTable> | null>(
    null
  );
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);
  const [deleteSelection, setDeleteSelection] = useState<Set<string>>(
    () => new Set()
  );
  const [deleteTargets, setDeleteTargets] = useState<
    components['schemas']['DAGFile'][] | null
  >(null);
  const [isDeleting, setIsDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleteFailures, setDeleteFailures] = useState<Record<string, string>>(
    {}
  );
  const [renameTarget, setRenameTarget] = useState<
    components['schemas']['DAGFile'] | null
  >(null);
  const [isRenaming, setIsRenaming] = useState(false);
  const [renameError, setRenameError] = useState<string | null>(null);
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

  const [clientSort, setClientSort] = useState<string>('');
  const [clientOrder, setClientOrder] = useState<string>('asc');

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

  const openDeleteDAG = useCallback((dag: components['schemas']['DAGFile']) => {
    setDeleteError(null);
    setDeleteFailures({});
    setDeleteTargets([dag]);
  }, []);

  const openRenameDAG = useCallback((dag: components['schemas']['DAGFile']) => {
    setRenameError(null);
    setRenameTarget(dag);
  }, []);

  const closeRenameDAG = useCallback(() => {
    if (isRenaming) {
      return;
    }
    setRenameTarget(null);
    setRenameError(null);
  }, [isRenaming]);

  const handleRenameSubmit = useCallback(
    async (newFileName: string) => {
      if (!renameTarget) {
        return;
      }
      setIsRenaming(true);
      setRenameError(null);
      try {
        await onRenameDAG(renameTarget.fileName, newFileName);
        setRenameTarget(null);
      } catch (error) {
        setRenameError(
          error instanceof Error ? error.message : 'Failed to rename workflow'
        );
      } finally {
        setIsRenaming(false);
      }
    },
    [onRenameDAG, renameTarget]
  );

  // Handler for client-side sorting
  const handleClientSort = (field: string, order: string) => {
    setClientSort(field);
    setClientOrder(order);
  };

  // Handler for DAG selection
  const handleSelectDAG = (fileName: string, title: string) => {
    // Check if screen is small (less than 768px width)
    const isSmallScreen = window.innerWidth < 768;

    if (isSmallScreen) {
      // For small screens, navigate directly to the DAG details page
      navigate(`/dags/${fileName}`);
    } else if (onSelectDAG) {
      // For larger screens, call the selection handler
      onSelectDAG(fileName, title);
    }
  };

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

  // Helper function for client-side sorting comparison
  const getSortValue = useCallback(
    (
      dag: components['schemas']['DAGFile']
    ): string | components['schemas']['Status'] => {
      if (clientSort === 'Status') {
        return dag.latestDAGRun?.status || '';
      }
      if (clientSort === 'LastRun') {
        return dag.latestDAGRun?.startedAt || '';
      }
      return '';
    },
    [clientSort]
  );

  const compareDags = useCallback(
    (
      a: components['schemas']['DAGFile'],
      b: components['schemas']['DAGFile']
    ): number => {
      let aValue = getSortValue(a);
      let bValue = getSortValue(b);

      if (clientOrder === 'desc') {
        [aValue, bValue] = [bValue, aValue];
      }

      if (aValue < bValue) return -1;
      if (aValue > bValue) return 1;
      return 0;
    },
    [getSortValue, clientOrder]
  );

  // Transform the flat list of DAGs into a hierarchical structure with groups
  const data = useMemo(() => {
    const sortedDags = [...dags];

    if (clientSort) {
      sortedDags.sort(compareDags);
    }

    const groups: { [key: string]: Data } = {};
    sortedDags.forEach((dag) => {
      const groupName = dag.dag.group;
      if (groupName) {
        if (!groups[groupName]) {
          groups[groupName] = {
            kind: ItemKind.Group,
            name: groupName,
            subRows: [],
          };
        }
        groups[groupName].subRows!.push({
          kind: ItemKind.DAG,
          name: dag.dag.name,
          dag: dag,
        });
      }
    });

    // Sort sub-rows within groups if client sorting is active
    if (clientSort) {
      Object.values(groups).forEach((group) => {
        if (group.subRows) {
          group.subRows.sort((a, b) => {
            const aDag = (a as DAGRow).dag;
            const bDag = (b as DAGRow).dag;
            return compareDags(aDag, bDag);
          });
        }
      });
    }

    const hierarchicalData: Data[] = Object.values(groups);
    // Add DAGs without a group
    sortedDags
      .filter((dag) => !dag.dag.group)
      .forEach((dag) => {
        hierarchicalData.push({
          kind: ItemKind.DAG,
          name: dag.dag.name,
          dag: dag,
        });
      });
    return hierarchicalData;
  }, [dags, clientSort, compareDags]);

  useEffect(() => {
    if (!selectedDAG || !tableInstanceRef.current || !onSelectDAG) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      // Get all DAG rows from the sorted table rows (not groups)
      const sortedRows = tableInstanceRef.current?.getRowModel().rows || [];
      const dagRows = sortedRows
        .filter((row) => (row.original as Data)?.kind === ItemKind.DAG)
        .map((row) => ({
          fileName: (row.original as DAGRow).dag.fileName,
          title: (row.original as DAGRow).dag.dag.name,
        }));

      // Find current index
      const currentIndex = dagRows.findIndex(
        (item) => item.fileName === selectedDAG
      );
      if (currentIndex === -1) return;

      // Navigate with arrow keys
      if (event.key === 'ArrowDown' && currentIndex < dagRows.length - 1) {
        event.preventDefault();
        const nextDAG = dagRows[currentIndex + 1];
        if (nextDAG) {
          onSelectDAG(nextDAG.fileName, nextDAG.title);
        }
      } else if (event.key === 'ArrowUp' && currentIndex > 0) {
        event.preventDefault();
        const prevDAG = dagRows[currentIndex - 1];
        if (prevDAG) {
          onSelectDAG(prevDAG.fileName, prevDAG.title);
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [selectedDAG, onSelectDAG]);

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
      onToggleAllDeleteSelection: toggleAllDeleteSelection,
      canRenameDAGs,
      onOpenRenameDAG: openRenameDAG,
      canDeleteDAGs,
      onOpenDeleteDAG: openDeleteDAG,
    },
  });

  tableInstanceRef.current = instance as ReturnType<typeof useReactTable>;
  const filteredDAGFileNames = new Set(getFilteredDAGFileNames());
  const selectedDAGsForDelete = dags.filter(
    (dag) =>
      filteredDAGFileNames.has(dag.fileName) &&
      deleteSelection.has(dag.fileName)
  );

  const appBarContext = useContext(AppBarContext);
  const panelWidth = useContext(PanelWidthContext);

  const useCardView = panelWidth !== null && panelWidth < CARD_VIEW_THRESHOLD;

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
        description: `No workflows match the “${activeWorkflowViewName}” view. Try adjusting its filters or show all workflows.`,
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
        className={`mb-3 space-y-3 rounded-lg border border-border bg-card/50 p-3 ${
          isLoading ? 'opacity-70 pointer-events-none' : ''
        }`}
      >
        <div className="flex flex-wrap items-center gap-2">
          <WorkflowViewSelector
            views={workflowViews}
            activeViewId={activeWorkflowViewId}
            defaultViewId={defaultWorkflowViewId}
            isAllView={isAllWorkflowsView}
            isActiveViewEdited={isWorkflowViewEdited}
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
          />

          {/* Search input */}
          <I18nProps>
            <Input
              type="text"
              placeholder="Filter by workflow name..."
              value={searchText}
              onChange={(e) => handleSearchTextChange(e.target.value)}
              className="w-[200px]"
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
              className="h-9 min-w-[170px] max-w-[220px]"
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

          {canDeleteDAGs && selectedDAGsForDelete.length > 0 && (
            <Button
              type="button"
              variant="destructive"
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

          {/* Pagination - pushed to right */}
          {pagination && (
            <div className="flex-shrink-0 ml-auto">
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
                  {cardSortOptions.map((option) => (
                    <SelectItem
                      key={option.value}
                      value={option.value}
                      className="text-xs"
                    >
                      <I18nText text={option.label} />
                    </SelectItem>
                  ))}
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
          {/* Column widths: Select 40px, Expand 32px, Name auto, Status 10%, LastRun 18%, Schedule 20%, Actions 160px */}
          <colgroup>
            {canDeleteDAGs && <col style={{ width: '40px' }} />}
            <col style={{ width: '32px' }} />
            <col />
            <col style={{ width: '10%' }} />
            <col style={{ width: '18%' }} />
            <col style={{ width: '20%' }} />
            <col style={{ width: '160px' }} />
          </colgroup>
          <TableHeader>
            {instance.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    className={`py-1 text-muted-foreground text-xs overflow-hidden ${header.column.id === 'Select' || header.column.id === 'Expand' ? 'px-0' : 'px-2'}`}
                  >
                    {header.isPlaceholder ? null : (
                      <div>
                        {' '}
                        {/* Wrap header content */}
                        {columnToSortField[header.column.id] ||
                        clientSortableColumns.includes(header.column.id) ? (
                          <SortableHeader
                            column={header.column}
                            currentSort={sortField}
                            currentOrder={sortOrder}
                            onSort={onSortChange}
                            clientSort={clientSort}
                            clientOrder={clientOrder}
                            onClientSort={handleClientSort}
                          >
                            {flexRender(
                              header.column.columnDef.header,
                              header.getContext()
                            )}
                          </SortableHeader>
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
              instance.getRowModel().rows.map((row) => {
                // For DAG rows, make the entire row clickable
                const isDAGRow = row.original?.kind === ItemKind.DAG;
                const isDeleteSelected =
                  isDAGRow &&
                  'dag' in row.original &&
                  deleteSelection.has((row.original as DAGRow).dag.fileName);
                // Type guard to ensure we only access dag property when it exists

                return (
                  <TableRow
                    key={row.id}
                    data-state={isDeleteSelected ? 'selected' : undefined}
                    className={`text-[0.8125rem] ${
                      row.original?.kind === ItemKind.Group
                        ? 'bg-muted/50 font-semibold cursor-pointer hover:bg-muted/70'
                        : isDAGRow &&
                            'dag' in row.original &&
                            selectedDAG ===
                              (row.original as DAGRow).dag.fileName
                          ? 'cursor-pointer hover:bg-muted/50 shadow-[inset_3px_0_0_0_var(--primary)]'
                          : 'cursor-pointer hover:bg-muted/50'
                    } ${isDAGRow && 'dag' in row.original && (row.original as DAGRow).dag.latestDAGRun?.status === Status.Running ? 'animate-running-row' : ''}`}
                    onClick={(e) => {
                      // Handle group row clicks - toggle expanded state
                      if ((row.original as Data)?.kind === ItemKind.Group) {
                        row.toggleExpanded();
                      }
                      // Handle DAG row clicks - select DAG or open in new tab
                      else if (isDAGRow && 'dag' in row.original) {
                        const dagRow = row.original as DAGRow;
                        const fileName = dagRow.dag.fileName;
                        const title = dagRow.dag.dag.name;

                        // If Cmd (Mac) or Ctrl (Windows/Linux) key is pressed, open in new tab
                        if (e.metaKey || e.ctrlKey) {
                          window.open(`/dags/${fileName}`, '_blank');
                        } else {
                          // Normal click behavior - select the DAG
                          handleSelectDAG(fileName, title);
                        }
                      }
                    }}
                  >
                    {row.getVisibleCells().map((cell) => (
                      <TableCell
                        key={cell.id}
                        className={`py-1 align-middle ${cell.column.id === 'Status' ? 'overflow-visible whitespace-nowrap' : 'overflow-hidden truncate'} ${cell.column.id === 'Select' || cell.column.id === 'Expand' ? 'px-0' : 'px-2'}`}
                      >
                        {flexRender(
                          cell.column.columnDef.cell,
                          cell.getContext()
                        )}
                      </TableCell>
                    ))}
                  </TableRow>
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
          instance.getRowModel().rows.map((row) => {
            // Render group rows with collapsible header
            if (row.original?.kind === ItemKind.Group) {
              const groupRow = row.original as GroupRow;
              const isExpanded = row.getIsExpanded();

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
                      <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide truncate">
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
                              isSelected={selectedDAG === dagRow.dag.fileName}
                              onSelect={handleSelectDAG}
                              onLabelClick={handleLabelClick}
                              refreshFn={refreshFn}
                              canDeleteDAGs={canDeleteDAGs}
                              isDeleteSelected={deleteSelection.has(
                                dagRow.dag.fileName
                              )}
                              onToggleDeleteSelection={toggleDeleteSelection}
                              canRenameDAGs={canRenameDAGs}
                              onRenameDAG={openRenameDAG}
                              onDeleteDAG={openDeleteDAG}
                              className="ml-2"
                            />
                          );
                        }
                        return null;
                      })}
                    </div>
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
              return (
                <DAGCard
                  key={row.id}
                  dag={dagRow.dag}
                  isSelected={selectedDAG === dagRow.dag.fileName}
                  onSelect={handleSelectDAG}
                  onLabelClick={handleLabelClick}
                  refreshFn={refreshFn}
                  canDeleteDAGs={canDeleteDAGs}
                  isDeleteSelected={deleteSelection.has(dagRow.dag.fileName)}
                  onToggleDeleteSelection={toggleDeleteSelection}
                  canRenameDAGs={canRenameDAGs}
                  onRenameDAG={openRenameDAG}
                  onDeleteDAG={openDeleteDAG}
                />
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

      <DAGNameInputModal
        isOpen={renameTarget !== null}
        onClose={closeRenameDAG}
        onSubmit={(newFileName) => void handleRenameSubmit(newFileName)}
        mode="rename"
        initialValue={renameTarget?.fileName ?? ''}
        isLoading={isRenaming}
        externalError={renameError}
      />
    </div>
  );
}

export default DAGTable;
