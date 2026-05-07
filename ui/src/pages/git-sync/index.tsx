import {
  components,
  SyncItemKind,
  SyncStatus,
  SyncSummary,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useSimpleToast } from '@/components/ui/simple-toast';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWrite } from '@/contexts/AuthContext';
import { useClient, useQuery } from '@/hooks/api';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  Download,
  EyeOff,
  FileCode,
  GitBranch,
  RefreshCw,
  Settings,
  Trash2,
  Upload,
} from 'lucide-react';
import {
  useCallback,
  useContext,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useSearchParams } from 'react-router-dom';
import { BatchDeleteDialog } from './BatchDeleteDialog';
import { CleanupDialog } from './CleanupDialog';
import { DeleteDialog } from './DeleteDialog';
import { DeleteMissingDialog } from './DeleteMissingDialog';
import { DiffModal } from './DiffModal';
import { ForgetDialog } from './ForgetDialog';
import { MoveDialog } from './MoveDialog';
import { RowActionMenu } from './RowActionMenu';
import { useSyncReconcile } from './useSyncReconcile';

type SyncStatusResponse = components['schemas']['SyncStatusResponse'];
type SyncConfigResponse = components['schemas']['SyncConfigResponse'];
type SyncItemDiffResponse = components['schemas']['SyncItemDiffResponse'];
type SyncItem = components['schemas']['SyncItem'];
type StatusFilter = 'all' | 'modified' | 'untracked' | 'conflict' | 'missing';
type TypeFilter = 'dag' | 'config' | 'memory' | 'skill' | 'soul' | 'doc';
type UISyncKind = 'dag' | 'config' | 'memory' | 'skill' | 'soul' | 'doc';
type SyncRow = { itemId: string; item: SyncItem; kind: UISyncKind };

const statusFilters: StatusFilter[] = [
  'all',
  'modified',
  'untracked',
  'conflict',
  'missing',
];
const typeFilters: TypeFilter[] = [
  'dag',
  'config',
  'memory',
  'skill',
  'soul',
  'doc',
];

function parseStatusFilter(value: string | null): StatusFilter {
  if (
    value === 'all' ||
    value === 'modified' ||
    value === 'untracked' ||
    value === 'conflict' ||
    value === 'missing'
  ) {
    return value;
  }
  return 'all';
}

function parseTypeFilter(value: string | null): TypeFilter {
  if (
    value === 'dag' ||
    value === 'config' ||
    value === 'memory' ||
    value === 'skill' ||
    value === 'soul' ||
    value === 'doc'
  ) {
    return value;
  }
  return 'dag';
}

function normalizeSyncItemKind(kind: SyncItemKind): UISyncKind {
  if (kind === SyncItemKind.config) return 'config';
  if (kind === SyncItemKind.memory) return 'memory';
  if (kind === SyncItemKind.skill) return 'skill';
  if (kind === SyncItemKind.soul) return 'soul';
  if (kind === SyncItemKind.doc) return 'doc';
  return 'dag';
}

// Subtle, readable status colors
const summaryConfig: Record<
  SyncSummary,
  { label: string; badgeClass: string }
> = {
  [SyncSummary.synced]: {
    label: 'Synced',
    badgeClass: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
  },
  [SyncSummary.pending]: {
    label: 'Unpublished',
    badgeClass: 'bg-amber-500/10 text-amber-700 dark:text-amber-400',
  },
  [SyncSummary.conflict]: {
    label: 'Conflict',
    badgeClass: 'bg-rose-500/10 text-rose-700 dark:text-rose-400',
  },
  [SyncSummary.error]: {
    label: 'Error',
    badgeClass: 'bg-rose-500/10 text-rose-700 dark:text-rose-400',
  },
  [SyncSummary.missing]: {
    label: 'Missing',
    badgeClass: 'bg-slate-500/10 text-slate-700 dark:text-slate-400',
  },
};

// Status configuration for dots
const statusConfig: Record<SyncStatus, { color: string; label: string }> = {
  [SyncStatus.synced]: { color: 'bg-emerald-500', label: 'synced' },
  [SyncStatus.modified]: { color: 'bg-amber-500', label: 'modified' },
  [SyncStatus.untracked]: { color: 'bg-blue-500', label: 'untracked' },
  [SyncStatus.conflict]: { color: 'bg-rose-500', label: 'conflict' },
  [SyncStatus.missing]: { color: 'bg-slate-400', label: 'missing' },
};

function StatusDot({ status }: { status: SyncStatus }) {
  const config = statusConfig[status];
  return (
    <div className="flex items-center gap-1.5">
      <span className={cn('h-2 w-2 rounded-full', config.color)} />
      <span className="text-xs text-muted-foreground">{config.label}</span>
    </div>
  );
}

export default function GitSyncPage() {
  const appBarContext = useContext(AppBarContext);
  const { setTitle } = appBarContext;
  const client = useClient();
  const canWrite = useCanWrite();
  const { showToast } = useSimpleToast();
  const { showError } = useErrorModal();

  // State
  const [isPulling, setIsPulling] = useState(false);
  const [isPublishing, setIsPublishing] = useState(false);
  const [showConfig, setShowConfig] = useState(false);
  const [publishModal, setPublishModal] = useState<{
    open: boolean;
    itemId?: string;
  }>({ open: false });
  const [publishForce, setPublishForce] = useState(false);
  const [commitMessage, setCommitMessage] = useState('');
  const [diffModal, setDiffModal] = useState<{
    open: boolean;
    itemId?: string;
  }>({ open: false });
  const [diffData, setDiffData] = useState<SyncItemDiffResponse | null>(null);
  const [revertModal, setRevertModal] = useState<{
    open: boolean;
    itemId?: string;
  }>({ open: false });
  const [forgetModal, setForgetModal] = useState<{
    open: boolean;
    itemId?: string;
  }>({ open: false });
  const [deleteModal, setDeleteModal] = useState<{
    open: boolean;
    itemId?: string;
  }>({ open: false });
  const [moveModal, setMoveModal] = useState<{
    open: boolean;
    itemId?: string;
  }>({ open: false });
  const [cleanupModal, setCleanupModal] = useState(false);
  const [deleteMissingModal, setDeleteMissingModal] = useState(false);
  const [batchDeleteModal, setBatchDeleteModal] = useState(false);
  const [selectedDags, setSelectedDags] = useState<Set<string>>(new Set());
  const [searchQuery, setSearchQuery] = useState('');
  const userTouchedSelectionRef = useRef(false);
  const prevPublishableRef = useRef<string>('');
  const [searchParams, setSearchParams] = useSearchParams();

  useEffect(() => {
    setTitle('Git Sync');
  }, [setTitle]);

  useEffect(() => {
    if (publishModal.open) {
      setPublishForce(false);
    }
  }, [publishModal.open, publishModal.itemId]);

  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  // Fetch sync status
  const { data: statusData, mutate: mutateStatus } = useQuery(
    '/sync/status',
    {
      params: {
        query: { remoteNode },
      },
    },
    { refreshInterval: 30000 }
  );

  // Fetch sync config
  const { data: configData } = useQuery('/sync/config', {
    params: {
      query: { remoteNode },
    },
  });

  const status = statusData as SyncStatusResponse | undefined;
  const config = configData as SyncConfigResponse | undefined;

  const reconcile = useSyncReconcile({
    remoteNode,
    onSuccess: () => mutateStatus(),
  });

  const statusFilter = parseStatusFilter(searchParams.get('status'));
  const typeFilter = parseTypeFilter(searchParams.get('type'));

  const setFilters = useCallback(
    (next: Partial<{ status: StatusFilter; type: TypeFilter }>) => {
      const nextStatus = next.status ?? statusFilter;
      const nextType = next.type ?? typeFilter;
      const params = new URLSearchParams(searchParams);

      if (nextStatus === 'all') {
        params.delete('status');
      } else {
        params.set('status', nextStatus);
      }

      if (nextType === 'dag') {
        params.delete('type');
      } else {
        params.set('type', nextType);
      }

      setSearchParams(params, { replace: true });
    },
    [searchParams, setSearchParams, statusFilter, typeFilter]
  );

  const syncRows = useMemo<SyncRow[]>(
    () =>
      status?.items
        ? status.items.map((item) => ({
            itemId: item.itemId,
            item,
            kind: normalizeSyncItemKind(item.kind),
          }))
        : [],
    [status?.items]
  );
  const deferredSearchQuery = useDeferredValue(searchQuery);

  const publishableKey = useMemo(() => {
    return syncRows
      .filter(
        ({ item }) =>
          item.status === SyncStatus.modified ||
          item.status === SyncStatus.untracked
      )
      .map(({ itemId }) => itemId)
      .sort()
      .join(',');
  }, [syncRows]);

  useEffect(() => {
    userTouchedSelectionRef.current = false;
    prevPublishableRef.current = '';
    setSelectedDags(new Set());
  }, [remoteNode]);

  const allKnownItemIds = useMemo(
    () => new Set(syncRows.map(({ itemId }) => itemId)),
    [syncRows]
  );

  // Auto-select publishable items without overriding user manual choices on polling.
  useEffect(() => {
    const next = publishableKey ? publishableKey.split(',') : [];
    const prev = prevPublishableRef.current
      ? prevPublishableRef.current.split(',')
      : [];
    const prevSet = new Set(prev);

    setSelectedDags((current) => {
      if (!userTouchedSelectionRef.current) {
        return new Set(next);
      }

      // Keep any currently selected item that still exists (not just publishable).
      const updated = new Set<string>();
      for (const id of current) {
        if (allKnownItemIds.has(id)) {
          updated.add(id);
        }
      }
      // Auto-add newly publishable items.
      for (const id of next) {
        if (!prevSet.has(id)) {
          updated.add(id);
        }
      }
      return updated;
    });

    prevPublishableRef.current = publishableKey;
  }, [publishableKey, allKnownItemIds]);

  // Handlers
  const handlePull = async () => {
    setIsPulling(true);
    try {
      const response = await client.POST('/sync/pull', {
        params: { query: { remoteNode } },
      });
      if (response.error) {
        showError(response.error.message || 'Pull failed');
      } else {
        showToast(`Pulled ${response.data?.synced?.length || 0} items`);
        mutateStatus();
      }
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Pull failed');
    } finally {
      setIsPulling(false);
    }
  };

  const handlePublish = async (itemId?: string, force?: boolean) => {
    setIsPublishing(true);
    try {
      const response = itemId
        ? await client.POST('/sync/items/{itemId}/publish', {
            params: { path: { itemId }, query: { remoteNode } },
            body: {
              message: commitMessage || `Update ${itemId}`,
              force: force || false,
            },
          })
        : await client.POST('/sync/publish-all', {
            params: { query: { remoteNode } },
            body: {
              message: commitMessage || 'Batch update',
              itemIds: publishableSelectedIds,
            },
          });

      if (response.error) {
        showError(response.error.message || 'Publish failed');
      } else {
        const count = itemId
          ? itemId
          : `${response.data?.synced?.length || 0} items`;
        showToast(`Published ${count}`);
        setPublishModal({ open: false });
        setDiffModal({ open: false });
        setCommitMessage('');
        setPublishForce(false);
        setSelectedDags(new Set());
        userTouchedSelectionRef.current = false;
        mutateStatus();
      }
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Publish failed');
    } finally {
      setIsPublishing(false);
    }
  };

  const handleDiscard = async (itemId: string) => {
    try {
      const response = await client.POST('/sync/items/{itemId}/discard', {
        params: { path: { itemId }, query: { remoteNode } },
      });
      if (response.error) {
        showError(response.error.message || 'Discard failed');
      } else {
        showToast(`Discarded changes to ${itemId}`);
        mutateStatus();
      }
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Discard failed');
    }
  };

  const handleTestConnection = async () => {
    try {
      const response = await client.POST('/sync/test-connection', {
        params: { query: { remoteNode } },
      });
      if (response.error) {
        showError(response.error.message || 'Connection test failed');
      } else if (response.data?.success) {
        showToast('Connection successful');
      } else {
        showError(response.data?.error || 'Connection test failed');
      }
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Connection test failed');
    }
  };

  const handleViewDiff = async (itemId: string) => {
    // Fetch data first, then open modal
    try {
      const response = await client.GET('/sync/items/{itemId}/diff', {
        params: { path: { itemId }, query: { remoteNode } },
      });
      if (response.data) {
        setDiffData(response.data);
        setDiffModal({ open: true, itemId });
      }
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Failed to load diff');
    }
  };

  const filteredRows = useMemo(
    () =>
      syncRows.filter(({ itemId, item, kind }) => {
        const typeMatches = kind === typeFilter;
        const statusMatches =
          statusFilter === 'all' || item.status === statusFilter;
        const normalizedQuery = deferredSearchQuery.trim().toLowerCase();
        const searchMatches =
          normalizedQuery === '' ||
          itemId.toLowerCase().includes(normalizedQuery) ||
          item.displayName.toLowerCase().includes(normalizedQuery);
        return typeMatches && statusMatches && searchMatches;
      }),
    [syncRows, typeFilter, statusFilter, deferredSearchQuery]
  );

  const allVisibleItemIDs = useMemo(
    () => filteredRows.map(({ itemId }) => itemId),
    [filteredRows]
  );

  const allVisibleSelected = useMemo(
    () =>
      allVisibleItemIDs.length > 0 &&
      allVisibleItemIDs.every((id) => selectedDags.has(id)),
    [allVisibleItemIDs, selectedDags]
  );

  const handleToggleSelectAll = useCallback(() => {
    userTouchedSelectionRef.current = true;
    setSelectedDags((prev) => {
      const next = new Set(prev);
      if (allVisibleSelected) {
        for (const id of allVisibleItemIDs) next.delete(id);
      } else {
        for (const id of allVisibleItemIDs) next.add(id);
      }
      return next;
    });
  }, [allVisibleSelected, allVisibleItemIDs]);

  const handleToggleItem = useCallback((itemId: string) => {
    userTouchedSelectionRef.current = true;
    setSelectedDags((prev) => {
      const next = new Set(prev);
      if (next.has(itemId)) next.delete(itemId);
      else next.add(itemId);
      return next;
    });
  }, []);

  const typeCounts = useMemo(() => {
    const counts: Record<TypeFilter, number> = {
      dag: 0,
      config: 0,
      memory: 0,
      skill: 0,
      soul: 0,
      doc: 0,
    };
    for (const { kind } of syncRows) {
      counts[kind] += 1;
    }
    return counts;
  }, [syncRows]);

  const statusCounts = useMemo(() => {
    const counts: Record<StatusFilter, number> = {
      all: 0,
      modified: 0,
      untracked: 0,
      conflict: 0,
      missing: 0,
    };

    for (const { item, kind } of syncRows) {
      if (kind !== typeFilter) {
        continue;
      }
      counts.all += 1;
      if (item.status === SyncStatus.modified) counts.modified += 1;
      if (item.status === SyncStatus.untracked) counts.untracked += 1;
      if (item.status === SyncStatus.conflict) counts.conflict += 1;
      if (item.status === SyncStatus.missing) counts.missing += 1;
    }

    return counts;
  }, [syncRows, typeFilter]);

  const rowByID = useMemo(
    () => new Map(syncRows.map((row) => [row.itemId, row] as const)),
    [syncRows]
  );

  // Deletable items from current selection (synced, modified, conflict, missing — not untracked)
  const deletableSelectedIds = useMemo(
    () =>
      Array.from(selectedDags).filter((id) => {
        const row = rowByID.get(id);
        return row && row.item.status !== SyncStatus.untracked;
      }),
    [selectedDags, rowByID]
  );

  const hasModifiedOrConflictInSelection = useMemo(
    () =>
      deletableSelectedIds.some((id) => {
        const row = rowByID.get(id);
        return (
          row &&
          (row.item.status === SyncStatus.modified ||
            row.item.status === SyncStatus.conflict)
        );
      }),
    [deletableSelectedIds, rowByID]
  );

  // Publishable items from current selection
  const publishableSelectedIds = useMemo(
    () =>
      Array.from(selectedDags).filter((id) => {
        const row = rowByID.get(id);
        return (
          row &&
          (row.item.status === SyncStatus.modified ||
            row.item.status === SyncStatus.untracked)
        );
      }),
    [selectedDags, rowByID]
  );

  const publishableSelectedCount = publishableSelectedIds.length;
  const publishItemStatus = publishModal.itemId
    ? rowByID.get(publishModal.itemId)?.item.status
    : undefined;
  const canForcePublish = publishItemStatus === SyncStatus.conflict;

  const selectedCounts = useMemo(() => {
    let dag = 0;
    let config = 0;
    let memory = 0;
    let skill = 0;
    let soul = 0;
    let doc = 0;
    for (const dagID of selectedDags) {
      const row = rowByID.get(dagID);
      if (!row) continue;
      if (row.kind === 'config') config += 1;
      else if (row.kind === 'memory') memory += 1;
      else if (row.kind === 'skill') skill += 1;
      else if (row.kind === 'soul') soul += 1;
      else if (row.kind === 'doc') doc += 1;
      else dag += 1;
    }
    return {
      dag,
      config,
      memory,
      skill,
      soul,
      doc,
      total: dag + config + memory + skill + soul + doc,
    };
  }, [selectedDags, rowByID]);

  const emptyStateMessage = useMemo(() => {
    const typeLabelMap: Record<string, string> = {
      dag: 'DAG',
      config: 'config',
      memory: 'memory',
      skill: 'skill',
      soul: 'soul',
      doc: 'doc',
    };
    const typeLabel = typeLabelMap[typeFilter] || typeFilter;
    if (searchQuery.trim()) {
      if (statusFilter === 'all') {
        return `No ${typeLabel} items matching "${searchQuery.trim()}"`;
      }
      return `No ${typeLabel} items with ${statusFilter} status matching "${searchQuery.trim()}"`;
    }
    if (statusFilter === 'all') {
      return `No ${typeLabel} items`;
    }
    return `No ${typeLabel} items with ${statusFilter} status`;
  }, [searchQuery, statusFilter, typeFilter]);

  const missingCount = status?.counts?.missing || 0;

  // Not configured state
  if (!status?.enabled) {
    return (
      <div className="flex flex-col items-center justify-center h-[60vh] text-center">
        <GitBranch className="h-16 w-16 text-muted-foreground/30 mb-4" />
        <h2 className="text-xl font-semibold mb-2">Git Sync Not Configured</h2>
        <p className="text-muted-foreground max-w-md">
          Git sync allows you to synchronize DAG definitions with a remote Git
          repository. Configure it in your server settings.
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4 max-w-7xl pb-8">
      {/* Compact Header */}
      <div className="flex items-center justify-between py-2">
        <div className="flex items-center gap-4">
          <h1 className="text-lg font-semibold">Git Sync</h1>
          <span className="text-xs text-muted-foreground font-mono">
            {status?.repository}:{status?.branch}
          </span>
          {status?.summary && (
            <span
              className={cn(
                'text-xs px-2 py-0.5 rounded',
                summaryConfig[status.summary]?.badgeClass
              )}
            >
              {summaryConfig[status.summary]?.label}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0"
            onClick={handlePull}
            disabled={isPulling || !canWrite}
            title={!canWrite ? 'Write permission required' : 'Pull from remote'}
          >
            {isPulling ? (
              <RefreshCw className="h-4 w-4 animate-spin" />
            ) : (
              <Download className="h-4 w-4" />
            )}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0"
            onClick={() => setPublishModal({ open: true })}
            disabled={
              publishableSelectedCount === 0 ||
              !config?.pushEnabled ||
              !canWrite
            }
            title={
              !canWrite
                ? 'Write permission required'
                : !config?.pushEnabled
                  ? 'Push disabled in read-only mode'
                  : `Publish ${publishableSelectedCount} selected`
            }
          >
            <Upload className="h-4 w-4" />
          </Button>
          {deletableSelectedIds.length > 0 &&
            config?.pushEnabled &&
            canWrite && (
              <Button
                variant="ghost"
                size="sm"
                className="h-8 px-2 text-xs text-destructive hover:text-destructive"
                onClick={() => setBatchDeleteModal(true)}
                title={`Delete ${deletableSelectedIds.length} selected`}
              >
                <Trash2 className="h-4 w-4 mr-1" />
                Delete ({deletableSelectedIds.length})
              </Button>
            )}
          {missingCount > 0 && canWrite && (
            <Button
              variant="ghost"
              size="sm"
              className="h-8 px-2 text-xs"
              onClick={() => setCleanupModal(true)}
              title="Remove missing items from sync tracking"
            >
              <EyeOff className="h-4 w-4 mr-1" />
              Cleanup
            </Button>
          )}
          {missingCount > 0 && config?.pushEnabled && canWrite && (
            <Button
              variant="ghost"
              size="sm"
              className="h-8 px-2 text-xs text-destructive hover:text-destructive"
              onClick={() => setDeleteMissingModal(true)}
              title="Delete all missing items from remote repository"
            >
              <Trash2 className="h-4 w-4 mr-1" />
              Delete Missing
            </Button>
          )}
          <Button
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0"
            onClick={async () => {
              await mutateStatus();
              showToast('Status refreshed');
            }}
            title="Refresh status"
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0"
            onClick={() => setShowConfig(true)}
            title="Configuration"
          >
            <Settings className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* Filter Controls */}
      <div className="flex items-center justify-between gap-2">
        <div className="inline-flex items-center rounded-md border border-border/60 bg-card p-0.5 text-xs">
          {typeFilters.map((f) => (
            <button
              key={f}
              type="button"
              onClick={() => setFilters({ type: f })}
              className={cn(
                'px-2.5 py-1 rounded transition-colors',
                typeFilter === f
                  ? 'bg-muted text-foreground'
                  : 'text-muted-foreground hover:text-foreground'
              )}
            >
              {
                (
                  {
                    dag: 'DAGs',
                    config: 'Config',
                    memory: 'Memory',
                    skill: 'Skills',
                    soul: 'Souls',
                    doc: 'Docs',
                  } as Record<string, string>
                )[f]
              }{' '}
              ({typeCounts[f]})
            </button>
          ))}
        </div>
        <div className="w-full max-w-sm">
          <Input
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search items..."
            className="h-8 text-xs"
            aria-label="Search git sync items"
          />
        </div>
        {selectedCounts.total > 0 && (
          <span className="text-xs text-muted-foreground">
            Selected: {selectedCounts.dag} DAGs
            {selectedCounts.config > 0
              ? `, ${selectedCounts.config} config`
              : ''}
            {selectedCounts.memory > 0
              ? `, ${selectedCounts.memory} memory`
              : ''}
            {selectedCounts.skill > 0 ? `, ${selectedCounts.skill} skills` : ''}
            {selectedCounts.soul > 0 ? `, ${selectedCounts.soul} souls` : ''}
            {selectedCounts.doc > 0 ? `, ${selectedCounts.doc} docs` : ''}
          </span>
        )}
      </div>

      <div
        className="flex items-center gap-1 text-xs border-b border-border/40"
        role="tablist"
        aria-label="Filter items by status"
      >
        {statusFilters.map((f, index) => (
          <button
            key={f}
            role="tab"
            aria-selected={statusFilter === f}
            tabIndex={statusFilter === f ? 0 : -1}
            onClick={() => setFilters({ status: f })}
            onKeyDown={(e) => {
              if (e.key === 'ArrowRight') {
                e.preventDefault();
                const nextFilter =
                  statusFilters[(index + 1) % statusFilters.length];
                if (nextFilter) setFilters({ status: nextFilter });
              } else if (e.key === 'ArrowLeft') {
                e.preventDefault();
                const prevFilter =
                  statusFilters[
                    (index - 1 + statusFilters.length) % statusFilters.length
                  ];
                if (prevFilter) setFilters({ status: prevFilter });
              }
            }}
            className={cn(
              'px-3 py-1.5 border-b-2 -mb-px transition-colors focus:outline-none',
              statusFilter === f
                ? 'border-foreground text-foreground'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            )}
          >
            {f.charAt(0).toUpperCase() + f.slice(1)} ({statusCounts[f]})
          </button>
        ))}
      </div>

      {/* Items Table */}
      <div className="bg-card border border-border rounded-md overflow-hidden shadow-sm">
        <Table className="text-xs [&_table]:table-fixed">
          <TableHeader>
            <TableRow>
              <TableHead className="w-8">
                {allVisibleItemIDs.length > 0 && (
                  <Checkbox
                    checked={allVisibleSelected}
                    onCheckedChange={handleToggleSelectAll}
                    aria-label="Select all items"
                  />
                )}
              </TableHead>
              <TableHead>Item</TableHead>
              <TableHead className="w-24">Status</TableHead>
              <TableHead className="w-28">Synced</TableHead>
              <TableHead className="w-28"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredRows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={5}
                  className="text-center text-muted-foreground py-8 text-xs"
                >
                  {emptyStateMessage}
                </TableCell>
              </TableRow>
            ) : (
              filteredRows.map(({ itemId, item, kind }) => (
                <TableRow
                  key={itemId}
                  className="h-9 cursor-pointer hover:bg-muted/50"
                  onClick={() => handleViewDiff(itemId)}
                >
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <Checkbox
                      checked={selectedDags.has(itemId)}
                      onCheckedChange={() => handleToggleItem(itemId)}
                      aria-label={`Select ${itemId}`}
                    />
                  </TableCell>
                  <TableCell className="max-w-0 overflow-hidden">
                    <div className="flex items-center gap-1.5 min-w-0">
                      <span
                        className="font-mono truncate"
                        title={item.displayName}
                      >
                        {item.displayName}
                      </span>
                      {kind === 'memory' && (
                        <span className="text-[10px] px-1 py-0 rounded bg-purple-500/10 text-purple-600 dark:text-purple-400">
                          memory
                        </span>
                      )}
                      {kind === 'skill' && (
                        <span className="text-[10px] px-1 py-0 rounded bg-pink-500/10 text-pink-600 dark:text-pink-400">
                          skill
                        </span>
                      )}
                      {kind === 'soul' && (
                        <span className="text-[10px] px-1 py-0 rounded bg-cyan-500/10 text-cyan-600 dark:text-cyan-400">
                          soul
                        </span>
                      )}
                      {kind === 'doc' && (
                        <span className="text-[10px] px-1 py-0 rounded bg-blue-500/10 text-blue-600 dark:text-blue-400">
                          doc
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <StatusDot status={item.status} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {item.lastSyncedAt
                      ? dayjs(item.lastSyncedAt).fromNow()
                      : '-'}
                  </TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <div className="flex items-center justify-end gap-0.5">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-6 w-6 p-0"
                        onClick={() => handleViewDiff(itemId)}
                        title="View diff"
                      >
                        <FileCode className="h-3 w-3" />
                      </Button>
                      {(item.status === SyncStatus.modified ||
                        item.status === SyncStatus.untracked ||
                        item.status === SyncStatus.conflict) &&
                        config?.pushEnabled &&
                        canWrite && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 w-6 p-0"
                            onClick={() =>
                              setPublishModal({ open: true, itemId })
                            }
                            title="Publish"
                          >
                            <Upload className="h-3 w-3" />
                          </Button>
                        )}
                      {item.status === SyncStatus.missing &&
                        config?.pushEnabled &&
                        canWrite && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 w-6 p-0 text-muted-foreground hover:text-rose-600"
                            onClick={() =>
                              setDeleteModal({ open: true, itemId })
                            }
                            title="Delete from remote"
                          >
                            <Trash2 className="h-3 w-3" />
                          </Button>
                        )}
                      {(item.status === SyncStatus.modified ||
                        item.status === SyncStatus.conflict) &&
                        canWrite && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 w-6 p-0 text-muted-foreground hover:text-rose-600"
                            onClick={() =>
                              setRevertModal({ open: true, itemId })
                            }
                            title="Discard"
                          >
                            <Trash2 className="h-3 w-3" />
                          </Button>
                        )}
                      <RowActionMenu
                        itemId={itemId}
                        kind={kind}
                        status={item.status}
                        pushEnabled={!!config?.pushEnabled}
                        canWrite={canWrite}
                        onForget={(id) =>
                          setForgetModal({ open: true, itemId: id })
                        }
                        onDelete={(id) =>
                          setDeleteModal({ open: true, itemId: id })
                        }
                        onMove={(id) =>
                          setMoveModal({ open: true, itemId: id })
                        }
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {/* Configuration Modal */}
      <Dialog open={showConfig} onOpenChange={setShowConfig}>
        <DialogContent className="sm:max-w-sm">
          <div className="space-y-3 text-sm">
            <div>
              <span className="text-muted-foreground text-xs">Repository</span>
              <div className="font-mono text-xs mt-0.5 select-all overflow-x-auto whitespace-nowrap scrollbar-thin">
                {config?.repository || '-'}
              </div>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Branch</span>
              <span className="font-mono text-xs">{config?.branch || '-'}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Push</span>
              <span className="text-xs">
                {config?.pushEnabled ? 'Enabled' : 'Disabled'}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Auto-sync</span>
              <span className="text-xs">
                {config?.autoSync?.enabled
                  ? `Every ${config.autoSync.interval || 300}s`
                  : 'Off'}
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Last Sync</span>
              <span className="text-xs">
                {status?.lastSyncAt
                  ? dayjs(status.lastSyncAt).format('MMM D, HH:mm')
                  : 'Never'}
              </span>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="w-full mt-2"
              onClick={handleTestConnection}
            >
              Test Connection
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* Publish Modal */}
      <Dialog
        open={publishModal.open}
        onOpenChange={(open) => {
          setPublishModal({ open });
          if (!open) {
            setPublishForce(false);
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="text-base">
              {publishModal.itemId
                ? `Publish ${publishModal.itemId}`
                : `Publish ${publishableSelectedCount} Selected`}
            </DialogTitle>
            <DialogDescription className="text-xs">
              Enter a commit message for this change.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="commit-message" className="text-xs">
                Commit Message
              </Label>
              <Input
                id="commit-message"
                className="h-8 text-sm"
                placeholder={
                  publishModal.itemId
                    ? `Update ${publishModal.itemId}`
                    : 'Batch update'
                }
                value={commitMessage}
                onChange={(e) => setCommitMessage(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !isPublishing) {
                    e.preventDefault();
                    handlePublish(publishModal.itemId, publishForce);
                  }
                }}
              />
            </div>
            {canForcePublish && (
              <div className="flex items-center gap-2">
                <Checkbox
                  id="publish-force"
                  checked={publishForce}
                  onCheckedChange={(checked) =>
                    setPublishForce(checked === true)
                  }
                />
                <Label htmlFor="publish-force" className="text-xs">
                  Force publish (override conflict)
                </Label>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setPublishModal({ open: false });
                setPublishForce(false);
              }}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              onClick={() => handlePublish(publishModal.itemId, publishForce)}
              disabled={isPublishing}
            >
              {isPublishing ? (
                <>
                  <RefreshCw className="h-3.5 w-3.5 mr-1 animate-spin" />
                  Publishing...
                </>
              ) : (
                <>
                  <Upload className="h-3.5 w-3.5 mr-1" />
                  Publish
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Diff Modal */}
      <DiffModal
        open={diffModal.open}
        onOpenChange={(open) => setDiffModal({ open })}
        dagId={diffModal.itemId || ''}
        status={diffData?.status}
        localContent={diffData?.localContent}
        remoteContent={diffData?.remoteContent}
        remoteCommit={diffData?.remoteCommit}
        remoteAuthor={diffData?.remoteAuthor}
        canPublish={
          canWrite &&
          config?.pushEnabled &&
          (diffData?.status === SyncStatus.modified ||
            diffData?.status === SyncStatus.untracked ||
            diffData?.status === SyncStatus.conflict)
        }
        canRevert={
          canWrite &&
          (diffData?.status === SyncStatus.modified ||
            diffData?.status === SyncStatus.conflict)
        }
        onPublish={() => {
          setPublishModal({ open: true, itemId: diffModal.itemId });
        }}
        onRevert={() => {
          if (diffModal.itemId) {
            setRevertModal({ open: true, itemId: diffModal.itemId });
          }
        }}
        onForget={
          diffData?.status === SyncStatus.missing &&
          canWrite &&
          diffModal.itemId
            ? () => setForgetModal({ open: true, itemId: diffModal.itemId })
            : undefined
        }
        onDelete={
          diffData?.status === SyncStatus.missing &&
          canWrite &&
          config?.pushEnabled &&
          diffModal.itemId
            ? () => setDeleteModal({ open: true, itemId: diffModal.itemId })
            : undefined
        }
        isForgetting={reconcile.isForgetting}
        isDeleting={reconcile.isDeleting}
      />

      {/* Revert Confirmation Modal */}
      <ConfirmModal
        title="Discard Changes"
        buttonText="Discard"
        visible={revertModal.open}
        dismissModal={() => setRevertModal({ open: false })}
        onSubmit={() => {
          if (revertModal.itemId) {
            handleDiscard(revertModal.itemId);
          }
          setRevertModal({ open: false });
          setDiffModal({ open: false });
        }}
      >
        <p className="text-sm text-muted-foreground">
          Discard local changes to{' '}
          <span className="font-mono font-medium text-foreground">
            {revertModal.itemId}
          </span>
          ? This cannot be undone.
        </p>
      </ConfirmModal>

      {/* Forget Dialog */}
      <ForgetDialog
        open={forgetModal.open}
        itemId={forgetModal.itemId || ''}
        isForgetting={reconcile.isForgetting}
        onConfirm={async () => {
          if (
            forgetModal.itemId &&
            (await reconcile.handleForget(forgetModal.itemId))
          ) {
            setForgetModal({ open: false });
            setDiffModal({ open: false });
          }
        }}
        onCancel={() => setForgetModal({ open: false })}
      />

      {/* Delete Dialog */}
      <DeleteDialog
        open={deleteModal.open}
        itemId={deleteModal.itemId || ''}
        itemStatus={
          deleteModal.itemId
            ? rowByID.get(deleteModal.itemId)?.item.status || SyncStatus.synced
            : SyncStatus.synced
        }
        isDeleting={reconcile.isDeleting}
        onConfirm={async (force) => {
          if (
            deleteModal.itemId &&
            (await reconcile.handleDelete(deleteModal.itemId, force))
          ) {
            setDeleteModal({ open: false });
            setDiffModal({ open: false });
          }
        }}
        onCancel={() => setDeleteModal({ open: false })}
      />

      {/* Move Dialog */}
      <MoveDialog
        open={moveModal.open}
        itemId={moveModal.itemId || ''}
        itemKind={
          moveModal.itemId
            ? rowByID.get(moveModal.itemId)?.kind || 'dag'
            : 'dag'
        }
        itemStatus={
          moveModal.itemId
            ? rowByID.get(moveModal.itemId)?.item.status || SyncStatus.synced
            : SyncStatus.synced
        }
        isMoving={reconcile.isMoving}
        onConfirm={async (newItemId, message, force) => {
          if (
            moveModal.itemId &&
            (await reconcile.handleMove(
              moveModal.itemId,
              newItemId,
              message,
              force
            ))
          ) {
            setMoveModal({ open: false });
          }
        }}
        onCancel={() => setMoveModal({ open: false })}
      />

      {/* Cleanup Dialog */}
      <CleanupDialog
        open={cleanupModal}
        missingCount={missingCount}
        isCleaningUp={reconcile.isCleaningUp}
        onConfirm={async () => {
          if (await reconcile.handleCleanup()) {
            setCleanupModal(false);
          }
        }}
        onCancel={() => setCleanupModal(false)}
      />

      {/* Delete Missing Dialog */}
      <DeleteMissingDialog
        open={deleteMissingModal}
        missingCount={missingCount}
        isDeletingMissing={reconcile.isDeletingMissing}
        onConfirm={async (message) => {
          if (await reconcile.handleDeleteMissing(message)) {
            setDeleteMissingModal(false);
          }
        }}
        onCancel={() => setDeleteMissingModal(false)}
      />

      {/* Batch Delete Dialog */}
      <BatchDeleteDialog
        open={batchDeleteModal}
        itemIds={deletableSelectedIds}
        hasModifiedOrConflict={hasModifiedOrConflictInSelection}
        isDeletingBatch={reconcile.isDeletingBatch}
        onConfirm={async (message, force) => {
          if (
            await reconcile.handleDeleteBatch(
              deletableSelectedIds,
              message,
              force
            )
          ) {
            setBatchDeleteModal(false);
            setSelectedDags(new Set());
            userTouchedSelectionRef.current = false;
          }
        }}
        onCancel={() => setBatchDeleteModal(false)}
      />
    </div>
  );
}
