// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  LicenseMonitoringLevel,
  LicenseRemoteAccessLevel,
  type components,
} from '@/api/v1/schema';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import type { StaticTranslationValues } from '@/i18n/staticMessages';
import { LICENSE_CONSOLE_URL } from './constants';
import dayjs from './dayjs';

type LicenseMonitoring = components['schemas']['LicenseMonitoring'];

export const TRIAL_WARNING_DAYS = 3;

/** Days before a paid license expires when Dagu starts warning about it. */
export const LICENSE_WARNING_DAYS = 30;

/**
 * How long a server set to report may go without a report before it counts
 * as not reporting. Servers report about once a minute.
 */
export const MONITORING_STALE_MS = 10 * 60_000;

export const licensedFeatures = [
  {
    id: 'audit',
    title: 'Audit logs',
    description: 'Trace who changed or ran workflows.',
    href: '/audit-logs',
  },
  {
    id: 'rbac',
    title: 'Role-based access',
    description: 'Control what teammates can view and change.',
    href: '/users',
  },
  {
    id: 'sso',
    title: 'Single sign-on',
    description: 'Sign in through your organization’s identity provider.',
    href: 'https://docs.dagu.sh/server-admin/authentication/oidc',
  },
  {
    id: 'incidents',
    title: 'Incident routing',
    description: 'Open and resolve incidents when workflows fail.',
    href: '/incident-policies',
  },
  {
    id: 'api-keys',
    title: 'More API keys',
    description: 'Create API keys beyond the Community limit of two.',
    href: '/api-keys',
  },
] as const;

export type LicensedFeature = (typeof licensedFeatures)[number]['id'];

/**
 * Reports whether the license is on the free plan, which lets Dagu Console
 * monitor the server and unlocks no paid features.
 */
export function isFreeLicense(license: LicenseStatus): boolean {
  return !license.community && license.plan === 'free';
}

/**
 * Reports whether an environment variable supplies the license, so the UI can
 * neither replace nor remove it.
 */
export function isLicenseManagedByEnv(license: LicenseStatus): boolean {
  return license.connectedVia
    ? license.connectedVia === 'env'
    : license.source === 'env';
}

/**
 * Reports whether any license, free included, is valid or in its grace
 * period.
 */
export function hasLicense(license: LicenseStatus): boolean {
  return !license.community && (license.valid || license.gracePeriod);
}

/**
 * Reports whether a license that unlocks paid features is valid or in its
 * grace period. The free plan unlocks none.
 */
export function hasActiveLicense(license: LicenseStatus): boolean {
  return hasLicense(license) && !isFreeLicense(license);
}

export function hasLicensedFeature(
  license: LicenseStatus,
  feature: LicensedFeature
): boolean {
  return (
    hasActiveLicense(license) &&
    (feature === 'incidents' ||
      feature === 'api-keys' ||
      license.features.includes(feature))
  );
}

export function licensePlanName(license: LicenseStatus): string {
  if (license.community) return 'Community';
  const plan = license.plan.trim();
  if (plan === 'minimum' || plan === 'team') return 'Team';
  return plan ? plan.charAt(0).toUpperCase() + plan.slice(1) : 'Licensed';
}

/** The highest role each remote access level gives calls from Dagu Cloud. */
export const remoteAccessLevelLabels: Record<LicenseRemoteAccessLevel, string> =
  {
    [LicenseRemoteAccessLevel.off]: 'Off',
    [LicenseRemoteAccessLevel.viewer]: 'Viewer',
    [LicenseRemoteAccessLevel.operator]: 'Operator',
    [LicenseRemoteAccessLevel.developer]: 'Developer',
  };

/** What each monitoring level reports, as a short label. */
export const monitoringLevelLabels: Record<LicenseMonitoringLevel, string> = {
  [LicenseMonitoringLevel.off]: 'Off',
  [LicenseMonitoringLevel.health]: 'Health only',
  [LicenseMonitoringLevel.runs]: 'Health and run status',
  [LicenseMonitoringLevel.errors]: 'Health, run status, and error messages',
};

/** Names the plan as a plan ("Team plan"), or "Community" without one. */
export function licensePlanLabel(
  license: LicenseStatus,
  ts: (source: string, values?: StaticTranslationValues) => string
): string {
  const name = ts(licensePlanName(license));
  return license.community ? name : ts('{plan} plan', { plan: name });
}

/** How a license status reads: in force, informational, or a problem. */
export type LicenseTone = 'ok' | 'neutral' | 'warning' | 'error';

/** A license status as a phrase to translate with its values. */
export type LicenseStatusText = {
  text: string;
  values?: StaticTranslationValues;
  tone: LicenseTone;
};

/**
 * Describes where a license stands, most urgent condition first. A trial
 * always shows when it expires; a paid license only within
 * LICENSE_WARNING_DAYS. Expiry is a warning within TRIAL_WARNING_DAYS for a
 * trial and LICENSE_WARNING_DAYS for a paid license, as in LicenseBanner. A
 * free license, which unlocks nothing that could lapse, never shows expiry.
 */
export function licenseStatus(
  license: LicenseStatus,
  now = Date.now()
): LicenseStatusText {
  if (license.error) return { text: 'License error', tone: 'error' };
  if (license.warningCode) return { text: 'Needs attention', tone: 'warning' };
  if (isFreeLicense(license))
    return { text: 'Monitoring only', tone: 'neutral' };
  if (license.gracePeriod) return { text: 'Grace period', tone: 'warning' };
  if (license.community) return { text: 'Community', tone: 'neutral' };
  if (!license.valid) return { text: 'Expired', tone: 'error' };
  const trial = license.plan === 'trial';
  const warningDays = trial ? TRIAL_WARNING_DAYS : LICENSE_WARNING_DAYS;
  const days = licenseDaysLeft(license.expiry, now);
  if (days === undefined || (!trial && days > warningDays))
    return { text: 'Active', tone: 'ok' };
  return {
    text:
      days === 0
        ? 'Expires today'
        : days === 1
          ? 'Expires in {count} day'
          : 'Expires in {count} days',
    values: { count: days },
    tone: days <= warningDays ? 'warning' : 'neutral',
  };
}

/**
 * Reports whether a server set to report has sent nothing for longer than
 * MONITORING_STALE_MS. A server that has not reported yet is not judged.
 */
export function monitoringStale(
  monitoring: LicenseMonitoring | undefined,
  now = Date.now()
): boolean {
  if (
    !monitoring ||
    monitoring.level === LicenseMonitoringLevel.off ||
    !monitoring.lastReportAt
  )
    return false;
  const last = dayjs(monitoring.lastReportAt);
  return last.isValid() && now - last.valueOf() > MONITORING_STALE_MS;
}

export function licenseDaysLeft(
  expiry: string,
  now = Date.now()
): number | undefined {
  if (!expiry || !dayjs(expiry).isValid()) return undefined;
  return Math.max(0, Math.ceil(dayjs(expiry).diff(dayjs(now), 'day', true)));
}

export function licenseLink(
  action: 'plans' | 'manage',
  content: string
): string {
  const url = new URL(
    action === 'plans' ? 'https://dagu.sh/pricing' : LICENSE_CONSOLE_URL
  );
  url.searchParams.set('source', 'dagu-ui');
  url.searchParams.set('medium', 'product');
  url.searchParams.set('content', content);
  url.searchParams.set('deployment', 'self-hosted');
  return url.toString();
}
