import { useContext, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { AlertTriangle, Check, Info } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { LicenseStatusBadge } from '@/components/LicenseStatusBadge';
import { LicenseActions } from '@/components/LicenseActions';
import { AppBarContext } from '@/contexts/AppBarContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import { useConfig, type LicenseStatus } from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { useLicenseState } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import {
  hasActiveLicense,
  hasLicensedFeature,
  licensedFeatures,
  licenseLink,
  licensePlanName,
} from '@/lib/license';
import dayjs from '@/lib/dayjs';

export default function LicensePage() {
  const { license, loading, error: statusError } = useLicenseState();
  const { mutate } = useContext(LicenseContext)!;
  const config = useConfig();
  const { setTitle, selectedRemoteNode } = useContext(AppBarContext);
  const remoteNode = selectedRemoteNode || 'local';
  const client = useClient();
  const { ts } = useI18n();
  const [key, setKey] = useState('');
  const [pendingAction, setPendingAction] = useState<
    'activate' | 'deactivate' | null
  >(null);
  const [showDeactivateConfirm, setShowDeactivateConfirm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  useEffect(() => {
    setTitle(ts('Plan & features'));
  }, [setTitle, ts]);

  async function handleActivate(e: React.FormEvent) {
    e.preventDefault();
    if (!key.trim()) return;
    setPendingAction('activate');
    setError(null);
    setSuccessMessage(null);
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
      setSuccessMessage(
        next && hasActiveLicense(next)
          ? ts('{plan} activated. Explore your included features below.', {
              plan: licensePlanName(next),
            })
          : ts('License status updated.')
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : ts('Activation failed'));
    } finally {
      setPendingAction(null);
    }
  }

  async function handleDeactivate() {
    setShowDeactivateConfirm(false);
    setPendingAction('deactivate');
    setError(null);
    setSuccessMessage(null);
    try {
      await mutate(
        async () => {
          const { error: apiError } = await client.POST('/license/deactivate', {
            params: { query: { remoteNode } },
          });
          if (apiError)
            throw new Error(apiError.message || ts('Deactivation failed'));
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
      setSuccessMessage(ts('License deactivated. Running in community mode.'));
    } catch (err) {
      setError(err instanceof Error ? err.message : ts('Deactivation failed'));
    } finally {
      setPendingAction(null);
    }
  }

  const active = hasActiveLicense(license);
  const known = !loading && !statusError;
  return (
    <div className="flex flex-col gap-4 max-w-3xl">
      <div>
        <h1 className="text-lg font-semibold">{ts('Plan & features')}</h1>
        <p className="text-sm text-muted-foreground">
          {ts('Your plan, included features, and license settings.')}
        </p>
      </div>
      <section className="card-obsidian p-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <LicenseStatusBadge />
          <span className="text-xs text-muted-foreground break-all">
            {ts('Server: {name}', { name: remoteNode })}
          </span>
        </div>
        {known && (
          <p className="text-sm">
            {active
              ? ts('Your {plan} license is active.', {
                  plan: licensePlanName(license),
                })
              : license.community && !license.error
                ? ts(
                    'Community includes workflow automation. Add team controls when you need them.'
                  )
                : ts('Review your license status to restore paid features.')}
          </p>
        )}
        {known && license.expiry && dayjs(license.expiry).isValid() && (
          <p className="text-xs text-muted-foreground">
            {ts('Expires')}: {dayjs(license.expiry).format('YYYY-MM-DD')}
          </p>
        )}
        {known && license.gracePeriod && (
          <p className="text-sm text-warning">
            {ts(
              'Features remain available during the grace period. Renew to keep access.'
            )}
          </p>
        )}
        {license.error && (
          <p role="alert" className="text-sm text-destructive">
            {license.error}
          </p>
        )}
        {statusError ? (
          <div
            role="status"
            className="flex flex-wrap items-center gap-2 text-sm"
          >
            <span>{ts('License status unavailable')}</span>
            <Button size="sm" variant="outline" onClick={() => void mutate()}>
              {ts('Retry')}
            </Button>
          </div>
        ) : (
          known &&
          (active && license.plan !== 'trial' ? (
            <Button asChild size="sm" variant="outline">
              <a
                href={licenseLink('manage', 'plan-page')}
                target="_blank"
                rel="noopener noreferrer"
              >
                {ts('Manage license')}
              </a>
            </Button>
          ) : (
            <LicenseActions content="plan-page" activationLink={false} />
          ))
        )}
      </section>
      {successMessage && (
        <div role="status" className="text-sm text-muted-foreground">
          <p>{successMessage}</p>
        </div>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      {known && (
        <section
          id="features"
          className="space-y-2 scroll-mt-4"
          aria-label={ts('Features')}
        >
          <h2 className="text-sm font-semibold">{ts('Features')}</h2>
          <div className="grid gap-3 sm:grid-cols-2">
            {licensedFeatures.map((feature) => {
              const included = hasLicensedFeature(license, feature.id);
              const setup =
                feature.id === 'sso' ||
                (feature.id === 'rbac' && config.authMode !== 'builtin');
              const href =
                feature.id === 'rbac' && setup
                  ? 'https://docs.dagu.sh/server-admin/authentication/builtin'
                  : feature.href;
              return (
                <article
                  key={feature.id}
                  className="card-obsidian p-3 flex flex-col items-start gap-2"
                >
                  <h3 className="text-sm font-medium">{ts(feature.title)}</h3>
                  <p className="text-sm text-muted-foreground flex-1">
                    {ts(feature.description)}
                  </p>
                  <div className="flex w-full flex-wrap items-center justify-between gap-2 text-xs">
                    <span
                      className={
                        included
                          ? 'text-success inline-flex items-center gap-1'
                          : 'text-muted-foreground'
                      }
                    >
                      {included && (
                        <Check className="h-3 w-3" aria-hidden="true" />
                      )}
                      {ts(
                        included
                          ? 'Included'
                          : 'Requires a license with this feature'
                      )}
                    </span>
                    {included &&
                      (setup ? (
                        <a
                          className="underline"
                          href={href}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {ts('Setup guide')}
                        </a>
                      ) : (
                        <Link className="underline" to={href}>
                          {ts('Open feature')}
                        </Link>
                      ))}
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      )}
      <section
        id="activate"
        className="card-obsidian p-4 space-y-3 scroll-mt-4"
      >
        <h2 className="text-sm font-medium">{ts('Activate License Key')}</h2>
        <form onSubmit={handleActivate} className="flex gap-2">
          <Input
            type="password"
            autoComplete="off"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="DAGU-XXXX-XXXX-XXXX-XXXX"
            className="font-mono text-sm h-8"
            aria-label={ts('License key')}
            disabled={Boolean(pendingAction)}
          />
          <Button
            type="submit"
            size="sm"
            className="h-8 shrink-0"
            disabled={Boolean(pendingAction) || !key.trim()}
          >
            {ts(pendingAction === 'activate' ? 'Activating...' : 'Activate')}
          </Button>
        </form>
        <p className="text-xs text-muted-foreground">
          {ts('Enter a license or trial key for this server.')}
        </p>
      </section>
      {known && !license.community && (
        <section className="card-obsidian p-4 space-y-3">
          <h2 className="text-sm font-medium">{ts('Deactivate License')}</h2>
          {license.source === 'env' ? (
            <p className="text-sm text-muted-foreground flex items-start gap-2">
              <Info className="h-4 w-4 shrink-0 mt-0.5" />
              {ts(
                'This license is configured via an environment variable (DAGU_LICENSE or DAGU_LICENSE_KEY). To deactivate, remove the environment variable and restart Dagu.'
              )}
            </p>
          ) : (
            <>
              <p className="text-sm text-muted-foreground">
                {ts(
                  'Remove the license from this machine and return to community mode.'
                )}
              </p>
              <Button
                variant="destructive"
                size="sm"
                disabled={Boolean(pendingAction)}
                onClick={() => setShowDeactivateConfirm(true)}
              >
                <AlertTriangle className="h-3.5 w-3.5" />
                {ts(
                  pendingAction === 'deactivate'
                    ? 'Deactivating...'
                    : 'Deactivate License'
                )}
              </Button>
            </>
          )}
        </section>
      )}
      <ConfirmModal
        title={ts('Deactivate License')}
        buttonText={ts('Deactivate')}
        visible={showDeactivateConfirm}
        dismissModal={() => setShowDeactivateConfirm(false)}
        onSubmit={handleDeactivate}
      >
        <p className="text-sm">
          {ts(
            'This will deactivate the license on this server. Paid features will become unavailable. Existing API keys remain active.'
          )}
        </p>
      </ConfirmModal>
    </div>
  );
}
