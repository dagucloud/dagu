// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useState } from 'react';
import { ExternalLink, Loader2 } from 'lucide-react';
import {
  LicenseConnectStatusState,
  LicenseMonitoringLevel,
} from '@/api/v1/schema';
import {
  neverSent,
  ReportingChoices,
  type ReportingLevel,
} from '@/components/MonitoringDialog';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { useClock } from '@/hooks/useClock';
import type { LicenseMonitoring } from '@/hooks/useLicenseMonitoring';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { monitoringLevelLabels } from '@/lib/license';
import { LicenseCallout } from './LicenseAlerts';
import type { useLicenseConnect } from './useLicenseConnect';

function Countdown({ expiresAt }: { expiresAt?: string }) {
  const { ts } = useI18n();
  const valid = !!expiresAt && dayjs(expiresAt).isValid();
  // The text is the snapshot of the shared clock: a render when it changes.
  const time = useClock(valid ? 1000 : null, (now) => {
    if (!valid) return '';
    const seconds = Math.max(0, dayjs(expiresAt).diff(dayjs(now), 'second'));
    return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
  });
  if (!valid) return null;
  return (
    <span className="text-xs text-muted-foreground">
      {ts('Expires in {time}', { time })}
    </span>
  );
}

/**
 * Shows a request to connect the selected server to Dagu Console while it
 * waits for approval, or why the last request failed with a way to retry.
 * Otherwise it offers to connect the server so that Dagu Console monitors it
 * for free, reporting from the moment it connects at the level chosen here,
 * or at the level the configuration sets, and optionally to start the trial
 * of paid features in the same approval.
 */
export function ConnectSection({
  connect,
  monitoring,
  disabled,
  trialRequested = false,
}: {
  connect: ReturnType<typeof useLicenseConnect>;
  /** What the server reports; unknown while loading or unavailable. */
  monitoring?: LicenseMonitoring;
  disabled: boolean;
  /** Whether the trial starts out chosen, as when a page asked for it. */
  trialRequested?: boolean;
}) {
  const { ts } = useI18n();
  const {
    status,
    connectUrl,
    starting,
    error,
    failure,
    popupBlocked,
    start,
    retry,
    cancel,
  } = connect;
  const [report, setReport] = useState<ReportingLevel>(
    LicenseMonitoringLevel.errors
  );
  const [trial, setTrial] = useState(trialRequested);
  useEffect(() => {
    if (trialRequested) setTrial(true);
  }, [trialRequested]);
  // A level an administrator chose before, as on a server that connected
  // earlier, stays chosen.
  const chosen =
    monitoring?.chosen && monitoring.level !== LicenseMonitoringLevel.off
      ? monitoring.level
      : undefined;
  useEffect(() => {
    if (chosen) setReport(chosen);
  }, [chosen]);
  const pending = status?.state === LicenseConnectStatusState.pending;
  const configured = monitoring?.configured ? monitoring.level : undefined;

  if (failure)
    return (
      <LicenseCallout
        tone="error"
        title={ts('Could not connect to Dagu Console')}
        action={
          <Button
            size="sm"
            variant="outline"
            disabled={disabled || starting}
            onClick={() => void retry()}
          >
            {starting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {ts('Try again')}
          </Button>
        }
      >
        {failure}
      </LicenseCallout>
    );
  if (!pending)
    return (
      <section
        id="monitoring"
        className="card-obsidian p-4 space-y-3 scroll-mt-4"
        aria-labelledby="license-free-monitoring"
      >
        <div className="space-y-1">
          <h2 id="license-free-monitoring" className="text-sm font-medium">
            {ts('Monitor this server for free')}
          </h2>
          <p className="text-sm">
            {(configured ?? report) === LicenseMonitoringLevel.health
              ? ts(
                  "Dagu Console shows this server's health and emails you when the server stops reporting."
                )
              : ts(
                  "Dagu Console shows this server's runs, failures, and trends, and emails you when a workflow fails or the server stops reporting."
                )}
            {configured !== LicenseMonitoringLevel.off && (
              <> {ts('Reporting starts as soon as the server connects.')}</>
            )}
          </p>
          <p className="text-xs text-muted-foreground">
            {ts(
              trial
                ? 'Free for one server, with 14 days of paid features. After the trial: reports every 5 minutes and keeps 7 days of run history.'
                : 'Free for one server: reports every 5 minutes and keeps 7 days of run history. Paid features stay off.'
            )}
          </p>
        </div>
        {configured ? (
          <div className="space-y-1">
            <p className="flex flex-wrap gap-x-2 text-sm">
              <span className="text-muted-foreground">{ts('Reports')}</span>
              <span>{ts(monitoringLevelLabels[configured])}</span>
            </p>
            <p className="text-xs text-muted-foreground">
              {ts(
                'cloud.report in the server configuration sets this. Change it there and restart Dagu.'
              )}
            </p>
          </div>
        ) : (
          <>
            <ReportingChoices
              selected={report}
              onSelect={setReport}
              disabled={disabled || starting}
              className="sm:grid sm:grid-cols-2 sm:gap-2 sm:space-y-0"
            />
            <p className="text-xs text-muted-foreground">{ts(neverSent)}</p>
          </>
        )}
        <label className="flex items-start gap-2 text-sm">
          <Checkbox
            className="mt-0.5"
            checked={trial}
            disabled={disabled || starting}
            onCheckedChange={(checked) => setTrial(checked === true)}
          />
          <span className="space-y-0.5">
            <span className="block">
              {ts('Also try paid features free for 14 days')}
            </span>
            <span className="block text-xs text-muted-foreground">
              {ts(
                'Audit logs, role-based access, single sign-on, and chat and webhook alerts. No card needed; free monitoring stays when the trial ends.'
              )}
            </span>
          </span>
        </label>
        <div className="flex justify-end">
          <Button
            size="sm"
            variant="primary"
            disabled={disabled || starting}
            onClick={() => void start(configured ? undefined : report, trial)}
          >
            {starting ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <ExternalLink className="h-3.5 w-3.5" />
            )}
            {ts(trial ? 'Connect and start trial' : 'Connect for free')}
          </Button>
        </div>
      </section>
    );
  return (
    <section
      className="card-obsidian p-4 space-y-3"
      aria-labelledby="license-connect"
    >
      <h2 id="license-connect" className="text-sm font-medium">
        {ts('Waiting for approval')}
      </h2>
      <p className="text-sm">
        {ts(
          'Approve this server in Dagu Console. Check that the console shows this code:'
        )}
      </p>
      <p className="font-mono text-lg tracking-widest">{status.code}</p>
      {popupBlocked && (
        <p className="text-sm text-warning">
          {ts(
            'Your browser blocked the new tab. Open Dagu Console to continue.'
          )}
        </p>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button asChild size="sm" variant="primary">
          <a href={connectUrl} target="_blank" rel="noopener noreferrer">
            <ExternalLink className="h-3.5 w-3.5" />
            {ts('Open Dagu Console')}
          </a>
        </Button>
        <Button size="sm" variant="outline" onClick={() => void cancel()}>
          {ts('Cancel')}
        </Button>
        <Countdown expiresAt={status.expiresAt} />
      </div>
    </section>
  );
}
