// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';
import { AlertTriangle, ExternalLink } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import {
  isFreeLicense,
  LICENSE_WARNING_DAYS,
  licenseDaysLeft,
  licenseLink,
  TRIAL_WARNING_DAYS,
} from '@/lib/license';

/**
 * A problem that needs an administrator: what is wrong, one line of detail,
 * and the one action that addresses it. Errors interrupt screen readers;
 * warnings do not.
 */
export function LicenseCallout({
  tone,
  title,
  children,
  action,
}: {
  tone: 'warning' | 'error';
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <Alert
      variant={tone === 'error' ? 'destructive' : 'warning'}
      role={tone === 'error' ? 'alert' : 'status'}
      className="py-3"
    >
      <AlertTriangle className="h-4 w-4" />
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0 flex-1">
          <AlertTitle className="text-sm">{title}</AlertTitle>
          {children && (
            <AlertDescription className="text-foreground">
              {children}
            </AlertDescription>
          )}
        </div>
        {action}
      </div>
    </Alert>
  );
}

/** Links out to Dagu Console or the plans page from a callout. */
export function CalloutLink({
  href,
  children,
}: {
  href: string;
  children: ReactNode;
}) {
  return (
    <Button asChild size="sm" variant="outline">
      <a href={href} target="_blank" rel="noopener noreferrer">
        <ExternalLink className="h-3.5 w-3.5" />
        {children}
      </a>
    </Button>
  );
}

/**
 * Shows one callout for each license problem of the selected server: an
 * error, a warning from Dagu Console, the grace period, expiry, or expiry
 * within the warning window. A free license unlocks nothing that its expiry
 * would take away, so it gets no expiry callouts.
 */
export function LicenseAlerts({
  license,
  now,
  consoleHref,
}: {
  license: LicenseStatus;
  now: number;
  consoleHref: string;
}) {
  const { ts } = useI18n();
  const trial = license.plan === 'trial';
  const expiryMatters = !license.community && !isFreeLicense(license);
  const renew = (
    <CalloutLink href={licenseLink(trial ? 'plans' : 'manage', 'plan-page')}>
      {ts(trial ? 'View plans' : 'Renew license')}
    </CalloutLink>
  );
  const days = licenseDaysLeft(license.expiry, now);
  const expiring =
    expiryMatters &&
    license.valid &&
    days !== undefined &&
    days <= (trial ? TRIAL_WARNING_DAYS : LICENSE_WARNING_DAYS);
  const graceEnds =
    license.graceEndsAt && dayjs(license.graceEndsAt).isValid()
      ? dayjs(license.graceEndsAt).format('YYYY-MM-DD')
      : undefined;

  return (
    <>
      {license.error && (
        <LicenseCallout
          tone="error"
          title={ts('License error')}
          action={
            <CalloutLink href={licenseLink('manage', 'plan-page')}>
              {ts('Manage license')}
            </CalloutLink>
          }
        >
          {license.error}
        </LicenseCallout>
      )}
      {license.warningCode && (
        <LicenseCallout
          tone="warning"
          title={ts('License needs attention')}
          action={
            <CalloutLink href={consoleHref}>
              {ts('Manage in Dagu Console')}
            </CalloutLink>
          }
        >
          {license.warningCode === 'MACHINE_LIMIT_EXCEEDED'
            ? ts(
                'This license is active on more machines than allowed. Deactivate extra machines in Dagu Console.'
              )
            : ts('Dagu Console reported {code} for this license.', {
                code: license.warningCode,
              })}
        </LicenseCallout>
      )}
      {license.gracePeriod && expiryMatters && (
        <LicenseCallout
          tone="warning"
          title={ts('License expired')}
          action={renew}
        >
          {graceEnds
            ? ts('Licensed features keep working until {date}.', {
                date: graceEnds,
              })
            : ts('Licensed features keep working for a short grace period.')}
        </LicenseCallout>
      )}
      {!license.valid &&
        !license.gracePeriod &&
        expiryMatters &&
        !license.error && (
          <LicenseCallout
            tone="error"
            title={ts('License expired')}
            action={renew}
          >
            {ts('Licensed features are unavailable.')}
          </LicenseCallout>
        )}
      {expiring && (
        <LicenseCallout
          tone="warning"
          title={
            days === 0
              ? ts('License expires today')
              : ts(
                  days === 1
                    ? 'License expires in {count} day'
                    : 'License expires in {count} days',
                  { count: days }
                )
          }
          action={renew}
        >
          {ts('Licensed features stop working when it expires.')}
        </LicenseCallout>
      )}
    </>
  );
}
