import { useState } from 'react';
import { X } from 'lucide-react';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useLicenseState } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import {
  licenseDaysLeft,
  licenseLink,
  TRIAL_WARNING_DAYS,
} from '@/lib/license';
import dayjs from '@/lib/dayjs';

export function LicenseBanner() {
  const { ts } = useI18n();
  const { license, loading, error, now } = useLicenseState();
  const isAdmin = useIsAdmin();
  const remoteNode = useRemoteNode();
  const [dismissedKeys, setDismissedKeys] = useState<string[]>([]);
  if (loading || error) return null;

  const warning =
    license.error ||
    (license.warningCode
      ? license.warningCode === 'MACHINE_LIMIT_EXCEEDED'
        ? ts(
            'This license is active on more machines than allowed. Deactivate extra machines or contact your administrator.'
          )
        : ts('There is an issue with your license. Contact your administrator.')
      : '');
  if (warning)
    return (
      <div
        role="alert"
        className="border-b border-destructive/30 bg-destructive/10 px-4 py-1.5 text-sm text-destructive"
      >
        {warning}
      </div>
    );
  if (license.community) return null;

  const trial = license.plan === 'trial';
  const action = (label: string) =>
    isAdmin ? (
      <a
        href={licenseLink(trial ? 'plans' : 'manage', 'expiry-banner')}
        target="_blank"
        rel="noopener noreferrer"
        className="underline hover:no-underline"
      >
        {ts(label)}
      </a>
    ) : (
      ts('contact your administrator')
    );
  if (license.gracePeriod)
    return (
      <div
        role="alert"
        className="border-b border-warning/30 bg-warning/10 px-4 py-1.5 text-sm text-warning"
      >
        <I18nTemplate
          text="Your Dagu {license} has expired. Features will be disabled on {date}. Please {action}."
          values={{
            license: ts(trial ? 'trial' : 'license'),
            date:
              license.graceEndsAt && dayjs(license.graceEndsAt).isValid()
                ? dayjs(license.graceEndsAt).format('YYYY-MM-DD')
                : ts('soon'),
            action: action(trial ? 'upgrade' : 'renew'),
          }}
        />
      </div>
    );
  if (!license.valid) return null;

  const days = licenseDaysLeft(license.expiry, now);
  if (days === undefined || days > (trial ? TRIAL_WARNING_DAYS : 30))
    return null;
  const urgent = days <= (trial ? TRIAL_WARNING_DAYS : 7);
  const dismissalKey = `license-banner-${remoteNode}-${license.plan}-${license.expiry}-${urgent ? 'urgent' : 'notice'}`;
  if (
    dismissedKeys.includes(dismissalKey) ||
    localStorage.getItem(dismissalKey) === 'true'
  )
    return null;
  const expiry =
    days === 0
      ? ts('expires today')
      : ts(days === 1 ? 'expires in {count} day' : 'expires in {count} days', {
          count: days,
        });
  return (
    <div
      role={urgent ? 'alert' : 'status'}
      className="border-b border-warning/30 bg-warning/10 px-4 py-1.5 flex items-center justify-between gap-2 text-sm text-warning"
    >
      <I18nTemplate
        text="Your Dagu {license} {expiry}. Please {action}."
        values={{
          license: ts(trial ? 'trial' : 'license'),
          expiry,
          action: action(trial ? 'upgrade' : 'renew'),
        }}
      />
      <button
        type="button"
        onClick={() => {
          localStorage.setItem(dismissalKey, 'true');
          setDismissedKeys((keys) => [...keys, dismissalKey]);
        }}
        className="p-0.5 rounded"
        aria-label={ts('Dismiss license expiry notification')}
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  );
}
