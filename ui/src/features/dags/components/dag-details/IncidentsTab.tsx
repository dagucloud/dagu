// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  Loader2,
  RefreshCw,
  RotateCcw,
  Save,
  Settings,
  Shield,
} from 'lucide-react';
import { ReactElement, useContext, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useLicense } from '@/hooks/useLicense';
import { AppBarContext } from '@/contexts/AppBarContext';
import { IncidentPolicyScope } from '@/api/v1/schema';
import { IncidentPolicyEditor } from '@/features/incidents/IncidentPolicyEditor';
import {
  DraftIncidentPolicySet,
  policySetDraftFromAPI,
  policySetInput,
} from '@/features/incidents/incidentDrafts';

type IncidentsTabProps = {
  fileName: string;
  workspaceName?: string;
};

function LicenseRequired(): ReactElement {
  return (
    <div className="flex flex-col items-center justify-center gap-4 rounded-md border border-border p-8 text-center">
      <Shield size={40} className="text-muted-foreground" />
      <div>
        <h2 className="text-lg font-semibold text-foreground">
          License Required
        </h2>
        <p className="mt-1 max-w-md text-sm text-muted-foreground">
          Incident connections and routing require an active Dagu license or
          trial. Visit the{' '}
          <Link
            to="/license"
            className="text-primary underline underline-offset-2"
          >
            License
          </Link>{' '}
          page to activate one.
        </p>
      </div>
    </div>
  );
}

function apiErrorMessage(error: unknown, fallback: string): string | null {
  if (!error) return null;
  if (typeof error === 'object' && 'message' in error) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === 'string' && message) return message;
  }
  return fallback;
}

export default function IncidentsTab({
  fileName,
  workspaceName,
}: IncidentsTabProps): ReactElement {
  const license = useLicense();
  const licensed = !license.community && (license.valid || license.gracePeriod);
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const client = useClient();
  const { showToast } = useSimpleToast();
  const [draft, setDraft] = useState<DraftIncidentPolicySet>(() =>
    policySetDraftFromAPI(
      undefined,
      IncidentPolicyScope.dag,
      undefined,
      fileName
    )
  );
  const [saving, setSaving] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [localOverrideStarted, setLocalOverrideStarted] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const {
    data: providersData,
    error: providersError,
    isLoading: providersLoading,
  } = useQuery(
    '/incident-providers',
    whenEnabled(licensed, { params: { query: { remoteNode } } }),
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );
  const {
    data,
    error: loadError,
    isLoading,
    mutate,
  } = useQuery(
    '/dags/{fileName}/incidents',
    whenEnabled(licensed, {
      params: { path: { fileName }, query: { remoteNode } },
    }),
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  const providers = useMemo(
    () => providersData?.providers || [],
    [providersData]
  );
  const isConfigured = !!data?.id || localOverrideStarted;
  const visibleDraft = isConfigured
    ? draft
    : { ...draft, inheritParent: true, policies: [] };
  const effectiveSource = workspaceName
    ? `workspace ${workspaceName}`
    : 'Global';
  const combinedLoadError =
    apiErrorMessage(loadError, 'Failed to load DAG incident routing') ??
    apiErrorMessage(providersError, 'Failed to load incident connections');

  useEffect(() => {
    if (data) {
      setDraft(
        policySetDraftFromAPI(
          data,
          IncidentPolicyScope.dag,
          undefined,
          fileName
        )
      );
      setLocalOverrideStarted(false);
    }
  }, [data, fileName]);

  if (!licensed) {
    return <LicenseRequired />;
  }

  const refresh = async () => {
    setError(null);
    await mutate();
  };

  const configureOverride = () => {
    setLocalOverrideStarted(true);
    setDraft((current) => ({ ...current, inheritParent: false }));
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const response = await client.PUT('/dags/{fileName}/incidents', {
        params: { path: { fileName }, query: { remoteNode } },
        body: policySetInput(draft),
      });
      if (response.error) {
        throw new Error(
          response.error.message || 'Failed to save incident routing'
        );
      }
      await mutate();
      showToast('DAG incident routing saved');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save incident routing'
      );
    } finally {
      setSaving(false);
    }
  };

  const reset = async () => {
    if (!data?.id) {
      setLocalOverrideStarted(false);
      setDraft((current) => ({
        ...current,
        inheritParent: true,
        policies: [],
      }));
      return;
    }
    setResetting(true);
    setError(null);
    try {
      const response = await client.DELETE('/dags/{fileName}/incidents', {
        params: { path: { fileName }, query: { remoteNode } },
      });
      if (response.error) {
        throw new Error(response.error.message || 'Failed to reset incidents');
      }
      await mutate();
      showToast('DAG now inherits incident routing');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to reset incidents'
      );
    } finally {
      setResetting(false);
    }
  };

  return (
    <div className="space-y-5">
      <div className="space-y-4">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div className="space-y-1">
            <h1 className="text-2xl font-semibold tracking-normal text-foreground">
              DAG Incidents
            </h1>
            <p className="text-sm text-muted-foreground">
              This DAG uses {effectiveSource} routing unless you set a DAG
              override.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" onClick={refresh}>
              <RefreshCw className="h-4 w-4" />
              Refresh
            </Button>
            {isConfigured ? (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={reset}
                  disabled={resetting || saving}
                >
                  {resetting ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <RotateCcw className="h-4 w-4" />
                  )}
                  Reset to inherit
                </Button>
                <Button size="sm" onClick={save} disabled={saving}>
                  {saving ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Save className="h-4 w-4" />
                  )}
                  Save changes
                </Button>
              </>
            ) : (
              <Button size="sm" onClick={configureOverride}>
                <Settings className="h-4 w-4" />
                Configure DAG override
              </Button>
            )}
          </div>
        </div>
      </div>

      {combinedLoadError && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{combinedLoadError}</AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {(isLoading || providersLoading) && (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading incident routing
        </div>
      )}

      <IncidentPolicyEditor
        draft={visibleDraft}
        providers={providers}
        allowInherit
        inheritTitle="DAG routing"
        inheritDescription="Use inherited routing, turn incidents off for this DAG, or send this DAG to specific connections."
        onChange={(next) => {
          setLocalOverrideStarted(true);
          setDraft(next);
        }}
      />
    </div>
  );
}
