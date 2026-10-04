// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { LicenseStatus } from '@/contexts/ConfigContext';
import { LICENSE_CONSOLE_URL } from './constants';
import dayjs from './dayjs';

export const TRIAL_WARNING_DAYS = 3;

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

export function hasActiveLicense(license: LicenseStatus): boolean {
  return !license.community && (license.valid || license.gracePeriod);
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

export function licenseDaysLeft(
  expiry: string,
  now = Date.now()
): number | undefined {
  if (!expiry || !dayjs(expiry).isValid()) return undefined;
  return Math.max(0, Math.ceil(dayjs(expiry).diff(dayjs(now), 'day', true)));
}

export function licenseLink(
  action: 'trial' | 'plans' | 'manage',
  content: string
): string {
  const url = new URL(
    action === 'plans' ? 'https://dagu.sh/pricing' : LICENSE_CONSOLE_URL
  );
  url.searchParams.set('source', 'dagu-ui');
  url.searchParams.set('medium', 'product');
  url.searchParams.set('content', content);
  url.searchParams.set('deployment', 'self-hosted');
  if (action === 'trial') {
    url.pathname = '/signup';
    url.searchParams.set('intent', 'license-trial');
    url.searchParams.set('plan', 'trial');
  }
  return url.toString();
}
