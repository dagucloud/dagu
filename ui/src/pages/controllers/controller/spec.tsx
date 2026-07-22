// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { AlertTriangle, Check, GitGraph, Save, Trash2 } from 'lucide-react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { Tab, Tabs } from '@/components/ui/tabs';
import Title from '@/components/ui/title';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import {
  useControllerAPI,
  useControllerDAGOptions,
  useControllerDetail,
} from '@/features/controllers/api';
import { ControllerBuilder } from '@/features/controllers/components/ControllerBuilder';
import { ControllerDeleteDialog } from '@/features/controllers/components/ControllerDeleteDialog';
import { ControllerGraph } from '@/features/controllers/components/ControllerGraph';
import { ControllerPageHeader } from '@/features/controllers/components/ControllerPageHeader';
import { controllerSchema } from '@/features/controllers/controllerSchema';
import {
  createControllerDraft,
  parseControllerYAML,
  serializeControllerDefinition,
  validateControllerDefinition,
} from '@/features/controllers/draft';
import { canEditController } from '@/features/controllers/types';
import {
  isWorkspaceLabel,
  workspaceNameFromLabels,
  workspaceSelectionKey,
} from '@/lib/workspace';
import DAGEditorWithDocs from '@/features/dags/components/dag-editor/DAGEditorWithDocs';

type EditorTab = 'builder' | 'yaml' | 'graph';

type DraftRouteState = {
  workspace?: string;
  duplicateSpec?: string;
};

function Issues({
  title,
  issues,
  variant = 'destructive',
}: {
  title: string;
  issues: { code: string; path: string; message: string }[];
  variant?: 'destructive' | 'warning';
}) {
  if (issues.length === 0) return null;
  return (
    <Alert variant={variant}>
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <ul className="list-disc space-y-1 pl-5">
          {issues.map((item, index) => (
            <li key={`${item.path}-${item.code}-${index}`}>
              <code>{item.path}</code>: {item.message}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  );
}

export default function ControllerSpecPage({
  isNew = false,
}: {
  isNew?: boolean;
}) {
  const { id: routeID } = useParams<{ id: string }>();
  const id = isNew ? undefined : routeID;
  const location = useLocation();
  const routeState = (location.state ?? {}) as DraftRouteState;
  const navigate = useNavigate();
  const appBar = React.useContext(AppBarContext);
  const api = useControllerAPI();
  const { showToast } = useSimpleToast();
  const workspaceKey = workspaceSelectionKey(appBar.workspaceSelection);
  const detailQuery = useControllerDetail(id, workspaceKey);
  const [tab, setTab] = React.useState<EditorTab>('builder');
  const [source, setSource] = React.useState(() => {
    if (!isNew) return '';
    return (
      routeState.duplicateSpec ??
      serializeControllerDefinition(
        createControllerDraft(routeState.workspace ?? '')
      )
    );
  });
  const [savedSource, setSavedSource] = React.useState('');
  const [initializedID, setInitializedID] = React.useState<string | null>(
    isNew ? 'new' : null
  );
  const [pending, setPending] = React.useState(false);
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [saveError, setSaveError] = React.useState<string | null>(null);
  const parsed = React.useMemo(() => parseControllerYAML(source), [source]);
  const definition = parsed.definition;
  const draftWorkspace = definition
    ? workspaceNameFromLabels(definition.labels)
    : (routeState.workspace ?? '');
  const persistedWorkspace = detailQuery.data
    ? workspaceNameFromLabels(detailQuery.data.definition.labels)
    : (routeState.workspace ?? '');
  const workspace = isNew ? draftWorkspace : persistedWorkspace;
  const canWrite = useCanWriteForWorkspace(workspace);
  const dagOptions = useControllerDAGOptions(workspace);
  const dirty = source !== savedSource;

  React.useEffect(() => {
    const detail = detailQuery.data;
    if (!detail || initializedID === detail.id) return;
    setSource(detail.spec);
    setSavedSource(detail.spec);
    setInitializedID(detail.id);
  }, [detailQuery.data, initializedID]);

  React.useEffect(() => {
    appBar.setTitle(
      isNew
        ? 'New Controller'
        : (detailQuery.data?.definition.name ?? 'Controller')
    );
  }, [appBar, detailQuery.data?.definition.name, isNew]);

  React.useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  React.useEffect(() => {
    if (!dirty) return;
    const guardLinkNavigation = (event: MouseEvent) => {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey ||
        !(event.target instanceof Element)
      ) {
        return;
      }
      const anchor = event.target.closest<HTMLAnchorElement>('a[href]');
      if (
        !anchor ||
        anchor.hasAttribute('download') ||
        (anchor.target && anchor.target !== '_self')
      ) {
        return;
      }
      const next = new URL(anchor.href, window.location.href);
      if (
        next.origin !== window.location.origin ||
        next.href === window.location.href
      ) {
        return;
      }
      if (window.confirm('Discard unsaved Controller changes?')) return;
      event.preventDefault();
      event.stopPropagation();
    };
    document.addEventListener('click', guardLinkNavigation, true);
    return () =>
      document.removeEventListener('click', guardLinkNavigation, true);
  }, [dirty]);

  if (!isNew && detailQuery.isLoading && !detailQuery.data)
    return <LoadingIndicator />;
  if (!isNew && (detailQuery.error || !detailQuery.data)) {
    return (
      <Alert variant="destructive">
        <AlertTitle>Unable to load Controller</AlertTitle>
        <AlertDescription>
          {detailQuery.error?.message ?? 'Controller not found'}
        </AlertDescription>
      </Alert>
    );
  }

  const detail = detailQuery.data;
  const mutable = isNew || canEditController(detail?.runtime);
  const readOnly = !canWrite || !mutable;
  const requiredIssues = definition
    ? validateControllerDefinition(definition, { requireId: !isNew })
    : [];
  const workspaceChanged =
    !isNew &&
    definition !== null &&
    detail !== undefined &&
    definition.labels.filter(isWorkspaceLabel).join('\n') !==
      detail.definition.labels.filter(isWorkspaceLabel).join('\n');
  const issues = [
    ...parsed.issues,
    ...requiredIssues.filter(
      (candidate) =>
        !parsed.issues.some(
          (existing) =>
            existing.path === candidate.path &&
            existing.message === candidate.message
        )
    ),
    ...(workspaceChanged
      ? [
          {
            code: 'immutable_workspace',
            path: 'labels',
            message: 'Controller workspace cannot be changed after creation',
          },
        ]
      : []),
  ];
  const canSave =
    !readOnly && dirty && definition !== null && issues.length === 0;

  const save = async () => {
    if (!canSave || !definition) return;
    setPending(true);
    setSaveError(null);
    try {
      if (isNew) {
        const createDefinition = { ...definition, id: undefined };
        const createSource = serializeControllerDefinition(createDefinition);
        const result = await api.create(createSource);
        showToast('Controller created');
        navigate(`/controllers/${encodeURIComponent(result.id)}/spec`, {
          replace: true,
        });
        return;
      }
      if (!id) return;
      const updated = await api.update(id, source);
      setSource(updated.spec);
      setSavedSource(updated.spec);
      await detailQuery.mutate(updated, { revalidate: false });
      showToast('Controller definition saved');
    } catch (failure) {
      setSaveError(
        failure instanceof Error ? failure.message : 'Could not save Controller'
      );
    } finally {
      setPending(false);
    }
  };

  const deleteController = async () => {
    if (!id || !detail) return;
    setPending(true);
    setSaveError(null);
    try {
      await api.delete(id);
      showToast('Controller deleted');
      navigate('/controllers', { replace: true });
    } catch (failure) {
      setSaveError(
        failure instanceof Error
          ? failure.message
          : 'Could not delete Controller'
      );
    } finally {
      setPending(false);
    }
  };

  const headerActions = (
    <>
      {dirty && <Badge variant="warning">Unsaved changes</Badge>}
      {!isNew && !readOnly && (
        <Button
          variant="destructive"
          disabled={pending}
          onClick={() => setDeleteOpen(true)}
        >
          <Trash2 className="h-4 w-4" />
          Delete
        </Button>
      )}
      <Button
        variant="primary"
        disabled={!canSave || pending}
        onClick={() => void save()}
      >
        {pending ? (
          <span className="animate-pulse">Saving…</span>
        ) : (
          <>
            <Save className="h-4 w-4" />
            Save
          </>
        )}
      </Button>
    </>
  );

  return (
    <div className="mx-auto flex w-full max-w-7xl flex-col gap-5 pb-8">
      {detail ? (
        <ControllerPageHeader
          detail={detail}
          activeTab="spec"
          actions={headerActions}
        />
      ) : (
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <Title>New Controller</Title>
              <Badge variant="warning">Experimental</Badge>
            </div>
            <p className="text-sm text-muted-foreground">
              Build locally, then save to receive an immutable Controller ID.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button asChild variant="ghost">
              <Link to="/controllers">Cancel</Link>
            </Button>
            {headerActions}
          </div>
        </div>
      )}

      {readOnly && (
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>Definition is read-only</AlertTitle>
          <AlertDescription>
            {!canWrite
              ? 'Write permission is required for this workspace.'
              : 'Stop the active Controller and wait for its child DAG to settle before editing.'}
          </AlertDescription>
        </Alert>
      )}
      {saveError && (
        <Alert variant="destructive">
          <AlertDescription>{saveError}</AlertDescription>
        </Alert>
      )}
      <Issues title="Definition needs attention" issues={issues} />
      <Issues
        title="Server warnings"
        issues={detail?.warnings ?? []}
        variant="warning"
      />

      <div className="rounded-md border border-border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-3">
          <Tabs>
            <Tab isActive={tab === 'builder'} onClick={() => setTab('builder')}>
              Builder
            </Tab>
            <Tab isActive={tab === 'yaml'} onClick={() => setTab('yaml')}>
              YAML
            </Tab>
            <Tab isActive={tab === 'graph'} onClick={() => setTab('graph')}>
              <GitGraph className="h-4 w-4" />
              Graph
            </Tab>
          </Tabs>
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            {issues.length === 0 && definition && (
              <>
                <Check className="h-3.5 w-3.5 text-success" />
                Valid locally
              </>
            )}
            <span>{workspace || 'default'} workspace</span>
          </div>
        </div>
        <div className="p-4">
          {tab === 'builder' &&
            (definition && parsed.builderRepresentable ? (
              <ControllerBuilder
                definition={definition}
                workspace={workspace}
                availableDAGs={dagOptions.data}
                readOnly={readOnly}
                onChange={(nextDefinition) =>
                  setSource(serializeControllerDefinition(nextDefinition))
                }
              />
            ) : (
              <Alert variant="warning">
                <AlertTitle>Builder is unavailable</AlertTitle>
                <AlertDescription>
                  Fix structural, type, or unknown-field YAML errors in the YAML
                  tab. The source has been preserved unchanged.
                </AlertDescription>
              </Alert>
            ))}
          {tab === 'yaml' && (
            <div className="space-y-3">
              <Alert variant="warning">
                <AlertTriangle className="h-4 w-4" />
                <AlertDescription>
                  The llm.system value is stored and may be sent to an external
                  LLM. Do not include secrets in the YAML.
                </AlertDescription>
              </Alert>
              <DAGEditorWithDocs
                value={source}
                onChange={(value) => setSource(value ?? '')}
                readOnly={readOnly}
                schema={controllerSchema}
                modelUri={`inmemory://dagu/controllers/${id ?? 'new'}.yaml`}
                className="h-[72vh]"
              />
            </div>
          )}
          {tab === 'graph' &&
            (definition ? (
              <div className="space-y-2">
                <p className="text-xs text-muted-foreground">
                  Draft preview only. Runtime state is intentionally not
                  highlighted here.
                </p>
                <ControllerGraph definition={definition} />
              </div>
            ) : (
              <Alert variant="warning">
                <AlertDescription>
                  Fix YAML errors before previewing the graph.
                </AlertDescription>
              </Alert>
            ))}
        </div>
      </div>
      <ControllerDeleteDialog
        target={
          deleteOpen && detail
            ? { id: detail.id, name: detail.definition.name }
            : null
        }
        pending={pending}
        onClose={() => setDeleteOpen(false)}
        onDelete={deleteController}
      />
    </div>
  );
}
