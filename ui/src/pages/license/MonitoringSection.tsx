// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, type ReactNode } from 'react';
import { Info } from 'lucide-react';
import { LicenseMonitoringLevel } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import RelativeTime from '@/components/ui/relative-time';
import type { LicenseMonitoring } from '@/hooks/useLicenseMonitoring';
import { useI18n } from '@/i18n/I18nProvider';

const levelLabels: Record<LicenseMonitoringLevel, string> = {
  [LicenseMonitoringLevel.off]: 'Off',
  [LicenseMonitoringLevel.health]: 'Health only',
  [LicenseMonitoringLevel.runs]: 'Health and run status',
};

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  );
}

/** Indents a report for reading, or returns it as is if it is not JSON. */
function formatReport(report: string): string {
  try {
    return JSON.stringify(JSON.parse(report), null, 2);
  } catch {
    return report;
  }
}

/**
 * Shows what this server reports to Dagu Console, when it last did, and the
 * last report itself, and lets an administrator change or stop it unless the
 * configuration fixes it.
 */
export function MonitoringSection({
  monitoring,
  loadError,
  onRetry,
  onChoose,
  onTurnOff,
}: {
  monitoring?: LicenseMonitoring;
  loadError: boolean;
  onRetry: () => void;
  onChoose: () => void;
  onTurnOff: () => Promise<void>;
}) {
  const { ts } = useI18n();
  const [showReport, setShowReport] = useState(false);
  const [turningOff, setTurningOff] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reporting =
    monitoring !== undefined && monitoring.level !== LicenseMonitoringLevel.off;

  async function turnOff() {
    setTurningOff(true);
    setError(null);
    try {
      await onTurnOff();
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : ts('Could not change what this server reports.')
      );
    } finally {
      setTurningOff(false);
    }
  }

  return (
    <section
      className="card-obsidian p-4 space-y-3"
      aria-labelledby="license-monitoring"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="license-monitoring" className="text-sm font-medium">
          {ts('Monitoring')}
        </h2>
        {monitoring?.configured && (
          <span className="text-xs text-muted-foreground">
            {ts('Set in configuration')}
          </span>
        )}
      </div>
      <p className="text-sm text-muted-foreground">
        {ts(
          "Dagu Console shows this server's health on its Servers page. With run status, it also emails the workspace's owners and admins when a workflow fails."
        )}
      </p>
      {loadError && !monitoring ? (
        <div
          role="status"
          className="flex flex-wrap items-center gap-2 text-sm"
        >
          <span>{ts('Monitoring status unavailable')}</span>
          <Button size="sm" variant="outline" onClick={onRetry}>
            {ts('Retry')}
          </Button>
        </div>
      ) : (
        monitoring && (
          <>
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
              <Row label={ts('Reports')}>
                {ts(levelLabels[monitoring.level])}
              </Row>
              {reporting && (
                <Row label={ts('Last report')}>
                  <RelativeTime
                    timestamp={monitoring.lastReportAt}
                    fallback={ts('Not yet')}
                  />
                </Row>
              )}
            </dl>
            {monitoring.configured && (
              <p className="text-sm text-muted-foreground flex items-start gap-2">
                <Info className="h-4 w-4 shrink-0 mt-0.5" />
                {ts(
                  'cloud.report in the server configuration sets this. Change it there and restart Dagu.'
                )}
              </p>
            )}
            {error && (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              {!monitoring.configured &&
                (reporting ? (
                  <>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={turningOff}
                      onClick={onChoose}
                    >
                      {ts('Change')}
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={turningOff}
                      onClick={() => void turnOff()}
                    >
                      {ts(turningOff ? 'Turning off…' : 'Turn off')}
                    </Button>
                  </>
                ) : (
                  <Button size="sm" onClick={onChoose}>
                    {ts('Turn on')}
                  </Button>
                ))}
              {monitoring.lastReport && (
                <Button
                  size="sm"
                  variant="link"
                  onClick={() => setShowReport(true)}
                >
                  {ts('View last report')}
                </Button>
              )}
            </div>
          </>
        )
      )}
      <Dialog open={showReport} onOpenChange={setShowReport}>
        <DialogContent className="max-h-[90dvh] grid-rows-[auto_minmax(0,1fr)] sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{ts('Last report')}</DialogTitle>
            <DialogDescription>
              {ts(
                'The last report this server sent to Dagu Console. The heartbeat secret is hidden.'
              )}
            </DialogDescription>
          </DialogHeader>
          <pre className="min-h-0 overflow-auto rounded-md bg-muted p-3 font-mono text-xs">
            {formatReport(monitoring?.lastReport ?? '')}
          </pre>
        </DialogContent>
      </Dialog>
    </section>
  );
}
