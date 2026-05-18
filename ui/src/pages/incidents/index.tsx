// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  BellRing,
  CheckCircle2,
  FlaskConical,
  Loader2,
  Plus,
  Route as RouteIcon,
  Save,
  ServerCog,
  Trash2,
} from 'lucide-react';
import { ReactElement, useContext, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import Title from '@/components/ui/title';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient, useQuery } from '@/hooks/api';
import { useLicense } from '@/hooks/useLicense';
import { WorkspaceKind, workspaceNameForSelection } from '@/lib/workspace';
import { IncidentPolicyScope, IncidentProviderType } from '@/api/v1/schema';
import { IncidentPolicyEditor } from '@/features/incidents/IncidentPolicyEditor';
import {
  blankProvider,
  DraftIncidentPolicySet,
  DraftProvider,
  INCIDENT_PROVIDER_TYPES,
  IncidentProvider,
  incidentRoutingMode,
  policySetDraftFromAPI,
  policySetInput,
  providerDraftFromAPI,
  providerInput,
  providerLabel,
} from '@/features/incidents/incidentDrafts';

type IncidentHomeLink = {
  to: string;
  label: string;
  description: string;
};

function apiErrorMessage(error: unknown, fallback: string): string | null {
  if (!error) return null;
  if (typeof error === 'object' && 'message' in error) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === 'string' && message) return message;
  }
  return fallback;
}

function IncidentHomeLinks({
  links,
}: {
  links: IncidentHomeLink[];
}): ReactElement {
  return (
    <section className="space-y-2">
      <h3 className="text-xs font-semibold uppercase text-muted-foreground">
        Setup
      </h3>
      <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
        {links.map((link) => (
          <Link
            key={link.to}
            to={link.to}
            className="rounded-md border border-border bg-card px-4 py-3 transition-colors hover:border-border-strong hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <span className="block text-sm font-medium text-foreground">
              {link.label}
            </span>
            <span className="mt-1 block text-xs text-muted-foreground">
              {link.description}
            </span>
          </Link>
        ))}
      </div>
    </section>
  );
}

export default function IncidentsPage(): ReactElement {
  const { setTitle } = useContext(AppBarContext);

  useEffect(() => {
    setTitle('Incidents');
  }, [setTitle]);

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <Title>Incidents</Title>
      <IncidentHomeLinks
        links={[
          {
            to: '/incident-providers',
            label: 'Connections',
            description: 'Connect PagerDuty or SolarWinds Incident Response.',
          },
          {
            to: '/incident-policies',
            label: 'Routing',
            description: 'Choose where Global and workspace incidents go.',
          },
        ]}
      />
    </div>
  );
}

function IncidentSectionTabs({
  active,
}: {
  active: 'providers' | 'policies';
}): ReactElement {
  return (
    <div className="flex items-center gap-1 border-b border-border">
      {active === 'providers' ? (
        <span className="inline-flex h-10 items-center gap-2 border-b-2 border-primary px-3 text-sm font-medium text-foreground">
          <ServerCog className="h-4 w-4 text-primary" />
          Connections
        </span>
      ) : (
        <Link
          to="/incident-providers"
          className="inline-flex h-10 items-center gap-2 border-b-2 border-transparent px-3 text-sm font-medium text-muted-foreground hover:text-foreground"
        >
          <ServerCog className="h-4 w-4" />
          Connections
        </Link>
      )}

      {active === 'policies' ? (
        <span className="inline-flex h-10 items-center gap-2 border-b-2 border-primary px-3 text-sm font-medium text-foreground">
          <RouteIcon className="h-4 w-4 text-primary" />
          Routing
        </span>
      ) : (
        <Link
          to="/incident-policies"
          className="inline-flex h-10 items-center gap-2 border-b-2 border-transparent px-3 text-sm font-medium text-muted-foreground hover:text-foreground"
        >
          <RouteIcon className="h-4 w-4" />
          Routing
        </Link>
      )}
    </div>
  );
}

function ProvidersHeader({ onAdd }: { onAdd: () => void }): ReactElement {
  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-normal text-foreground">
            Incident Connections
          </h1>
          <p className="text-sm text-muted-foreground">
            Connections are configured once, then selected by Global, workspace,
            or DAG incident routing.
          </p>
        </div>
        <Button size="sm" onClick={onAdd}>
          <Plus className="h-4 w-4" />
          Add connection
        </Button>
      </div>
      <IncidentSectionTabs active="providers" />
    </div>
  );
}

type ProviderCardProps = {
  draft: DraftProvider;
  isNew?: boolean;
  saving: boolean;
  testing: boolean;
  onChange: (draft: DraftProvider) => void;
  onSave: () => void;
  onDelete?: () => void;
  onTest?: () => void;
};

function ProviderCard({
  draft,
  isNew = false,
  saving,
  testing,
  onChange,
  onSave,
  onDelete,
  onTest,
}: ProviderCardProps): ReactElement {
  const secretConfigured =
    draft.type === IncidentProviderType.pagerduty
      ? draft.routingKeyConfigured
      : draft.webhookUrlConfigured;
  const secretPreview =
    draft.type === IncidentProviderType.pagerduty
      ? draft.routingKeyPreview
      : draft.webhookUrlPreview;

  const handleTypeChange = (type: IncidentProviderType) => {
    const oldDefault = providerLabel(draft.type);
    const nextDefault = providerLabel(type);
    onChange({
      ...draft,
      type,
      name:
        !draft.name.trim() || draft.name.trim() === oldDefault
          ? nextDefault
          : draft.name,
    });
  };

  return (
    <div className="rounded-md border border-border bg-card p-4">
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-semibold text-foreground">
              {draft.name || 'New connection'}
            </h2>
            <Badge variant={draft.enabled ? 'success' : 'default'}>
              {draft.enabled ? 'Enabled' : 'Disabled'}
            </Badge>
            <Badge variant="outline">{providerLabel(draft.type)}</Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            {secretConfigured
              ? `Secret configured${secretPreview ? ` (${secretPreview})` : ''}`
              : 'Secret not configured'}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {!isNew && (
            <Button
              variant="outline"
              size="sm"
              onClick={onTest}
              disabled={testing || saving || !secretConfigured}
            >
              {testing ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <FlaskConical className="h-4 w-4" />
              )}
              Test
            </Button>
          )}
          <Button size="sm" onClick={onSave} disabled={saving}>
            {saving ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Save className="h-4 w-4" />
            )}
            Save
          </Button>
          {!isNew && onDelete && (
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-destructive hover:text-destructive"
              onClick={onDelete}
              aria-label="Delete incident connection"
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          )}
        </div>
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <div className="space-y-2">
          <label className="text-xs font-medium text-muted-foreground">
            Name
          </label>
          <Input
            value={draft.name}
            onChange={(event) =>
              onChange({ ...draft, name: event.target.value })
            }
          />
        </div>
        <div className="space-y-2">
          <label className="text-xs font-medium text-muted-foreground">
            Type
          </label>
          <Select value={draft.type} onValueChange={handleTypeChange}>
            <SelectTrigger className="h-7">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {INCIDENT_PROVIDER_TYPES.map((type) => (
                <SelectItem key={type} value={type}>
                  {providerLabel(type)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="mt-4 flex flex-wrap items-center gap-4">
        <label className="flex items-center gap-2 text-sm text-foreground">
          <Switch
            checked={draft.enabled}
            onCheckedChange={(checked) =>
              onChange({ ...draft, enabled: checked })
            }
          />
          Enabled
        </label>
      </div>

      {draft.type === IncidentProviderType.pagerduty ? (
        <div className="mt-4 space-y-2">
          <label className="text-xs font-medium text-muted-foreground">
            Events API v2 routing key
          </label>
          <Input
            type="password"
            value={draft.routingKey}
            placeholder={
              draft.routingKeyConfigured
                ? 'Leave blank to keep existing routing key'
                : 'Paste routing key'
            }
            onChange={(event) =>
              onChange({ ...draft, routingKey: event.target.value })
            }
          />
        </div>
      ) : (
        <div className="mt-4 space-y-4">
          <div className="space-y-2">
            <label className="text-xs font-medium text-muted-foreground">
              Incoming webhook URL
            </label>
            <Input
              type="password"
              value={draft.webhookUrl}
              placeholder={
                draft.webhookUrlConfigured
                  ? 'Leave blank to keep existing webhook URL'
                  : 'Paste SolarWinds incoming webhook URL'
              }
              onChange={(event) =>
                onChange({ ...draft, webhookUrl: event.target.value })
              }
            />
          </div>
          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2 text-sm text-foreground">
              <Switch
                checked={draft.allowInsecureHttp}
                onCheckedChange={(checked) =>
                  onChange({ ...draft, allowInsecureHttp: checked })
                }
              />
              Allow HTTP
            </label>
            <label className="flex items-center gap-2 text-sm text-foreground">
              <Switch
                checked={draft.allowPrivateNetwork}
                onCheckedChange={(checked) =>
                  onChange({ ...draft, allowPrivateNetwork: checked })
                }
              />
              Allow private network
            </label>
          </div>
        </div>
      )}
    </div>
  );
}

export function IncidentProvidersPage(): ReactElement {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const client = useClient();
  const { showToast } = useSimpleToast();
  const [drafts, setDrafts] = useState<DraftProvider[]>([]);
  const [newDraft, setNewDraft] = useState<DraftProvider | null>(null);
  const [savingId, setSavingId] = useState<string | null>(null);
  const [testingId, setTestingId] = useState<string | null>(null);
  const [deleteDraft, setDeleteDraft] = useState<DraftProvider | null>(null);
  const [error, setError] = useState<string | null>(null);
  const {
    data,
    error: loadError,
    isLoading,
    mutate,
  } = useQuery(
    '/incident-providers',
    { params: { query: { remoteNode } } },
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  useEffect(() => {
    appBarContext.setTitle('Incident Connections');
  }, [appBarContext]);

  useEffect(() => {
    if (data) {
      setDrafts((data.providers || []).map(providerDraftFromAPI));
    }
  }, [data]);

  const updateDraft = (index: number, draft: DraftProvider) => {
    setDrafts((current) =>
      current.map((item, itemIndex) => (itemIndex === index ? draft : item))
    );
  };

  const saveProvider = async (draft: DraftProvider) => {
    setSavingId(draft.id || '__new__');
    setError(null);
    try {
      const body = providerInput(draft);
      const response = draft.id
        ? await client.PUT('/incident-providers/{providerId}', {
            params: { path: { providerId: draft.id }, query: { remoteNode } },
            body,
          })
        : await client.POST('/incident-providers', {
            params: { query: { remoteNode } },
            body,
          });
      if (response.error) {
        throw new Error(response.error.message || 'Failed to save connection');
      }
      await mutate();
      setNewDraft(null);
      showToast('Incident connection saved');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save connection'
      );
    } finally {
      setSavingId(null);
    }
  };

  const deleteProvider = async () => {
    if (!deleteDraft?.id) return;
    setSavingId(deleteDraft.id);
    setError(null);
    try {
      const response = await client.DELETE('/incident-providers/{providerId}', {
        params: { path: { providerId: deleteDraft.id }, query: { remoteNode } },
      });
      if (response.error) {
        throw new Error(
          response.error.message || 'Failed to delete connection'
        );
      }
      await mutate();
      showToast('Incident connection deleted');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to delete connection'
      );
    } finally {
      setDeleteDraft(null);
      setSavingId(null);
    }
  };

  const testProvider = async (draft: DraftProvider) => {
    if (!draft.id) return;
    setTestingId(draft.id);
    setError(null);
    try {
      const response = await client.POST(
        '/incident-providers/{providerId}/test',
        {
          params: { path: { providerId: draft.id }, query: { remoteNode } },
        }
      );
      if (response.error || !response.data?.result.delivered) {
        throw new Error(
          response.error?.message ||
            response.data?.result.error ||
            'Test incident delivery failed'
        );
      }
      showToast('Test incident sent');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Test incident delivery failed'
      );
    } finally {
      setTestingId(null);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <ProvidersHeader onAdd={() => setNewDraft(blankProvider())} />
      {apiErrorMessage(loadError, 'Failed to load incident connections') && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>
            {apiErrorMessage(loadError, 'Failed to load incident connections')}
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {isLoading && (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading incident connections
        </div>
      )}
      {newDraft && (
        <ProviderCard
          draft={newDraft}
          isNew
          saving={savingId === '__new__'}
          testing={false}
          onChange={setNewDraft}
          onSave={() => saveProvider(newDraft)}
        />
      )}
      {!isLoading && drafts.length === 0 && !newDraft ? (
        <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
          No incident connections configured.
        </div>
      ) : null}
      <div className="space-y-3">
        {drafts.map((draft, index) => (
          <ProviderCard
            key={draft.id || index}
            draft={draft}
            saving={savingId === draft.id}
            testing={testingId === draft.id}
            onChange={(next) => updateDraft(index, next)}
            onSave={() => saveProvider(draft)}
            onDelete={() => setDeleteDraft(draft)}
            onTest={() => testProvider(draft)}
          />
        ))}
      </div>
      <ConfirmDialog
        title="Delete incident connection?"
        buttonText="Delete"
        visible={!!deleteDraft}
        dismissModal={() => setDeleteDraft(null)}
        onSubmit={deleteProvider}
      >
        {deleteDraft
          ? `${deleteDraft.name} cannot be deleted while it is used by routing.`
          : ''}
      </ConfirmDialog>
    </div>
  );
}

type ScopeKey = 'global' | 'workspace';

function PoliciesHeader({
  saving,
  onSave,
}: {
  saving: boolean;
  onSave: () => void;
}): ReactElement {
  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-normal text-foreground">
            Incident Routing
          </h1>
          <p className="text-sm text-muted-foreground">
            Effective order is DAG, workspace, then Global.
          </p>
        </div>
        <Button size="sm" onClick={onSave} disabled={saving}>
          {saving ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <Save className="h-4 w-4" />
          )}
          Save routing
        </Button>
      </div>
      <IncidentSectionTabs active="policies" />
    </div>
  );
}

function ScopeSelector({
  activeScope,
  workspaceName,
  globalDraft,
  workspaceDraft,
  onChange,
}: {
  activeScope: ScopeKey;
  workspaceName: string;
  globalDraft: DraftIncidentPolicySet;
  workspaceDraft: DraftIncidentPolicySet;
  onChange: (scope: ScopeKey) => void;
}): ReactElement {
  const canUseWorkspace = !!workspaceName;
  const globalMode = incidentRoutingMode(globalDraft);
  const workspaceMode = incidentRoutingMode(workspaceDraft);
  return (
    <div className="rounded-md border border-border bg-card p-4">
      <h2 className="text-sm font-semibold text-foreground">Scope</h2>
      <div className="mt-3 grid gap-2 md:grid-cols-2">
        <button
          type="button"
          onClick={() => onChange('global')}
          className={`rounded-md border px-3 py-3 text-left transition-colors ${
            activeScope === 'global'
              ? 'border-primary bg-primary/10'
              : 'border-border hover:bg-muted'
          }`}
        >
          <div className="flex items-center justify-between gap-3">
            <span className="text-sm font-medium text-foreground">Global</span>
            <Badge variant={globalMode === 'custom' ? 'success' : 'default'}>
              {globalMode === 'custom'
                ? `${globalDraft.policies.length} route${globalDraft.policies.length === 1 ? '' : 's'}`
                : 'Off'}
            </Badge>
          </div>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            Default route for DAGs without workspace or DAG overrides.
          </p>
        </button>

        <button
          type="button"
          onClick={() => canUseWorkspace && onChange('workspace')}
          disabled={!canUseWorkspace}
          className={`rounded-md border px-3 py-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60 ${
            activeScope === 'workspace'
              ? 'border-primary bg-primary/10'
              : 'border-border hover:bg-muted'
          }`}
        >
          <div className="flex items-center justify-between gap-3">
            <span className="text-sm font-medium text-foreground">
              Workspace
            </span>
            <Badge variant={workspaceMode === 'custom' ? 'success' : 'default'}>
              {workspaceMode === 'inherit'
                ? 'Inherit'
                : workspaceMode === 'custom'
                  ? `${workspaceDraft.policies.length} route${workspaceDraft.policies.length === 1 ? '' : 's'}`
                  : 'Off'}
            </Badge>
          </div>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            {workspaceName
              ? `${workspaceName} override`
              : 'Select one workspace in the sidebar.'}
          </p>
        </button>
      </div>
    </div>
  );
}

export function IncidentPoliciesPage(): ReactElement {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const client = useClient();
  const { showToast } = useSimpleToast();
  const selectedWorkspaceName = workspaceNameForSelection(
    appBarContext.workspaceSelection
  );
  const canConfigureWorkspace =
    appBarContext.workspaceSelection?.kind === WorkspaceKind.workspace &&
    !!selectedWorkspaceName;
  const [activeScope, setActiveScope] = useState<ScopeKey>('global');
  const [globalDraft, setGlobalDraft] = useState<DraftIncidentPolicySet>(() =>
    policySetDraftFromAPI(undefined, IncidentPolicyScope.global)
  );
  const [workspaceDraft, setWorkspaceDraft] = useState<DraftIncidentPolicySet>(
    () =>
      policySetDraftFromAPI(
        undefined,
        IncidentPolicyScope.workspace,
        selectedWorkspaceName
      )
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const {
    data: providersData,
    error: providersError,
    isLoading: providersLoading,
  } = useQuery(
    '/incident-providers',
    { params: { query: { remoteNode } } },
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );
  const {
    data: globalData,
    error: globalError,
    isLoading: globalLoading,
    mutate: mutateGlobal,
  } = useQuery(
    '/incident-policies/global',
    { params: { query: { remoteNode } } },
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );
  const {
    data: workspaceData,
    error: workspaceError,
    isLoading: workspaceLoading,
    mutate: mutateWorkspace,
  } = useQuery(
    '/incident-policies/workspaces/{workspaceName}',
    canConfigureWorkspace
      ? {
          params: {
            path: { workspaceName: selectedWorkspaceName },
            query: { remoteNode },
          },
        }
      : null,
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  const providers = useMemo(
    () => providersData?.providers || [],
    [providersData]
  );
  const isLoading =
    providersLoading ||
    globalLoading ||
    (canConfigureWorkspace && workspaceLoading);
  const loadError =
    apiErrorMessage(providersError, 'Failed to load incident connections') ??
    apiErrorMessage(globalError, 'Failed to load Global incident routing') ??
    apiErrorMessage(
      workspaceError,
      'Failed to load workspace incident routing'
    );
  const activeDraft = activeScope === 'global' ? globalDraft : workspaceDraft;

  useEffect(() => {
    appBarContext.setTitle('Incident Routing');
  }, [appBarContext]);

  useEffect(() => {
    if (!canConfigureWorkspace && activeScope === 'workspace') {
      setActiveScope('global');
    }
  }, [activeScope, canConfigureWorkspace]);

  useEffect(() => {
    if (globalData) {
      setGlobalDraft(
        policySetDraftFromAPI(globalData, IncidentPolicyScope.global)
      );
    }
  }, [globalData]);

  useEffect(() => {
    setWorkspaceDraft(
      policySetDraftFromAPI(
        workspaceData,
        IncidentPolicyScope.workspace,
        selectedWorkspaceName
      )
    );
  }, [selectedWorkspaceName, workspaceData]);

  const savePolicies = async () => {
    setSaving(true);
    setError(null);
    try {
      const draft = activeScope === 'global' ? globalDraft : workspaceDraft;
      const body = policySetInput(draft);
      if (activeScope === 'workspace' && !selectedWorkspaceName) {
        setError('Select a workspace before saving workspace routing.');
        return;
      }
      const response =
        activeScope === 'global'
          ? await client.PUT('/incident-policies/global', {
              params: { query: { remoteNode } },
              body,
            })
          : await client.PUT('/incident-policies/workspaces/{workspaceName}', {
              params: {
                path: { workspaceName: selectedWorkspaceName },
                query: { remoteNode },
              },
              body,
            });
      if (response.error) {
        throw new Error(response.error.message || 'Failed to save routing');
      }
      if (activeScope === 'global') {
        await mutateGlobal();
      } else {
        await mutateWorkspace();
      }
      showToast('Incident routing saved');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save routing');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <PoliciesHeader saving={saving} onSave={savePolicies} />

      <Alert variant="info">
        <CheckCircle2 className="h-4 w-4" />
        <AlertDescription>
          Incidents open only after automatic retries are exhausted. Recovery
          resolves the same incident.
        </AlertDescription>
      </Alert>

      {loadError && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{loadError}</AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {isLoading && (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading incident routing
        </div>
      )}

      <ScopeSelector
        activeScope={activeScope}
        workspaceName={selectedWorkspaceName}
        globalDraft={globalDraft}
        workspaceDraft={workspaceDraft}
        onChange={setActiveScope}
      />

      <IncidentPolicyEditor
        draft={activeDraft}
        providers={providers}
        allowInherit={activeScope === 'workspace'}
        inheritTitle={activeScope === 'global' ? 'Global' : 'Workspace'}
        inheritDescription={
          activeScope === 'global'
            ? 'Default routing for DAGs without a workspace or DAG override.'
            : 'Use Global routing, turn incidents off, or choose workspace connections.'
        }
        onChange={(next) =>
          activeScope === 'global'
            ? setGlobalDraft(next)
            : setWorkspaceDraft(next)
        }
      />
    </div>
  );
}
