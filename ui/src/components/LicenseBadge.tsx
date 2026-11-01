// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';
import { AlertTriangle } from 'lucide-react';
import { Link } from 'react-router-dom';
import { LicenseMonitoringLevel } from '@/api/v1/schema';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useLicenseState } from '@/hooks/useLicense';
import {
  useLicenseMonitoring,
  type LicenseMonitoring,
} from '@/hooks/useLicenseMonitoring';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import dayjs from '@/lib/dayjs';
import {
  isFreeLicense,
  licensePlanLabel,
  licenseStatus,
  monitoringLevelLabels,
  monitoringStale,
  type LicenseStatusText,
} from '@/lib/license';
import { cn } from '@/lib/utils';
import { LicenseStatusBadge } from './LicenseStatusBadge';
import RelativeTime from './ui/relative-time';
import { Tooltip, TooltipTrigger, TooltipContent } from './ui/tooltip';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
} from './ui/dropdown-menu';

/** How often monitoring is re-read here, to notice a server that stopped reporting. */
const MONITORING_REFRESH_MS = 60_000;

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </>
  );
}

function MonitoringValue({
  monitoring,
  stale,
}: {
  monitoring: LicenseMonitoring;
  stale: boolean;
}) {
  const { ts } = useI18n();
  if (stale)
    return (
      <span className="text-warning">
        {ts('Not reporting since {time}', {
          time: dayjs(monitoring.lastReportAt).format('YYYY-MM-DD HH:mm'),
        })}
      </span>
    );
  const level = ts(monitoringLevelLabels[monitoring.level]);
  if (monitoring.level === LicenseMonitoringLevel.off) return level;
  return (
    <I18nTemplate
      text="{level}, last report {time}"
      values={{
        level,
        time: (
          <RelativeTime
            timestamp={monitoring.lastReportAt}
            fallback={ts('Not yet')}
          />
        ),
      }}
    />
  );
}

/**
 * States the plan and workspace of the selected server in one line, or what
 * needs attention instead; a free license states that it monitors only.
 * Collapsed, it shows only an alert icon, and only when something needs
 * attention. Details open on hover or focus.
 */
export function LicenseBadge({
  onNavigate,
  compact = false,
}: {
  onNavigate?: () => void;
  compact?: boolean;
}) {
  const isAdmin = useIsAdmin();
  const remoteNode = useRemoteNode();
  const { license, loading, error, now } = useLicenseState();
  const { ts } = useI18n();
  const known = !loading && !error;
  // The monitoring API is admin-only, and only servers that check in report.
  const online = isAdmin && known && Boolean(license.serverId);
  const { monitoring } = useLicenseMonitoring(remoteNode, {
    enabled: online,
    refreshInterval: MONITORING_REFRESH_MS,
  });
  const stale = online && monitoringStale(monitoring, now);

  const status = licenseStatus(license, now);
  const free = isFreeLicense(license);
  let attention: LicenseStatusText | undefined;
  if (known && (status.tone === 'warning' || status.tone === 'error')) {
    attention = status;
  } else if (stale) {
    attention = { text: 'Not reporting', tone: 'warning' };
  }
  const detail = attention
    ? ts(attention.text, attention.values)
    : free
      ? ts(status.text)
      : license.workspace;
  const label = loading
    ? ts('Checking license…')
    : error
      ? ts('License status unavailable')
      : license.community && attention
        ? detail
        : [licensePlanLabel(license, ts), detail].filter(Boolean).join(' · ');
  const tone = attention
    ? attention.tone === 'error'
      ? 'text-destructive'
      : 'text-warning'
    : 'text-muted-foreground hover:text-foreground';

  if (compact && !attention) return null;

  const expiry =
    known && !free && license.expiry && dayjs(license.expiry).isValid()
      ? dayjs(license.expiry).format('YYYY-MM-DD')
      : undefined;
  const details = (
    <div className="space-y-2 max-w-72">
      <LicenseStatusBadge />
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
        {known && license.workspace && (
          <Row label={ts('Workspace')}>{license.workspace}</Row>
        )}
        <Row label={ts('Server')}>{license.serverName || remoteNode}</Row>
        {expiry && <Row label={ts('Expires')}>{expiry}</Row>}
        {online && monitoring && (
          <Row label={ts('Monitoring')}>
            <MonitoringValue monitoring={monitoring} stale={stale} />
          </Row>
        )}
      </dl>
      <p>
        {ts(
          isAdmin
            ? 'Manage plan & features'
            : 'Ask your administrator to manage the license and available features.'
        )}
      </p>
    </div>
  );
  const className = cn(
    'rounded-sm align-middle text-xs font-normal focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
    compact ? 'inline-flex p-1' : 'inline-block min-w-0 max-w-full truncate',
    tone
  );
  const content = compact ? (
    <AlertTriangle className="h-4 w-4" aria-hidden="true" />
  ) : (
    label
  );
  return isAdmin ? (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          to="/license"
          onClick={onNavigate}
          aria-label={ts('Plan & features')}
          className={className}
        >
          {content}
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
              {content}
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
