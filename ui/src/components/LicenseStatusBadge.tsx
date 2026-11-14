// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Badge } from '@/components/ui/badge';
import { useLicenseState } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import {
  licensePlanName,
  licenseStatus,
  type LicenseTone,
} from '@/lib/license';

const variants: Record<
  LicenseTone,
  'success' | 'secondary' | 'warning' | 'error'
> = {
  ok: 'success',
  neutral: 'secondary',
  warning: 'warning',
  error: 'error',
};

/** Shows the license status, prefixed with the plan unless `plan` is false. */
export function LicenseStatusBadge({ plan = true }: { plan?: boolean }) {
  const { license, loading, error, now } = useLicenseState();
  const { ts } = useI18n();
  let label: string;
  let variant: (typeof variants)[LicenseTone];
  if (loading) {
    label = ts('Checking license…');
    variant = 'secondary';
  } else if (error) {
    label = ts('License status unavailable');
    variant = 'warning';
  } else {
    const status = licenseStatus(license, now);
    const text = ts(status.text, status.values);
    label =
      plan && !license.error && !license.community
        ? `${ts(licensePlanName(license))} · ${text}`
        : text;
    variant = variants[status.tone];
  }
  return (
    <Badge variant={variant} className="max-w-full" title={label}>
      <span className="truncate">{label}</span>
    </Badge>
  );
}
