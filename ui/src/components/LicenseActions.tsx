// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useLicenseState } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import { licenseLink } from '@/lib/license';
import { Button } from './ui/button';

export function LicenseActions({
  content,
  activationLink = true,
}: {
  content: string;
  activationLink?: boolean;
}) {
  const isAdmin = useIsAdmin();
  const { license, loading, error } = useLicenseState();
  const { ts } = useI18n();
  if (loading || error)
    return (
      <p role="status" className="text-sm text-muted-foreground">
        {ts(loading ? 'Checking license…' : 'License status unavailable')}
      </p>
    );
  if (!isAdmin)
    return (
      <p className="text-sm text-muted-foreground">
        {ts(
          'Ask your administrator to manage the license and available features.'
        )}
      </p>
    );
  const action = license.error
    ? 'manage'
    : license.community
      ? 'trial'
      : license.plan === 'trial' || license.valid
        ? 'plans'
        : 'manage';
  const label =
    action === 'trial'
      ? 'Start free trial'
      : action === 'plans'
        ? 'View plans'
        : license.error
          ? 'Manage license'
          : 'Renew license';
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button asChild size="sm" variant="outline">
        <a
          href={licenseLink(action, content)}
          target="_blank"
          rel="noopener noreferrer"
        >
          {ts(label)}
        </a>
      </Button>
      {action !== 'plans' && (
        <Button asChild variant="link" size="sm">
          <a
            href={licenseLink('plans', content)}
            target="_blank"
            rel="noopener noreferrer"
          >
            {ts('View plans')}
          </a>
        </Button>
      )}
      {activationLink && (
        <Button asChild variant="link" size="sm">
          <Link to="/license#activate">{ts('Activate a key')}</Link>
        </Button>
      )}
    </div>
  );
}
