// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import {
  consoleBaseURL,
  hasActiveLicense,
  hasLicense,
  hasLicensedFeature,
  isFreeLicense,
  licensePlanName,
  licenseStatus,
} from '../license';

const now = new Date('2026-10-01T00:00:00Z').getTime();

const team: LicenseStatus = {
  valid: true,
  plan: 'team',
  expiry: '2026-10-05T00:00:00Z',
  features: ['audit', 'rbac', 'sso'],
  gracePeriod: false,
  community: false,
  source: 'file',
  warningCode: '',
};
// Free tokens claim no features; this one claims them all to show that the
// plan alone decides.
const free: LicenseStatus = { ...team, plan: 'free' };
const community: LicenseStatus = {
  ...team,
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  community: true,
  source: '',
};

describe('free license', () => {
  it('is told apart from paid and community licenses', () => {
    expect(isFreeLicense(free)).toBe(true);
    expect(isFreeLicense(team)).toBe(false);
    expect(isFreeLicense(community)).toBe(false);
  });

  it('counts as a license but not as one that unlocks paid features', () => {
    expect(hasLicense(free)).toBe(true);
    expect(hasActiveLicense(free)).toBe(false);
    expect(hasLicense(team)).toBe(true);
    expect(hasActiveLicense(team)).toBe(true);
    expect(hasLicense(community)).toBe(false);
  });

  it.each(['audit', 'rbac', 'sso', 'incidents', 'api-keys'] as const)(
    'does not include %s',
    (feature) => {
      expect(hasLicensedFeature(free, feature)).toBe(false);
      expect(hasLicensedFeature(team, feature)).toBe(true);
    }
  );

  it('is named Free', () => {
    expect(licensePlanName(free)).toBe('Free');
  });

  it.each([
    ['a valid license', {}],
    ['an expiry within days', { expiry: '2026-10-02T00:00:00Z' }],
    ['the grace period', { valid: false, gracePeriod: true }],
    ['an expired token', { valid: false }],
  ])('reads as monitoring only with %s', (_case, overrides) => {
    expect(licenseStatus({ ...free, ...overrides }, now)).toEqual({
      text: 'Monitoring only',
      tone: 'neutral',
    });
  });

  it('still reports errors and warnings', () => {
    expect(licenseStatus({ ...free, error: 'Invalid token' }, now).tone).toBe(
      'error'
    );
    expect(
      licenseStatus({ ...free, warningCode: 'MACHINE_LIMIT_EXCEEDED' }, now)
        .tone
    ).toBe('warning');
  });
});

describe('console base URL', () => {
  it.each([
    ['https://console.dagu.sh/servers?server=srv-1', 'https://console.dagu.sh'],
    [
      'https://example.com/dagu-console/servers?server=srv-1',
      'https://example.com/dagu-console',
    ],
    [undefined, 'https://console.dagu.sh'],
  ])('reads %s as %s', (consoleUrl, base) => {
    expect(consoleBaseURL({ ...team, consoleUrl })).toBe(base);
  });
});
