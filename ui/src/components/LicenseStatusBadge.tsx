// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Badge } from '@/components/ui/badge';
import { useLicenseState } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import {
  licenseDaysLeft,
  licensePlanName,
  TRIAL_WARNING_DAYS,
} from '@/lib/license';
import { Shield, ShieldCheck } from 'lucide-react';

export function LicenseStatusBadge() {
  const { license, loading, error, now } = useLicenseState();
  const { ts } = useI18n();
  const days = licenseDaysLeft(license.expiry, now);
  let status = ts('Active');
  let variant: 'secondary' | 'warning' | 'error' = 'secondary';
  if (loading) {
    status = ts('Checking license…');
    variant = 'secondary';
  } else if (error) {
    status = ts('License status unavailable');
    variant = 'warning';
  } else if (license.error) {
    status = ts('License Error');
    variant = 'error';
  } else if (license.warningCode) {
    status = ts('License needs attention');
    variant = 'warning';
  } else if (license.gracePeriod) {
    status = ts('Grace Period');
    variant = 'warning';
  } else if (license.community) {
    status = ts('Community');
    variant = 'secondary';
  } else if (!license.valid) {
    status = ts('Expired');
    variant = 'error';
  } else if (license.plan === 'trial' && days !== undefined) {
    status =
      days === 0
        ? ts('Expires today')
        : ts(days === 1 ? '{count} day left' : '{count} days left', {
            count: days,
          });
    variant = days <= TRIAL_WARNING_DAYS ? 'warning' : 'secondary';
  }
  const label =
    loading || error || license.error || license.community
      ? status
      : `${ts(licensePlanName(license))} · ${status}`;
  const Icon = license.valid && !license.community ? ShieldCheck : Shield;
  return (
    <Badge variant={variant} className="max-w-full" title={label}>
      <Icon aria-hidden="true" className="shrink-0" />
      <span className="truncate">{label}</span>
    </Badge>
  );
}
