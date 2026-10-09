// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useState, type ReactNode } from 'react';
import { AlertTriangle, Check, Copy, ExternalLink, Info } from 'lucide-react';
import { Button } from '@/components/ui/button';
import RelativeTime from '@/components/ui/relative-time';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import { useClient } from '@/hooks/api';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { useI18n } from '@/i18n/I18nProvider';
import { licenseLink } from '@/lib/license';

const connectedViaLabels: Record<string, string> = {
  console: 'Connected via Dagu Console',
  key: 'Activated with a license key',
  env: 'Set by an environment variable',
  config: 'Set by license.key in the config file',
  file: 'Loaded from a license file',
};

/** Explains how to stop using a license that Dagu loads again on restart. */
const restartNotes: Record<string, string> = {
  config:
    'license.key in the config file activates this license again when Dagu restarts. Remove it to stop using the license.',
  file: 'The license file is loaded again when Dagu restarts. Remove it to stop using the license.',
};

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all">{children}</dd>
    </>
  );
}

/**
 * Shows which server this is in Dagu Console and how its license got here,
 * so an administrator can match it to the console's server list.
 */
export function ServerIdentity({
  license,
  remoteNode,
  busy,
  disconnecting,
  onDisconnect,
}: {
  license: LicenseStatus;
  remoteNode: string;
  busy: boolean;
  disconnecting: boolean;
  onDisconnect: () => void;
}) {
  const { ts } = useI18n();
  const client = useClient();
  const { mutate } = useContext(LicenseContext)!;
  const { copied, copy } = useCopyFeedback();
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState<string | null>(null);

  const via = license.connectedVia;
  const viaLabel = via ? connectedViaLabels[via] : undefined;
  const fromConsole = via === 'console' || via === 'key';
  const managedByEnv = via ? via === 'env' : license.source === 'env';
  const restartNote = via ? restartNotes[via] : undefined;
  // Servers older than the identity fields report none of them.
  const hasDetails = Boolean(
    license.serverName ||
      license.workspace ||
      viaLabel ||
      license.serverId ||
      license.licenseId
  );

  async function checkNow() {
    setChecking(true);
    setCheckError(null);
    try {
      const { data, error } = await client.POST('/license/refresh', {
        params: { query: { remoteNode } },
      });
      if (error) throw new Error(error.message);
      await mutate(data, { revalidate: false });
    } catch (err) {
      setCheckError(
        err instanceof Error && err.message
          ? err.message
          : ts('Dagu Console could not be reached. Try again later.')
      );
    } finally {
      setChecking(false);
    }
  }

  return (
    <section
      className="card-obsidian p-4 space-y-3"
      aria-labelledby="license-this-server"
    >
      <h2 id="license-this-server" className="text-sm font-medium">
        {ts('This server')}
      </h2>
      {hasDetails && (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-sm">
          {license.serverName && (
            <Row label={ts('Name')}>{license.serverName}</Row>
          )}
          {license.workspace && (
            <Row label={ts('Workspace')}>{license.workspace}</Row>
          )}
          {viaLabel && <Row label={ts('Connection')}>{ts(viaLabel)}</Row>}
          {license.serverId && (
            <Row label={ts('Last check-in')}>
              <span className="inline-flex flex-wrap items-center gap-2">
                <RelativeTime
                  timestamp={license.lastCheckIn}
                  fallback={ts('Not yet')}
                />
                <Button
                  size="sm"
                  variant="link"
                  className="h-auto p-0"
                  disabled={checking || busy}
                  onClick={() => void checkNow()}
                >
                  {ts(checking ? 'Checking…' : 'Check now')}
                </Button>
              </span>
            </Row>
          )}
          {license.serverId && (
            <Row label={ts('Server ID')}>
              <span className="inline-flex items-center gap-1">
                <code className="font-mono text-xs">{license.serverId}</code>
                <Button
                  size="icon"
                  variant="ghost"
                  className="h-6 w-6"
                  aria-label={ts('Copy server ID')}
                  onClick={() => void copy(license.serverId ?? '')}
                >
                  {copied ? (
                    <Check className="h-3.5 w-3.5" />
                  ) : (
                    <Copy className="h-3.5 w-3.5" />
                  )}
                </Button>
              </span>
            </Row>
          )}
          {license.licenseId && (
            <Row label={ts('License ID')}>
              <code className="font-mono text-xs">{license.licenseId}</code>
            </Row>
          )}
        </dl>
      )}
      {checkError && (
        <p role="alert" className="text-sm text-destructive">
          {checkError}
        </p>
      )}
      {managedByEnv ? (
        <p className="text-sm text-muted-foreground flex items-start gap-2">
          <Info className="h-4 w-4 shrink-0 mt-0.5" />
          {ts(
            'This license is configured via an environment variable (DAGU_LICENSE or DAGU_LICENSE_KEY). To deactivate, remove the environment variable and restart Dagu.'
          )}
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">
          {ts(
            fromConsole
              ? 'Disconnecting frees this server’s slot in Dagu Console and returns it to community mode.'
              : 'Remove the license from this machine and return to community mode.'
          )}
          {restartNote && <> {ts(restartNote)}</>}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button asChild size="sm" variant="outline">
          <a
            href={license.consoleUrl || licenseLink('manage', 'server-panel')}
            target="_blank"
            rel="noopener noreferrer"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            {ts('Manage in Dagu Console')}
          </a>
        </Button>
        {!managedByEnv && (
          <Button
            variant="destructive"
            size="sm"
            disabled={busy}
            onClick={onDisconnect}
          >
            <AlertTriangle className="h-3.5 w-3.5" />
            {fromConsole
              ? ts(disconnecting ? 'Disconnecting...' : 'Disconnect')
              : ts(disconnecting ? 'Deactivating...' : 'Deactivate License')}
          </Button>
        )}
      </div>
    </section>
  );
}
