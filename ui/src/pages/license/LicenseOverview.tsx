// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useState, type FormEvent, type ReactNode } from 'react';
import { Check, Copy, Info } from 'lucide-react';
import { LicenseStatusBadge } from '@/components/LicenseStatusBadge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import RelativeTime from '@/components/ui/relative-time';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import { useClient } from '@/hooks/api';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { useLicenseState } from '@/hooks/useLicense';
import {
  isFreeLicense,
  licenseLink,
  licensePlanLabel,
  licenseStatus,
} from '@/lib/license';

const connectedViaLabels: Record<string, string> = {
  console: 'Connected via Dagu Console',
  key: 'Activated with a license key',
  env: 'Set by an environment variable',
  config: 'Set by license.key in the config file',
  file: 'Loaded from a license file',
};

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all">{children}</dd>
    </>
  );
}

function CopyValue({ value, label }: { value: string; label: string }) {
  const { copied, copy } = useCopyFeedback();
  return (
    <span className="inline-flex items-center gap-1">
      <code className="font-mono text-xs text-muted-foreground">{value}</code>
      <Button
        size="icon"
        variant="ghost"
        className="h-5 w-5"
        aria-label={label}
        onClick={() => void copy(value)}
      >
        {copied ? (
          <Check className="h-3.5 w-3.5" />
        ) : (
          <Copy className="h-3.5 w-3.5" />
        )}
      </Button>
    </span>
  );
}

// Dagu Console shows at most this many characters of a server's name.
const maxServerNameLength = 100;

/**
 * The name Dagu Console shows for this server. A server that checks in can
 * be renamed there from here; the name it reports stays visible.
 */
function ServerName({
  license,
  remoteNode,
  busy,
}: {
  license: LicenseStatus;
  remoteNode: string;
  busy: boolean;
}) {
  const { ts } = useI18n();
  const client = useClient();
  const { mutate } = useContext(LicenseContext)!;
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reported = license.reportedName;
  const renamed = Boolean(
    reported && license.serverName && license.serverName !== reported
  );

  async function save(event: FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError(null);
    try {
      const { data, error } = await client.PUT('/license/server-name', {
        params: { query: { remoteNode } },
        body: { name: name.trim() },
      });
      if (error) throw new Error(error.message);
      await mutate(data, { revalidate: false });
      setEditing(false);
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : ts('Dagu Console could not be reached. Try again later.')
      );
    } finally {
      setSaving(false);
    }
  }

  if (editing) {
    return (
      <form onSubmit={save} className="space-y-1">
        <div className="flex flex-wrap gap-2">
          <Input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={maxServerNameLength}
            placeholder={reported}
            className="text-sm h-8 min-w-0 flex-1 basis-48"
            aria-label={ts('Server name')}
            disabled={saving}
          />
          <div className="flex gap-2">
            <Button
              type="submit"
              size="sm"
              variant="primary"
              className="h-8"
              disabled={saving || busy}
            >
              {ts(saving ? 'Saving...' : 'Save')}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-8"
              disabled={saving}
              onClick={() => {
                setEditing(false);
                setError(null);
              }}
            >
              {ts('Cancel')}
            </Button>
          </div>
        </div>
        <p className="text-xs text-muted-foreground break-normal">
          {ts('Leave empty to use the name the server reports.')}
        </p>
        {error && (
          <p role="alert" className="text-sm text-destructive break-normal">
            {error}
          </p>
        )}
      </form>
    );
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-x-2">
      <span>{license.serverName || remoteNode}</span>
      {license.serverId && (
        <Button
          size="sm"
          variant="link"
          className="h-auto p-0"
          disabled={busy}
          onClick={() => {
            setName(renamed ? (license.serverName ?? '') : '');
            setEditing(true);
          }}
        >
          {ts('Rename')}
        </Button>
      )}
      {renamed && (
        <span className="basis-full text-xs text-muted-foreground">
          {ts('Reports as {name}', { name: reported ?? '' })}
        </span>
      )}
    </span>
  );
}

function ViewPlans() {
  const { ts } = useI18n();
  return (
    <Button asChild size="sm" variant="link" className="h-auto p-0">
      <a
        href={licenseLink('plans', 'plan-page')}
        target="_blank"
        rel="noopener noreferrer"
      >
        {ts('View plans')}
      </a>
    </Button>
  );
}

/**
 * Lists the license of the selected server and how it reaches Dagu Console:
 * license facts on the left, the server's identity on the right, so an
 * administrator can match it to the console's server list.
 */
export function LicenseOverview({
  license,
  remoteNode,
  busy,
}: {
  license: LicenseStatus;
  remoteNode: string;
  busy: boolean;
}) {
  const { ts } = useI18n();
  const client = useClient();
  const { mutate } = useContext(LicenseContext)!;
  const { now } = useLicenseState();
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState<string | null>(null);

  const via =
    license.connectedVia ?? (license.source === 'env' ? 'env' : undefined);
  const viaLabel = via
    ? connectedViaLabels[via]
    : license.community
      ? 'Not connected'
      : undefined;
  // A callout offers plans when the license has a problem.
  const tone = licenseStatus(license, now).tone;
  const offerPlans =
    (license.community || license.plan === 'trial') &&
    (tone === 'ok' || tone === 'neutral');
  // A free license has no term to renew, so its expiry is left out.
  const expiry =
    !isFreeLicense(license) && license.expiry && dayjs(license.expiry).isValid()
      ? dayjs(license.expiry).format('YYYY-MM-DD')
      : undefined;

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

  // A fixed label width lines values up across both columns and with the
  // Monitoring section, also when the columns stack.
  const grid =
    'grid grid-cols-[7.5rem_minmax(0,1fr)] content-start gap-x-4 gap-y-1.5 text-sm';
  return (
    <section
      className="card-obsidian p-4 space-y-3"
      aria-labelledby="license-overview"
    >
      <h2 id="license-overview" className="text-sm font-medium">
        {ts('Overview')}
      </h2>
      <div className="grid gap-x-8 gap-y-1.5 md:grid-cols-2">
        <dl className={grid}>
          <Row label={ts('Status')}>
            <span className="inline-flex flex-wrap items-center gap-2">
              <LicenseStatusBadge plan={false} />
              {license.community && offerPlans && <ViewPlans />}
            </span>
          </Row>
          {!license.community && (
            <Row label={ts('Plan')}>
              <span className="inline-flex flex-wrap items-center gap-2">
                {licensePlanLabel(license, ts)}
                {!license.community && offerPlans && <ViewPlans />}
              </span>
            </Row>
          )}
          {expiry && (
            <Row label={ts(license.valid ? 'Expires' : 'Expired')}>
              {expiry}
            </Row>
          )}
          {license.workspace && (
            <Row label={ts('Workspace')}>{license.workspace}</Row>
          )}
        </dl>
        <dl className={grid}>
          <Row label={ts('Server')}>
            <ServerName license={license} remoteNode={remoteNode} busy={busy} />
          </Row>
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
              <CopyValue
                value={license.serverId}
                label={ts('Copy server ID')}
              />
            </Row>
          )}
          {license.licenseId && (
            <Row label={ts('License ID')}>
              <CopyValue
                value={license.licenseId}
                label={ts('Copy license ID')}
              />
            </Row>
          )}
        </dl>
      </div>
      {checkError && (
        <p role="alert" className="text-sm text-destructive">
          {checkError}
        </p>
      )}
      {license.community && via === 'env' && (
        <p className="text-sm text-muted-foreground flex items-start gap-2">
          <Info className="h-4 w-4 shrink-0 mt-0.5" />
          {ts(
            'This server’s license is set by DAGU_LICENSE or DAGU_LICENSE_KEY. Fix or remove it to connect from Dagu Console.'
          )}
        </p>
      )}
    </section>
  );
}
