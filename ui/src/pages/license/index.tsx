import { useContext, useEffect, useRef, useState } from 'react';
import { useLocation, useSearchParams } from 'react-router-dom';
import { ExternalLink } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { MonitoringDialog } from '@/components/MonitoringDialog';
import { ConnectSection } from './ConnectSection';
import { FeatureList } from './FeatureList';
import { CalloutLink, LicenseAlerts, LicenseCallout } from './LicenseAlerts';
import { LicenseOverview } from './LicenseOverview';
import { MonitoringSection } from './MonitoringSection';
import { RemoteAccessSection } from './RemoteAccessSection';
import { useLicenseConnect } from './useLicenseConnect';
import { AppBarContext } from '@/contexts/AppBarContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { useLicenseState } from '@/hooks/useLicense';
import { useLicenseMonitoring } from '@/hooks/useLicenseMonitoring';
import { useLicenseRemoteAccess } from '@/hooks/useLicenseRemoteAccess';
import {
  LicenseConnectStatusState,
  LicenseMonitoringLevel,
} from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import {
  hasLicense,
  isFreeLicense,
  isLicenseManagedByEnv,
  licenseLink,
} from '@/lib/license';

/** How often the time of the last report is refreshed. */
const MONITORING_REFRESH_MS = 30_000;

/** How often the state of the remote access connection is refreshed. */
const REMOTE_ACCESS_REFRESH_MS = 10_000;

/** Explains how to stop using a license that Dagu loads again on restart. */
const restartNotes: Record<string, string> = {
  config:
    'license.key in the config file activates this license again when Dagu restarts. Remove it to stop using the license.',
  file: 'The license file is loaded again when Dagu restarts. Remove it to stop using the license.',
};

export default function LicensePage() {
  const { license, loading, error: statusError, now } = useLicenseState();
  const { mutate } = useContext(LicenseContext)!;
  const { setTitle, selectedRemoteNode } = useContext(AppBarContext);
  const remoteNode = selectedRemoteNode || 'local';
  const client = useClient();
  const { ts } = useI18n();
  const { hash, key: locationKey } = useLocation();
  const [searchParams] = useSearchParams();
  const trialRequested = searchParams.get('trial') === '1';
  const [key, setKey] = useState('');
  const [keyOpen, setKeyOpen] = useState(false);
  const [pendingAction, setPendingAction] = useState<
    'activate' | 'deactivate' | null
  >(null);
  const [showDeactivateConfirm, setShowDeactivateConfirm] = useState(false);
  const [activateError, setActivateError] = useState<string | null>(null);
  const [deactivateError, setDeactivateError] = useState<string | null>(null);
  const [notice, setNotice] = useState<{
    text: string;
    releaseFailed?: boolean;
  } | null>(null);
  const connect = useLicenseConnect(remoteNode);
  const granted = connect.status?.state === LicenseConnectStatusState.granted;
  const { clear: clearConnect } = connect;
  // Only a license that checks in with Dagu Console can report to it.
  const online = !loading && !statusError && Boolean(license.serverId);
  // The offer to connect shows a level that the configuration sets.
  const offersConnect =
    !loading &&
    !statusError &&
    license.community &&
    !isLicenseManagedByEnv(license);
  const monitoring = useLicenseMonitoring(remoteNode, {
    enabled: online || offersConnect,
    refreshInterval: online ? MONITORING_REFRESH_MS : 0,
  });
  const { refresh: refreshMonitoring } = monitoring;
  const remoteAccess = useLicenseRemoteAccess(remoteNode, {
    enabled: online,
    refreshInterval: online ? REMOTE_ACCESS_REFRESH_MS : 0,
  });
  const [choosingLevel, setChoosingLevel] = useState(false);
  // Set while a Dagu Console page opened from here may change the plan.
  const consoleOpened = useRef(false);

  useEffect(() => {
    setTitle(ts('Plan & features'));
  }, [setTitle, ts]);

  // Each visit to #activate, even a repeated one, opens the key form, which
  // takes focus, in view. A visit to #monitoring waits for its section, which
  // appears once the license status is known.
  const handledVisit = useRef<string | null>(null);
  useEffect(() => {
    if (hash !== '#activate' && hash !== '#monitoring') return;
    const target = document.getElementById(hash.slice(1));
    if (!target || handledVisit.current === locationKey) return;
    handledVisit.current = locationKey;
    if (hash === '#activate') setKeyOpen(true);
    target.scrollIntoView({ block: 'start' });
  }, [hash, locationKey, loading, statusError, online]);

  useEffect(() => {
    if (!granted) return;
    // Handle each approval once, even if this effect runs again later.
    clearConnect();
    setActivateError(null);
    setDeactivateError(null);
    void mutate().then(async (next) => {
      setNotice({
        text:
          next && hasLicense(next)
            ? next.workspace
              ? ts('Connected to {workspace}.', { workspace: next.workspace })
              : ts('Connected to Dagu Console.')
            : ts('License status updated.'),
      });
      if (!next?.serverId) return;
      try {
        const { data } = await client.GET('/license/monitoring', {
          params: { query: { remoteNode } },
        });
        // Replaces the status read before connecting, which the level chosen
        // when connecting has changed.
        if (data) void refreshMonitoring(data, { revalidate: false });
      } catch {
        // The next refresh reads it.
      }
    });
  }, [
    granted,
    clearConnect,
    mutate,
    ts,
    client,
    remoteNode,
    refreshMonitoring,
  ]);

  // A plan changed in Dagu Console, as when a trial started there, shows as
  // soon as the person comes back from it: to this tab, or to this window
  // from Console in another.
  useEffect(() => {
    if (!online) return;
    const onReturn = () => {
      if (document.hidden || !consoleOpened.current) return;
      consoleOpened.current = false;
      client
        .POST('/license/refresh', { params: { query: { remoteNode } } })
        .then(({ data }) => {
          if (data) void mutate(data, { revalidate: false });
        })
        .catch(() => undefined);
    };
    document.addEventListener('visibilitychange', onReturn);
    window.addEventListener('focus', onReturn);
    return () => {
      document.removeEventListener('visibilitychange', onReturn);
      window.removeEventListener('focus', onReturn);
    };
  }, [online, client, remoteNode, mutate]);

  async function handleActivate(e: React.FormEvent) {
    e.preventDefault();
    if (!key.trim()) return;
    setPendingAction('activate');
    setActivateError(null);
    setNotice(null);
    try {
      const next = await mutate(
        async () => {
          const { error: apiError } = await client.POST('/license/activate', {
            params: { query: { remoteNode } },
            body: { key: key.trim() },
          });
          if (apiError)
            throw new Error(apiError.message || ts('Activation failed'));
          const status = await client.GET('/license/status', {
            params: { query: { remoteNode } },
          });
          if (!status.data) throw new Error(ts('License status unavailable'));
          return status.data;
        },
        { revalidate: true }
      );
      setKey('');
      setKeyOpen(false);
      setNotice({
        text:
          next && hasLicense(next)
            ? ts('License activated.')
            : ts('License status updated.'),
      });
    } catch (err) {
      setActivateError(
        err instanceof Error ? err.message : ts('Activation failed')
      );
    } finally {
      setPendingAction(null);
    }
  }

  async function handleDeactivate() {
    setShowDeactivateConfirm(false);
    setPendingAction('deactivate');
    setDeactivateError(null);
    setNotice(null);
    const disconnecting = connectedToConsole;
    let releaseFailed = false;
    try {
      await mutate(
        async () => {
          const { data, error: apiError } = await client.POST(
            '/license/deactivate',
            { params: { query: { remoteNode } } }
          );
          if (apiError)
            throw new Error(apiError.message || ts('Deactivation failed'));
          releaseFailed = Boolean(data?.releaseFailed);
          return {
            valid: false,
            plan: '',
            features: [],
            expiry: '',
            gracePeriod: false,
            graceEndsAt: '',
            community: true,
            source: '',
            warningCode: '',
            error: '',
          } satisfies LicenseStatus;
        },
        { revalidate: true }
      );
      setNotice(
        releaseFailed
          ? {
              text: ts(
                'Disconnected here, but Dagu Console could not be reached. Disconnect this server in Dagu Console to free its slot.'
              ),
              releaseFailed,
            }
          : {
              text: ts(
                disconnecting
                  ? 'Disconnected. Running in community mode.'
                  : 'License deactivated. Running in community mode.'
              ),
            }
      );
    } catch (err) {
      setDeactivateError(
        err instanceof Error ? err.message : ts('Deactivation failed')
      );
    } finally {
      setPendingAction(null);
    }
  }

  const known = !loading && !statusError;
  const via = license.connectedVia;
  const connectedToConsole = via === 'console' || via === 'key';
  const managedByEnv = isLicenseManagedByEnv(license);
  const free = isFreeLicense(license);
  const restartNote = via ? restartNotes[via] : undefined;
  const consoleHref = license.consoleUrl || licenseLink('manage', 'plan-page');
  const busy = Boolean(pendingAction);

  return (
    <div className="flex flex-col gap-4 max-w-4xl">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold tracking-[-0.01em]">
            {ts('Plan & features')}
          </h1>
          <p className="text-sm text-muted-foreground">
            {ts(
              'License, Dagu Console connection, monitoring, and remote access for this server.'
            )}
          </p>
        </div>
        {known && !license.community && (
          <div className="flex flex-wrap items-center gap-2">
            {free && (
              <Button asChild size="sm" variant="outline">
                <a
                  href={consoleHref}
                  target="_blank"
                  rel="noopener noreferrer"
                  onClick={() => (consoleOpened.current = true)}
                >
                  {ts('Start free trial')}
                </a>
              </Button>
            )}
            <Button asChild size="sm" variant="outline">
              <a
                href={consoleHref}
                target="_blank"
                rel="noopener noreferrer"
                onClick={() => (consoleOpened.current = true)}
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {ts('Manage in Dagu Console')}
              </a>
            </Button>
          </div>
        )}
      </div>

      {notice &&
        (notice.releaseFailed ? (
          <LicenseCallout
            tone="warning"
            title={ts('Disconnected')}
            action={
              <CalloutLink href={licenseLink('manage', 'plan-page')}>
                {ts('Open Dagu Console')}
              </CalloutLink>
            }
          >
            {notice.text}
          </LicenseCallout>
        ) : (
          <p role="status" className="text-sm text-muted-foreground">
            {notice.text}
          </p>
        ))}
      {statusError ? (
        <LicenseCallout
          tone="warning"
          title={ts('License status unavailable')}
          action={
            <Button size="sm" variant="outline" onClick={() => void mutate()}>
              {ts('Retry')}
            </Button>
          }
        />
      ) : (
        known && (
          <LicenseAlerts
            license={license}
            now={now}
            consoleHref={consoleHref}
          />
        )
      )}
      {offersConnect && (
        <ConnectSection
          connect={connect}
          monitoring={monitoring.monitoring}
          disabled={busy}
          trialRequested={trialRequested}
        />
      )}

      {loading && (
        <p role="status" className="text-sm text-muted-foreground">
          {ts('Checking license…')}
        </p>
      )}
      {known && (
        <LicenseOverview
          license={license}
          remoteNode={remoteNode}
          busy={busy}
        />
      )}
      {online && (
        <MonitoringSection
          monitoring={monitoring.monitoring}
          now={now}
          loadError={Boolean(monitoring.error)}
          onRetry={() => void monitoring.refresh()}
          onChoose={() => setChoosingLevel(true)}
          onTurnOff={() => monitoring.setLevel(LicenseMonitoringLevel.off)}
        />
      )}
      {online && (
        <RemoteAccessSection
          remoteAccess={remoteAccess.remoteAccess}
          remoteNode={remoteNode}
          showActivity={license.features.includes('audit')}
          loadError={Boolean(remoteAccess.error)}
          onRetry={() => void remoteAccess.refresh()}
          onChange={remoteAccess.update}
        />
      )}
      {known && <FeatureList license={license} />}

      <section
        id="activate"
        className="card-obsidian p-4 space-y-3 scroll-mt-4"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="min-w-0 flex-1 space-y-1">
            <h2 className="text-sm font-medium">{ts('License key')}</h2>
            <p className="text-xs text-muted-foreground">
              {ts('Enter a server key or license key from Dagu Console.')}
            </p>
          </div>
          {!keyOpen && (
            <Button
              size="sm"
              variant="outline"
              aria-expanded={false}
              aria-controls="license-key-form"
              onClick={() => setKeyOpen(true)}
            >
              {ts(
                license.community
                  ? 'Use a license key'
                  : 'Use a different license key'
              )}
            </Button>
          )}
        </div>
        {keyOpen && (
          <form
            id="license-key-form"
            onSubmit={handleActivate}
            className="flex flex-wrap gap-2"
          >
            <Input
              type="password"
              autoComplete="off"
              autoFocus
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="DAGU-XXXX-XXXX-XXXX-XXXX"
              className="font-mono text-sm h-8 min-w-0 flex-1 basis-60"
              aria-label={ts('License key')}
              disabled={busy}
            />
            <div className="flex gap-2">
              <Button
                type="submit"
                size="sm"
                variant="primary"
                className="h-8"
                disabled={busy || !key.trim()}
              >
                {ts(
                  pendingAction === 'activate' ? 'Activating...' : 'Activate'
                )}
              </Button>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                className="h-8"
                disabled={busy}
                onClick={() => {
                  setKeyOpen(false);
                  setKey('');
                  setActivateError(null);
                }}
              >
                {ts('Cancel')}
              </Button>
            </div>
          </form>
        )}
        {activateError && (
          <p role="alert" className="text-sm text-destructive">
            {activateError}
          </p>
        )}
      </section>

      {known && !license.community && (
        <section
          className="rounded-md border border-destructive/30 p-4 space-y-3"
          aria-labelledby="license-danger-zone"
        >
          <h2 id="license-danger-zone" className="text-sm font-medium">
            {ts('Danger zone')}
          </h2>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0 flex-1 space-y-1">
              <p className="text-sm font-medium">
                {ts(
                  connectedToConsole
                    ? 'Disconnect this server'
                    : 'Deactivate License'
                )}
              </p>
              <p className="text-xs text-muted-foreground">
                {managedByEnv ? (
                  ts(
                    'This license is configured via an environment variable (DAGU_LICENSE or DAGU_LICENSE_KEY). To deactivate, remove the environment variable and restart Dagu.'
                  )
                ) : (
                  <>
                    {ts(
                      connectedToConsole
                        ? 'Disconnecting frees this server’s slot in Dagu Console and returns it to community mode.'
                        : 'Remove the license from this machine and return to community mode.'
                    )}
                    {restartNote && <> {ts(restartNote)}</>}
                  </>
                )}
              </p>
            </div>
            {!managedByEnv && (
              <Button
                variant="destructive"
                size="sm"
                disabled={busy}
                onClick={() => setShowDeactivateConfirm(true)}
              >
                {connectedToConsole
                  ? ts(
                      pendingAction === 'deactivate'
                        ? 'Disconnecting...'
                        : 'Disconnect'
                    )
                  : ts(
                      pendingAction === 'deactivate'
                        ? 'Deactivating...'
                        : 'Deactivate License'
                    )}
              </Button>
            )}
          </div>
          {deactivateError && (
            <p role="alert" className="text-sm text-destructive">
              {deactivateError}
            </p>
          )}
        </section>
      )}

      <MonitoringDialog
        open={choosingLevel}
        level={monitoring.monitoring?.level}
        onSave={async (level) => {
          await monitoring.setLevel(level);
          setChoosingLevel(false);
        }}
        onCancel={() => setChoosingLevel(false)}
      />
      <ConfirmModal
        title={ts(
          connectedToConsole ? 'Disconnect this server' : 'Deactivate License'
        )}
        buttonText={ts(connectedToConsole ? 'Disconnect' : 'Deactivate')}
        visible={showDeactivateConfirm}
        dismissModal={() => setShowDeactivateConfirm(false)}
        onSubmit={handleDeactivate}
      >
        <p className="text-sm">
          {ts(
            free
              ? 'Dagu Console stops monitoring this server, and its free slot becomes available to another server.'
              : connectedToConsole
                ? 'Paid features become unavailable on this server and its slot in Dagu Console is freed for another server. Existing API keys remain active.'
                : 'This will deactivate the license on this server. Paid features will become unavailable. Existing API keys remain active.'
          )}
        </p>
      </ConfirmModal>
    </div>
  );
}
