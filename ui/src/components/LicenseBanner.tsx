import { useConfig } from '@/contexts/ConfigContext';
import { LICENSE_CONSOLE_URL } from '@/lib/constants';
import dayjs from '@/lib/dayjs';
import { X } from 'lucide-react';
import * as React from 'react';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';

const warningMessages: Record<string, string> = {
  MACHINE_LIMIT_EXCEEDED:
    'This license is active on more machines than allowed. Deactivate extra machines or contact your administrator.',
};

function daysUntilExpiry(expiryISO: string): number {
  if (!expiryISO) return Infinity;
  return Math.max(0, Math.ceil(dayjs(expiryISO).diff(dayjs(), 'day', true)));
}

export function LicenseBanner() {
  const { ts } = useI18n();
  const config = useConfig();
  const license = config?.license;

  if (!license) return null;

  const expiryKey = license.expiry ?? 'none';
  const [dismissed30d, setDismissed30d] = React.useState(false);
  const [dismissed7d, setDismissed7d] = React.useState(false);

  React.useEffect(() => {
    setDismissed30d(
      localStorage.getItem(`license-banner-dismissed-30d-${expiryKey}`) ===
        'true'
    );
    setDismissed7d(
      localStorage.getItem(`license-banner-dismissed-7d-${expiryKey}`) ===
        'true'
    );
  }, [expiryKey]);

  if (license.error) {
    return (
      <div
        role="alert"
        className="bg-red-50 dark:bg-red-950 border-b border-red-200 dark:border-red-800 px-4 py-1.5 flex items-center text-sm"
      >
        <span className="text-red-800 dark:text-red-200">{license.error}</span>
      </div>
    );
  }

  // Community mode: no banner
  if (license.community) return null;

  const licenseLabel = ts(license.plan === 'trial' ? 'trial' : 'license');
  const renewalLabel = license.plan === 'trial' ? 'upgrade' : 'renew';

  // Warning code banner: non-dismissible
  if (license.warningCode) {
    const message =
      warningMessages[license.warningCode] ??
      'There is an issue with your license. Contact your administrator.';
    return (
      <div
        role="alert"
        className="bg-red-50 dark:bg-red-950 border-b border-red-200 dark:border-red-800 px-4 py-1.5 flex items-center text-sm"
      >
        <span className="text-red-800 dark:text-red-200">{ts(message)}</span>
      </div>
    );
  }

  // Grace period: non-dismissible amber banner
  if (license.gracePeriod) {
    const graceEnd = license.graceEndsAt
      ? dayjs(license.graceEndsAt).format('YYYY-MM-DD')
      : ts('soon');
    return (
      <div
        role="alert"
        className="bg-amber-50 dark:bg-amber-950 border-b border-amber-200 dark:border-amber-800 px-4 py-1.5 flex items-center text-sm"
      >
        <span className="text-amber-800 dark:text-amber-200">
          <I18nTemplate
            text={
              'Your Dagu {license} has expired. Features will be disabled on {date}. Please {action}.'
            }
            values={{
              license: licenseLabel,
              date: graceEnd,
              action: (
                <a
                  href={LICENSE_CONSOLE_URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline hover:no-underline"
                >
                  <I18nText text={renewalLabel} />
                </a>
              ),
            }}
          />
        </span>
      </div>
    );
  }

  // Not valid license (no grace period either): no banner
  if (!license.valid) return null;

  const days = daysUntilExpiry(license.expiry);
  const expiryLabel =
    days === 0
      ? ts('expires today')
      : ts(days === 1 ? 'expires in {count} day' : 'expires in {count} days', {
          count: days,
        });

  // 7-day urgent banner
  if (days <= 7 && !dismissed7d) {
    return (
      <div
        role="alert"
        className="bg-orange-50 dark:bg-orange-950 border-b border-orange-200 dark:border-orange-800 px-4 py-1.5 flex items-center justify-between text-sm"
      >
        <span className="text-orange-800 dark:text-orange-200">
          <I18nTemplate
            text={
              'Your Dagu {license} {expiry}! Please {action} to keep licensed features.'
            }
            values={{
              license: licenseLabel,
              expiry: expiryLabel,
              action: (
                <a
                  href={LICENSE_CONSOLE_URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline hover:no-underline"
                >
                  <I18nText
                    text={
                      license.plan === 'trial' ? 'upgrade now' : 'renew now'
                    }
                  />
                </a>
              ),
            }}
          />
        </span>
        <I18nProps>
          <button
            onClick={() => {
              localStorage.setItem(
                `license-banner-dismissed-7d-${expiryKey}`,
                'true'
              );
              setDismissed7d(true);
            }}
            className="p-0.5 hover:bg-orange-100 dark:hover:bg-orange-900 rounded"
            aria-label="Dismiss license expiry notification"
          >
            <X className="h-4 w-4" />
          </button>
        </I18nProps>
      </div>
    );
  }

  // 30-day warning banner
  if (days <= 30 && !dismissed30d) {
    return (
      <div
        role="status"
        className="bg-yellow-50 dark:bg-yellow-950 border-b border-yellow-200 dark:border-yellow-800 px-4 py-1.5 flex items-center justify-between text-sm"
      >
        <span className="text-yellow-800 dark:text-yellow-200">
          <I18nTemplate
            text={'Your Dagu {license} {expiry}. Please {action}.'}
            values={{
              license: licenseLabel,
              expiry: expiryLabel,
              action: (
                <a
                  href={LICENSE_CONSOLE_URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline hover:no-underline"
                >
                  <I18nText
                    text={
                      license.plan === 'trial'
                        ? 'upgrade to avoid disruption'
                        : 'renew to avoid disruption'
                    }
                  />
                </a>
              ),
            }}
          />
        </span>
        <I18nProps>
          <button
            onClick={() => {
              localStorage.setItem(
                `license-banner-dismissed-30d-${expiryKey}`,
                'true'
              );
              setDismissed30d(true);
            }}
            className="p-0.5 hover:bg-yellow-100 dark:hover:bg-yellow-900 rounded"
            aria-label="Dismiss license expiry notification"
          >
            <X className="h-4 w-4" />
          </button>
        </I18nProps>
      </div>
    );
  }

  return null;
}
