// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import {
  Copy,
  Edit3,
  Eye,
  MoreHorizontal,
  Play,
  Plus,
  Search,
  Square,
  Trash2,
} from 'lucide-react';
import { Link, useLocation, useNavigate } from 'react-router-dom';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { RefreshButton } from '@/components/ui/refresh-button';
import RelativeTime from '@/components/ui/relative-time';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import Title from '@/components/ui/title';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  useCanExecuteForWorkspace,
  useCanWriteForWorkspace,
} from '@/contexts/AuthContext';
import { ControllerPromptDialog } from '@/features/controllers/components/ControllerPromptDialog';
import { ControllerDeleteDialog } from '@/features/controllers/components/ControllerDeleteDialog';
import { ControllerStatusChip } from '@/features/controllers/components/ControllerStatusChip';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import {
  useControllerAPI,
  useControllerList,
} from '@/features/controllers/api';
import { serializeControllerDefinition } from '@/features/controllers/draft';
import { useControllerMutation } from '@/features/controllers/useControllerMutation';
import {
  ControllerStatus,
  canStartController,
  isControllerStatusActive,
  type ControllerSummary,
} from '@/features/controllers/types';
import {
  isMutableWorkspaceSelection,
  workspaceNameForSelection,
  workspaceSelectionQuery,
} from '@/lib/workspace';

type ActionTarget = { id: string; name: string };

function ControllerRow({
  controller,
  actionsDisabled,
  onStart,
  onStop,
  onDuplicate,
  onDelete,
}: {
  controller: ControllerSummary;
  actionsDisabled: boolean;
  onStart: () => void;
  onStop: () => void;
  onDuplicate: () => void;
  onDelete: () => void;
}) {
  const canWrite = useCanWriteForWorkspace(controller.workspace);
  const canExecute = useCanExecuteForWorkspace(controller.workspace);
  const active = isControllerStatusActive(
    controller.status,
    controller.finishedAt
  );
  const startable = canStartController(
    controller.status,
    controller.finishedAt
  );
  const detailPath = `/controllers/${encodeURIComponent(controller.id)}/status`;
  const latestDAGRun = controller.latestDAGRun;
  const latestDAGRunIsActive =
    latestDAGRun !== undefined &&
    latestDAGRun.dagRunId === controller.activeDAGRun?.dagRunId;
  const latestDAGRunAvailable = latestDAGRun?.status !== undefined;

  return (
    <TableRow>
      <TableCell className="min-w-64">
        <div className="flex items-center gap-2">
          <Link
            to={detailPath}
            className="font-semibold hover:text-primary hover:underline"
          >
            {controller.name}
          </Link>
          <Badge variant="warning">Experimental</Badge>
        </div>
        <div className="mt-1 flex items-center gap-1 font-mono text-[11px] text-muted-foreground">
          {controller.id}
          <button
            type="button"
            aria-label={`Copy ${controller.id}`}
            onClick={() => void navigator.clipboard.writeText(controller.id)}
          >
            <Copy className="h-3 w-3" />
          </button>
        </div>
        {controller.description && (
          <p className="mt-1 max-w-md truncate text-xs text-muted-foreground">
            {controller.description}
          </p>
        )}
      </TableCell>
      <TableCell>
        <ControllerStatusChip
          status={controller.status}
          finishedAt={controller.finishedAt}
        />
      </TableCell>
      <TableCell className="font-mono">
        {controller.currentState || '—'}
      </TableCell>
      <TableCell>
        {latestDAGRun ? (
          <div>
            {latestDAGRunAvailable ? (
              <Link
                className="text-primary hover:underline"
                to={buildDAGRunPageURL({
                  rootDAGRunName: latestDAGRun.dag,
                  rootDAGRunId: latestDAGRun.dagRunId,
                  remoteNode: 'local',
                })}
              >
                {latestDAGRun.dag}
              </Link>
            ) : (
              <span>{latestDAGRun.dag}</span>
            )}
            <div className="text-xs text-muted-foreground">
              {latestDAGRun.statusLabel ??
                (latestDAGRunIsActive ? 'Pending' : 'Expired')}
            </div>
          </div>
        ) : (
          '—'
        )}
      </TableCell>
      <TableCell>
        <RelativeTime timestamp={controller.resourceUpdatedAt} />
      </TableCell>
      <TableCell>
        <div className="flex justify-end gap-2">
          {active && canExecute ? (
            <Button
              size="sm"
              variant="outline"
              disabled={actionsDisabled}
              onClick={onStop}
            >
              <Square className="h-3.5 w-3.5" /> Stop
            </Button>
          ) : startable && canExecute ? (
            <Button
              size="sm"
              variant="primary"
              disabled={actionsDisabled}
              onClick={onStart}
            >
              <Play className="h-3.5 w-3.5" />
              {controller.status === ControllerStatus.NotStarted
                ? 'Start'
                : 'Run again'}
            </Button>
          ) : null}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                disabled={actionsDisabled}
                aria-label={`Actions for ${controller.name}`}
              >
                <MoreHorizontal className="h-4 w-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem asChild>
                <Link to={detailPath}>
                  <Eye className="mr-2 h-4 w-4" />
                  View
                </Link>
              </DropdownMenuItem>
              {canWrite && !active && (
                <DropdownMenuItem asChild>
                  <Link
                    to={`/controllers/${encodeURIComponent(controller.id)}/spec`}
                  >
                    <Edit3 className="mr-2 h-4 w-4" />
                    Edit
                  </Link>
                </DropdownMenuItem>
              )}
              {canWrite && (
                <DropdownMenuItem onClick={onDuplicate}>
                  Duplicate
                </DropdownMenuItem>
              )}
              {canWrite && !active && (
                <DropdownMenuItem
                  className="text-destructive"
                  onClick={onDelete}
                >
                  <Trash2 className="mr-2 h-4 w-4" />
                  Delete
                </DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </TableCell>
    </TableRow>
  );
}

export default function ControllersPage() {
  const appBar = React.useContext(AppBarContext);
  const location = useLocation();
  const navigate = useNavigate();
  const api = useControllerAPI();
  const mountedRef = React.useRef(true);
  const locationKeyRef = React.useRef(location.key);
  locationKeyRef.current = location.key;
  const requestMatchesLocation = (requestLocationKey: string) =>
    mountedRef.current && locationKeyRef.current === requestLocationKey;
  const workspace = workspaceSelectionQuery(
    appBar.workspaceSelection
  ).workspace;
  const createWorkspace = workspaceNameForSelection(appBar.workspaceSelection);
  const canCreate = useCanWriteForWorkspace(createWorkspace);
  const canSelectCreateWorkspace = isMutableWorkspaceSelection(
    appBar.workspaceSelection
  );
  const { data, error, isLoading, mutate } = useControllerList(workspace);
  const [search, setSearch] = React.useState('');
  const [startTarget, setStartTarget] = React.useState<ActionTarget | null>(
    null
  );
  const [deleteTarget, setDeleteTarget] = React.useState<ActionTarget | null>(
    null
  );
  const {
    pending,
    error: actionError,
    setError: setActionError,
    run: runAction,
  } = useControllerMutation(
    mutate,
    'The action succeeded, but the Controller list could not be refreshed.',
    JSON.stringify([location.key, workspace])
  );

  React.useEffect(() => {
    appBar.setTitle('Controllers');
  }, [appBar]);

  React.useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const controllers = React.useMemo(() => {
    const term = search.toLocaleLowerCase();
    return [...(data?.controllers ?? [])]
      .filter(
        (controller) =>
          !term ||
          controller.name.toLocaleLowerCase().includes(term) ||
          controller.id.toLocaleLowerCase().includes(term)
      )
      .sort(
        (left, right) =>
          left.name.localeCompare(right.name) || left.id.localeCompare(right.id)
      );
  }, [data?.controllers, search]);

  return (
    <div className="flex h-full max-w-7xl flex-col gap-4 overflow-hidden">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Title>Controllers</Title>
            <Badge variant="warning">Experimental</Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            LLM-guided workflows that route through explicit states and DAGs.
          </p>
        </div>
        <Button
          variant="primary"
          disabled={!canCreate || !canSelectCreateWorkspace}
          title={
            !canSelectCreateWorkspace
              ? 'Select a specific or default workspace to create a Controller.'
              : !canCreate
                ? 'Write permission is required for this workspace.'
                : undefined
          }
          onClick={() =>
            navigate('/controllers/new/spec', {
              state: { workspace: createWorkspace },
            })
          }
        >
          <Plus className="h-4 w-4" />
          New Controller
        </Button>
      </div>
      {actionError && (
        <Alert variant="destructive">
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}
      <div className="flex items-center justify-between gap-3">
        <div className="relative w-full max-w-sm">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search name or ID…"
            className="pl-9"
          />
        </div>
        <RefreshButton
          onRefresh={async () => {
            await mutate();
          }}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {isLoading && !data ? (
          <LoadingIndicator />
        ) : error ? (
          <Alert variant="destructive">
            <AlertDescription>{error.message}</AlertDescription>
          </Alert>
        ) : controllers.length === 0 ? (
          <div className="rounded-md border border-dashed border-border p-12 text-center text-sm text-muted-foreground">
            {search
              ? 'No Controllers match this search.'
              : 'No Controllers in this workspace.'}
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Controller</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Current state</TableHead>
                <TableHead>Active / Last DAG</TableHead>
                <TableHead>Updated</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {controllers.map((controller) => (
                <ControllerRow
                  key={controller.id}
                  controller={controller}
                  actionsDisabled={pending}
                  onStart={() =>
                    setStartTarget({ id: controller.id, name: controller.name })
                  }
                  onStop={() =>
                    void runAction(
                      () => api.stop(controller.id),
                      'Stop signal sent'
                    )
                  }
                  onDuplicate={() =>
                    void (async () => {
                      const requestLocationKey = location.key;
                      try {
                        const detail = await api.get(controller.id);
                        if (!requestMatchesLocation(requestLocationKey)) {
                          return;
                        }
                        const definition = {
                          ...detail.definition,
                          id: undefined,
                        };
                        navigate('/controllers/new/spec', {
                          state: {
                            workspace: controller.workspace,
                            duplicateSpec:
                              serializeControllerDefinition(definition),
                          },
                        });
                      } catch (duplicateError) {
                        if (!requestMatchesLocation(requestLocationKey)) {
                          return;
                        }
                        setActionError(
                          duplicateError instanceof Error
                            ? duplicateError.message
                            : 'Could not duplicate Controller'
                        );
                      }
                    })()
                  }
                  onDelete={() =>
                    setDeleteTarget({
                      id: controller.id,
                      name: controller.name,
                    })
                  }
                />
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      <ControllerPromptDialog
        open={startTarget !== null}
        title={startTarget ? `Start ${startTarget.name}` : 'Start Controller'}
        description="Starting creates a new Controller execution and replaces the current context snapshot. Existing DAG runs remain available."
        submitLabel="Start"
        pending={pending}
        onOpenChange={(open) => !open && setStartTarget(null)}
        onSubmit={async (prompt) => {
          if (!startTarget) return;
          const target = startTarget;
          const requestLocationKey = location.key;
          const started = await runAction(
            () => api.start(target.id, prompt),
            'Controller started'
          );
          if (started && requestMatchesLocation(requestLocationKey)) {
            setStartTarget(null);
            navigate(`/controllers/${encodeURIComponent(target.id)}/status`);
          }
        }}
      />
      <ControllerDeleteDialog
        target={deleteTarget}
        pending={pending}
        onClose={() => setDeleteTarget(null)}
        onDelete={async () => {
          if (!deleteTarget) return;
          const target = deleteTarget;
          const deleted = await runAction(
            () => api.delete(target.id),
            'Controller deleted'
          );
          if (deleted) setDeleteTarget(null);
        }}
      />
    </div>
  );
}
