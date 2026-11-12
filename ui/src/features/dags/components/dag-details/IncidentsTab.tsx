// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { LicenseFeaturePrompt } from '@/components/LicenseFeaturePrompt';
import { hasActiveLicense } from '@/lib/license';
import {
  AlertTriangle,
  Loader2,
  RefreshCw,
  RotateCcw,
  Save,
  Settings,
} from 'lucide-react';
import { ReactElement, useEffect, useMemo, useRef, useState } from 'react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useLicense } from '@/hooks/useLicense';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { IncidentPolicyScope } from '@/api/v1/schema';
import { IncidentPolicyEditor } from '@/features/incidents/IncidentPolicyEditor';
import {
  DraftIncidentPolicySet,
  policySetDraftFromAPI,
  policySetInput,
} from '@/features/incidents/incidentDrafts';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type IncidentsTabProps = {
  fileName: string;
  workspaceName?: string;
  /** Called with the unsaved-changes state when it changes, and once on mount. */
  onDirtyChange?: (dirty: boolean) => void;
};

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
  onDirtyChange,
}: IncidentsTabProps): ReactElement {
  const license = useLicense();
  const licensed = hasActiveLicense(license);
  const remoteNode = useRemoteNode();
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
  const [confirmReset, setConfirmReset] = useState(false);
  const [localOverrideStarted, setLocalOverrideStarted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const onDirtyChangeRef = useRef(onDirtyChange);
  onDirtyChangeRef.current = onDirtyChange;
  useEffect(() => {
    onDirtyChangeRef.current?.(dirty);
  }, [dirty]);

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
      setDirty(false);
    }
  }, [data, fileName]);

  if (!licensed) {
    return <LicenseFeaturePrompt feature="incidents" />;
  }

  const refresh = async () => {
    setError(null);
    await mutate();
  };

  const configureOverride = () => {
    setLocalOverrideStarted(true);
    setDirty(true);
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
      setDirty(false);
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
      setDirty(false);
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
      setDirty(false);
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
    <div className="@container space-y-5">
      <div className="space-y-4">
        <div className="flex flex-col gap-3 @3xl:flex-row @3xl:items-start @3xl:justify-between">
          <div className="min-w-0 max-w-2xl space-y-1">
            <h2 className="text-lg font-semibold text-foreground">
              <I18nText text={'Workflow incidents'} />
            </h2>
            <p className="text-sm text-muted-foreground">
              <I18nTemplate
                text="This workflow uses {source} routing unless you set a workflow override."
                values={{
                  source: workspaceName ? (
                    <I18nText
                      text="workspace {name}"
                      values={{ name: workspaceName }}
                    />
                  ) : (
                    <I18nText text="Global" />
                  ),
                }}
              />
            </p>
          </div>
          <div className="flex min-w-0 flex-wrap items-center gap-2 @3xl:shrink-0 @3xl:justify-end">
            <Button variant="outline" size="sm" onClick={refresh}>
              <RefreshCw className="h-4 w-4" />
              <I18nText text={'Refresh'} />
            </Button>
            {isConfigured ? (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => (data?.id ? setConfirmReset(true) : reset())}
                  disabled={resetting || saving}
                >
                  {resetting ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <RotateCcw className="h-4 w-4" />
                  )}
                  <I18nText text={'Reset to inherit'} />
                </Button>
                <Button size="sm" onClick={save} disabled={saving}>
                  {saving ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Save className="h-4 w-4" />
                  )}
                  <I18nText text={'Save changes'} />
                </Button>
              </>
            ) : (
              <Button size="sm" onClick={configureOverride}>
                <Settings className="h-4 w-4" />
                <I18nText text={'Configure workflow override'} />
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
          <I18nText text={'Loading incident routing'} />
        </div>
      )}

      {/* A save or a reset replaces the draft when it ends: no edit until then. */}
      <fieldset className="min-w-0" disabled={saving || resetting}>
        <IncidentPolicyEditor
          draft={visibleDraft}
          providers={providers}
          allowInherit
          inheritTitle="DAG routing"
          inheritDescription="Use inherited routing, turn incidents off for this DAG, or send this DAG to specific connections."
          onChange={(next) => {
            setLocalOverrideStarted(true);
            setDirty(true);
            setDraft(next);
          }}
        />
      </fieldset>

      <I18nProps>
        <ConfirmModal
          title="Reset to inherit"
          buttonText="Reset"
          visible={confirmReset}
          dismissModal={() => setConfirmReset(false)}
          onSubmit={() => {
            setConfirmReset(false);
            void reset();
          }}
        >
          <p>
            <I18nText
              text={
                "Remove this workflow's incident routing and use the inherited routing instead?"
              }
            />
          </p>
        </ConfirmModal>
      </I18nProps>
    </div>
  );
}
