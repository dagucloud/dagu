// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useState } from 'react';
import { ExternalLink, Info, Loader2 } from 'lucide-react';
import { LicenseConnectStatusState } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import type { useLicenseConnect } from './useLicenseConnect';

function Countdown({ expiresAt }: { expiresAt?: string }) {
  const { ts } = useI18n();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  if (!expiresAt || !dayjs(expiresAt).isValid()) return null;
  const seconds = Math.max(0, dayjs(expiresAt).diff(dayjs(now), 'second'));
  const time = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
  return (
    <span className="text-xs text-muted-foreground">
      {ts('Expires in {time}', { time })}
    </span>
  );
}

/**
 * Offers to license a community server by approving it in Dagu Console, and
 * shows the request while it waits for approval.
 */
export function ConnectSection({
  connect,
  managedByEnv,
  disabled,
}: {
  connect: ReturnType<typeof useLicenseConnect>;
  managedByEnv: boolean;
  disabled: boolean;
}) {
  const { ts } = useI18n();
  const { status, starting, error, popupBlocked, start, cancel } = connect;
  const pending = status?.state === LicenseConnectStatusState.pending;
  const failure =
    error ||
    (status?.state === LicenseConnectStatusState.failed ||
    status?.state === LicenseConnectStatusState.expired
      ? status.error
      : undefined);

  return (
    <section
      className="card-obsidian p-4 space-y-3"
      aria-labelledby="license-connect"
    >
      <h2 id="license-connect" className="text-sm font-medium">
        {ts(pending ? 'Waiting for approval' : 'Connect to Dagu Console')}
      </h2>
      {managedByEnv ? (
        <p className="text-sm text-muted-foreground flex items-start gap-2">
          <Info className="h-4 w-4 shrink-0 mt-0.5" />
          {ts(
            'This server’s license is set by DAGU_LICENSE or DAGU_LICENSE_KEY. Fix or remove it to connect from Dagu Console.'
          )}
        </p>
      ) : pending ? (
        <>
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
            <Button asChild size="sm">
              <a
                href={status.connectUrl}
                target="_blank"
                rel="noopener noreferrer"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {ts('Open Dagu Console')}
              </a>
            </Button>
            <Button size="sm" variant="outline" onClick={() => void cancel()}>
              {ts('Cancel')}
            </Button>
            <Countdown expiresAt={status.expiresAt} />
          </div>
        </>
      ) : (
        <>
          <p className="text-sm text-muted-foreground">
            {ts(
              'Sign in to Dagu Console and approve this server. There is no key to copy.'
            )}
          </p>
          {failure && (
            <p role="alert" className="text-sm text-destructive">
              {failure}
            </p>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              disabled={disabled || starting}
              onClick={() => void start()}
            >
              {starting ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <ExternalLink className="h-3.5 w-3.5" />
              )}
              {ts(failure ? 'Try again' : 'Connect to Dagu Console')}
            </Button>
            <Button asChild size="sm" variant="link">
              <a href="#activate">{ts('Use a license key instead')}</a>
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
