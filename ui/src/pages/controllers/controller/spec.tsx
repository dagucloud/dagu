// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { AlertTriangle, Check, Save, ShieldCheck, Trash2 } from 'lucide-react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';

import type { components } from '@/api/v1/schema';
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
import { ControllerPageHeader } from '@/features/controllers/components/ControllerPageHeader';
import { controllerSchema } from '@/features/controllers/controllerSchema';
import {
  createControllerDraft,
  parseControllerYAML,
  serializeControllerDefinition,
} from '@/features/controllers/draft';
import {
  canEditController,
  type ControllerValidationIssue,
} from '@/features/controllers/types';
import { useUnsavedChangesWarning } from '@/features/controllers/useUnsavedChangesWarning';
import { workspaceNameFromLabels } from '@/lib/workspace';
import { cn } from '@/lib/utils';
import DAGEditorWithDocs from '@/features/dags/components/dag-editor/DAGEditorWithDocs';

type EditorTab = 'builder' | 'yaml';
type PendingAction = 'save' | 'delete';

type DraftRouteState = {
  workspace?: string;
  duplicateSpec?: string;
};

type ControllerWarning = components['schemas']['ControllerWarning'];

function Issues({
  title,
  issues,
}: {
  title: string;
  issues: ControllerValidationIssue[];
}) {
  if (issues.length === 0) return null;
  return (
    <Alert variant="destructive">
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

function Warnings({ warnings }: { warnings: ControllerWarning[] }) {
  if (warnings.length === 0) return null;
  return (
    <Alert variant="warning">
      <AlertTriangle className="h-4 w-4" />
      <AlertTitle>Definition warnings</AlertTitle>
      <AlertDescription>
        <ul className="list-disc space-y-1 pl-5">
          {warnings.map((warning, index) => (
            <li key={`${warning.path}-${warning.code}-${index}`}>
              {warning.path && (
                <>
                  <code>{warning.path}</code>:{' '}
                </>
              )}
              {warning.message}
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
  const detailQuery = useControllerDetail(id);
  const [tab, setTab] = React.useState<EditorTab>('builder');
  const initialSource = React.useMemo(
    () =>
      isNew
        ? (routeState.duplicateSpec ??
          serializeControllerDefinition(
            createControllerDraft(routeState.workspace ?? '')
          ))
        : '',
    [isNew, routeState.duplicateSpec, routeState.workspace]
  );
  const [source, setSource] = React.useState(initialSource);
  const [savedSource, setSavedSource] = React.useState(initialSource);
  const [builderDraftDirty, setBuilderDraftDirty] = React.useState(false);
  const [dagSearch, setDAGSearch] = React.useState('');
  const initializedIDRef = React.useRef<string | null>(isNew ? 'new' : null);
  const [pendingAction, setPendingAction] =
    React.useState<PendingAction | null>(null);
  const operationInFlight = React.useRef(false);
  const mountedRef = React.useRef(true);
  const locationKeyRef = React.useRef(location.key);
  locationKeyRef.current = location.key;
  const routeIDRef = React.useRef(id);
  routeIDRef.current = id;
  const [deleteOpen, setDeleteOpen] = React.useState(false);
  const [saveError, setSaveError] = React.useState<string | null>(null);
  const parsed = React.useMemo(
    () => parseControllerYAML(source, isNew ? 'create' : 'update'),
    [isNew, source]
  );
  const definition = parsed.definition;
  const draftWorkspace = definition
    ? workspaceNameFromLabels(definition.labels)
    : (routeState.workspace ?? '');
  const persistedWorkspace = detailQuery.data
    ? workspaceNameFromLabels(detailQuery.data.definition.labels)
    : (routeState.workspace ?? '');
  const workspace = isNew ? draftWorkspace : persistedWorkspace;
  const canWrite = useCanWriteForWorkspace(workspace);
  const dagOptions = useControllerDAGOptions(workspace, dagSearch);
  const dirty = source !== savedSource || builderDraftDirty;
  const pending = pendingAction !== null;
  const createPending = isNew && pendingAction === 'save';
  useUnsavedChangesWarning(
    dirty,
    'Discard unsaved Controller changes?',
    createPending
  );

  React.useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  React.useEffect(() => {
    setDeleteOpen(false);
  }, [id, location.key]);

  React.useEffect(() => {
    const detail = detailQuery.data;
    if (!detail || initializedIDRef.current === detail.id) return;
    initializedIDRef.current = detail.id;
    setSource(detail.spec);
    setSavedSource(detail.spec);
    setBuilderDraftDirty(false);
  }, [detailQuery.data]);

  React.useEffect(() => {
    appBar.setTitle(
      isNew
        ? 'New Controller'
        : (detailQuery.data?.definition.name ?? 'Controller')
    );
  }, [appBar, detailQuery.data?.definition.name, isNew]);

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
  const warnings = isNew ? [] : (detail?.warnings ?? []);
  const mutable = isNew || canEditController(detail?.runtime);
  const readOnly = !mutable || (!isNew && !canWrite);
  const editorReadOnly = readOnly || pendingAction === 'save';
  const workspaceChanged =
    !isNew &&
    definition !== null &&
    detail !== undefined &&
    workspaceNameFromLabels(definition.labels) !== persistedWorkspace;
  const issues = [
    ...parsed.issues,
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
    canWrite &&
    mutable &&
    (isNew || dirty) &&
    definition !== null &&
    issues.length === 0;
  const requestMatchesRoute = (
    requestLocationKey: string,
    requestID: string | undefined
  ) =>
    mountedRef.current &&
    locationKeyRef.current === requestLocationKey &&
    routeIDRef.current === requestID;

  const save = async () => {
    if (!canSave || !definition || operationInFlight.current) return;
    operationInFlight.current = true;
    setDeleteOpen(false);
    setPendingAction('save');
    setSaveError(null);
    const requestLocationKey = location.key;
    const requestID = id;
    try {
      if (isNew) {
        const result = await api.create(source);
        showToast('Controller created');
        if (requestMatchesRoute(requestLocationKey, requestID)) {
          navigate(`/controllers/${encodeURIComponent(result.id)}/spec`, {
            replace: true,
          });
        }
        return;
      }
      if (!id) return;
      const updated = await api.update(id, source);
      if (!requestMatchesRoute(requestLocationKey, requestID)) {
        return;
      }
      setSource(updated.spec);
      setSavedSource(updated.spec);
      await detailQuery.mutate(updated, { revalidate: false });
      showToast('Controller definition saved');
    } catch (failure) {
      if (requestMatchesRoute(requestLocationKey, requestID)) {
        setSaveError(
          failure instanceof Error
            ? failure.message
            : 'Could not save Controller'
        );
      }
    } finally {
      operationInFlight.current = false;
      setPendingAction(null);
    }
  };

  const deleteController = async () => {
    if (!id || !detail || operationInFlight.current) return;
    operationInFlight.current = true;
    setPendingAction('delete');
    setSaveError(null);
    const requestLocationKey = location.key;
    const requestID = id;
    try {
      await api.delete(id);
      showToast('Controller deleted');
      if (requestMatchesRoute(requestLocationKey, requestID)) {
        navigate('/controllers', { replace: true });
      }
    } catch (failure) {
      if (requestMatchesRoute(requestLocationKey, requestID)) {
        setSaveError(
          failure instanceof Error
            ? failure.message
            : 'Could not delete Controller'
        );
      }
    } finally {
      operationInFlight.current = false;
      setPendingAction(null);
    }
  };

  const validate = () => {
    showToast(
      issues.length === 0
        ? 'Controller definition is valid'
        : `${issues.length} validation issue${issues.length === 1 ? '' : 's'}`,
      { variant: issues.length === 0 ? 'success' : 'error' }
    );
  };

  const discardPersistedChanges = () => {
    if (!dirty) return;
    if (!window.confirm('Discard unsaved Controller changes?')) return;
    setSource(savedSource);
    setBuilderDraftDirty(false);
    setDAGSearch('');
  };

  const headerActions = (
    <>
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
      <Button variant="outline" disabled={pending} onClick={validate}>
        <ShieldCheck className="h-4 w-4" />
        Validate
      </Button>
      <Button
        variant="primary"
        disabled={!canSave || pending}
        onClick={() => void save()}
      >
        {pendingAction === 'save' ? (
          <span className="animate-pulse">Saving…</span>
        ) : (
          <>
            <Save className="h-4 w-4" />
            Save Controller
          </>
        )}
      </Button>
    </>
  );

  return (
    <div className="mx-auto flex w-full max-w-[1800px] flex-col gap-4 pb-8">
      {detail ? (
        <ControllerPageHeader
          detail={detail}
          activeTab="spec"
          dirty={dirty}
          actions={headerActions}
        />
      ) : (
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <Title>{definition?.name || 'New Controller'}</Title>
              <Badge variant="warning">Experimental</Badge>
              {dirty && <Badge variant="warning">Unsaved</Badge>}
            </div>
            <p className="mt-1 font-mono text-xs text-muted-foreground">
              ID assigned after save · {workspace || 'default'} · Status: not
              started
            </p>
          </div>
          <div className="flex items-center gap-2">
            {createPending ? (
              <Button variant="ghost" disabled>
                Discard
              </Button>
            ) : (
              <Button asChild variant="ghost">
                <Link to="/controllers">Discard</Link>
              </Button>
            )}
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
      {isNew && !canWrite && (
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>Save unavailable</AlertTitle>
          <AlertDescription>
            Write permission is required to save in this workspace.
          </AlertDescription>
        </Alert>
      )}
      {saveError && (
        <Alert variant="destructive">
          <AlertDescription>{saveError}</AlertDescription>
        </Alert>
      )}
      <Warnings warnings={warnings} />
      <div>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border">
          <Tabs className="border-b-0">
            <Tab isActive={tab === 'builder'} onClick={() => setTab('builder')}>
              Builder
            </Tab>
            <Tab isActive={tab === 'yaml'} onClick={() => setTab('yaml')}>
              Advanced YAML
            </Tab>
          </Tabs>
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            {issues.length === 0 && definition && (
              <>
                <Check className="h-3.5 w-3.5 text-success" />
                Basic checks passed
              </>
            )}
            <span>{workspace || 'default'} workspace</span>
          </div>
        </div>
        {tab === 'builder' &&
          (definition && parsed.builderRepresentable ? (
            <ControllerBuilder
              definition={definition}
              workspace={workspace}
              dagSearch={dagSearch}
              onDAGSearchChange={setDAGSearch}
              availableDAGs={dagOptions.data}
              availableDAGsError={
                dagOptions.error
                  ? `Could not load compatible DAGs: ${dagOptions.error.message}`
                  : undefined
              }
              availableDAGsLoading={dagOptions.isLoading}
              onRetryAvailableDAGs={() => void dagOptions.mutate()}
              readOnly={editorReadOnly}
              onDraftDirtyChange={setBuilderDraftDirty}
              onChange={(nextDefinition) =>
                setSource(serializeControllerDefinition(nextDefinition))
              }
            />
          ) : (
            <Alert variant="warning" className="rounded-t-none">
              <AlertTitle>Builder is unavailable</AlertTitle>
              <AlertDescription>
                Fix structural, type, or unknown-field YAML errors in Advanced
                YAML. The source has been preserved unchanged.
              </AlertDescription>
            </Alert>
          ))}
        {tab === 'yaml' && (
          <div className="space-y-3 rounded-b-md border border-t-0 border-border bg-card p-4">
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
              readOnly={editorReadOnly}
              schema={controllerSchema}
              modelUri={`inmemory://dagu/controllers/${id ?? 'new'}.yaml`}
              className="h-[72vh]"
            />
          </div>
        )}
      </div>
      <div
        className={cn(
          'flex min-h-11 flex-wrap items-center justify-between gap-3 rounded-md border px-4 text-xs',
          issues.length === 0
            ? 'border-success/30 bg-success/5 text-success'
            : 'border-destructive/30 bg-destructive/5 text-destructive'
        )}
        aria-label="Validation status"
      >
        <span className="flex items-center gap-2 font-medium">
          {issues.length === 0 ? (
            <>
              <Check className="h-4 w-4" />
              Definition is valid
            </>
          ) : (
            <>
              <AlertTriangle className="h-4 w-4" />
              {issues.length} validation issue
              {issues.length === 1 ? '' : 's'}
            </>
          )}
        </span>
        {!isNew && dirty && (
          <Button variant="ghost" size="xs" onClick={discardPersistedChanges}>
            Discard changes
          </Button>
        )}
      </div>
      <Issues title="Definition needs attention" issues={issues} />
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
