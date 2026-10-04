// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useLicenseState } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { licensePlanName } from '@/lib/license';
import { LicenseStatusBadge } from './LicenseStatusBadge';
import { Tooltip, TooltipTrigger, TooltipContent } from './ui/tooltip';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
} from './ui/dropdown-menu';

export function LicenseBadge({ onNavigate }: { onNavigate?: () => void }) {
  const isAdmin = useIsAdmin();
  const remoteNode = useRemoteNode();
  const { license, loading, error } = useLicenseState();
  const { ts } = useI18n();
  const label = loading
    ? ts('Checking license…')
    : error
      ? ts('License status unavailable')
      : ts(licensePlanName(license));
  const details = (
    <div className="space-y-2 max-w-64">
      <LicenseStatusBadge />
      <p className="break-all">{ts('Server: {name}', { name: remoteNode })}</p>
      {!loading &&
        !error &&
        license.expiry &&
        dayjs(license.expiry).isValid() && (
          <p>
            {ts('Expires')}: {dayjs(license.expiry).format('YYYY-MM-DD')}
          </p>
        )}
      <p>
        {ts(
          isAdmin
            ? 'Manage plan & features'
            : 'Ask your administrator to manage the license and available features.'
        )}
      </p>
    </div>
  );
  const className =
    'inline-block min-w-0 max-w-full truncate rounded-sm align-middle text-xs font-normal text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';
  return isAdmin ? (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          to="/license"
          onClick={onNavigate}
          aria-label={ts('Plan & features')}
          className={className}
        >
          {label}
        </Link>
      </TooltipTrigger>
      <TooltipContent>{details}</TooltipContent>
    </Tooltip>
  ) : (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label={ts('Plan & features')}
              className={className}
            >
              {label}
            </button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{details}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent
        align="start"
        className="max-w-[calc(100vw-2rem)] p-3 text-xs"
      >
        {details}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
