// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useId, useState, type ReactNode } from 'react';
import { AlertTriangle, ExternalLink, Info, Loader2 } from 'lucide-react';
import { LicenseRemoteAccessLevel } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { CopyButton } from '@/components/ui/copy-button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import RelativeTime from '@/components/ui/relative-time';
import { Switch } from '@/components/ui/switch';
import { useQuery } from '@/hooks/api';
import type { LicenseRemoteAccess } from '@/hooks/useLicenseRemoteAccess';
import { useI18n } from '@/i18n/I18nProvider';
import { remoteAccessLevelLabels } from '@/lib/license';
import { cn } from '@/lib/utils';

/** A level at which calls from Dagu Cloud reach this server. */
type OnLevel = Exclude<LicenseRemoteAccessLevel, LicenseRemoteAccessLevel.off>;

/** What each level lets a call do, as the configuration reference words it. */
const choices: { level: OnLevel; allows: string }[] = [
  {
    level: LicenseRemoteAccessLevel.viewer,
    allows: 'Read DAGs, runs, and logs.',
  },
  {
    level: LicenseRemoteAccessLevel.operator,
    allows:
      'Also start, enqueue, retry, and stop runs of existing DAGs, with any parameters those DAGs take.',
  },
  {
    level: LicenseRemoteAccessLevel.developer,
    allows:
      'Also change DAG definitions and run inline specs. DAG steps run commands, so this runs any command on this machine.',
  },
];

/** How many recent calls the section lists. */
const ACTIVITY_ROWS = 8;

/** Audit actions that end a call, as opposed to those that start it. */
const finishedCall = /\.(succeeded|failed|denied)$/;

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  );
}

/**
 * Shows how far AI apps connected to Dagu Cloud may go on this server, whether
 * their calls reach it, how to connect one, and what they did lately, and lets
 * an administrator change it unless the configuration fixes it.
 */
export function RemoteAccessSection({
  remoteAccess,
  remoteNode,
  consoleURL,
  showActivity,
  loadError,
  onRetry,
  onChange,
}: {
  remoteAccess?: LicenseRemoteAccess;
  remoteNode: string;
  /** The base URL of the Dagu Console whose MCP endpoint AI apps use. */
  consoleURL: string;
  /** Lists recent calls, which audit logs record. */
  showActivity: boolean;
  loadError: boolean;
  onRetry: () => void;
  onChange: (change: {
    level?: LicenseRemoteAccessLevel;
    approvals?: boolean;
  }) => Promise<void>;
}) {
  const { ts } = useI18n();
  const [choosing, setChoosing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const on =
    remoteAccess !== undefined &&
    remoteAccess.level !== LicenseRemoteAccessLevel.off;
  const mcpURL = `${consoleURL}/mcp`;

  async function change(next: {
    level?: LicenseRemoteAccessLevel;
    approvals?: boolean;
  }) {
    setSaving(true);
    setError(null);
    try {
      await onChange(next);
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : ts('Could not change remote access.')
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <section
      id="remote-access"
      className="card-obsidian p-4 space-y-3 scroll-mt-4"
      aria-labelledby="license-remote-access"
    >
      <div className="min-w-0 space-y-1">
        <h2 id="license-remote-access" className="text-sm font-medium">
          {ts('Remote access')}
        </h2>
        <p className="text-xs text-muted-foreground">
          {ts(
            'AI apps connected to Dagu Cloud, such as Claude and ChatGPT, can work on this server through the connection it opens to Dagu Cloud. No port is opened. Each user gets the lower of this role and the one Dagu Cloud grants them.'
          )}
        </p>
      </div>
      {loadError && !remoteAccess ? (
        <div
          role="status"
          className="flex flex-wrap items-center gap-2 text-sm"
        >
          <span>{ts('Remote access status unavailable')}</span>
          <Button size="sm" variant="outline" onClick={onRetry}>
            {ts('Retry')}
          </Button>
        </div>
      ) : (
        remoteAccess && (
          <>
            <dl className="grid grid-cols-[7.5rem_minmax(0,1fr)] items-center gap-x-4 gap-y-1.5 text-sm">
              <Row label={ts('Access')}>
                <span className="flex flex-wrap items-center justify-between gap-2">
                  {ts(remoteAccessLevelLabels[remoteAccess.level])}
                  {remoteAccess.levelConfigured ? (
                    <span className="text-xs text-muted-foreground">
                      {ts('Set in configuration')}
                    </span>
                  ) : on ? (
                    <span className="flex flex-wrap items-center gap-2">
                      <Button
                        size="xs"
                        variant="outline"
                        disabled={saving}
                        onClick={() => setChoosing(true)}
                      >
                        {ts('Change')}
                      </Button>
                      <Button
                        size="xs"
                        variant="outline"
                        disabled={saving}
                        onClick={() =>
                          void change({ level: LicenseRemoteAccessLevel.off })
                        }
                      >
                        {ts('Turn off')}
                      </Button>
                    </span>
                  ) : (
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={saving}
                      onClick={() => setChoosing(true)}
                    >
                      {ts('Turn on')}
                    </Button>
                  )}
                </span>
              </Row>
              <Row label={ts('Approvals')}>
                <span className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-muted-foreground">
                    {ts(
                      'Approve, reject, or push back steps waiting for a person'
                    )}
                  </span>
                  {remoteAccess.approvalsConfigured ? (
                    <span className="text-xs text-muted-foreground">
                      {ts(remoteAccess.approvals ? 'On' : 'Off')} ·{' '}
                      {ts('Set in configuration')}
                    </span>
                  ) : (
                    <Switch
                      checked={remoteAccess.approvals}
                      disabled={saving}
                      aria-label={ts('Approvals')}
                      onCheckedChange={(approvals) =>
                        void change({ approvals })
                      }
                    />
                  )}
                </span>
              </Row>
              {on && (
                <Row label={ts('Connection')}>
                  <ConnectionState remoteAccess={remoteAccess} />
                </Row>
              )}
              {on && (
                <Row label={ts('MCP URL')}>
                  <span className="flex min-w-0 items-center gap-1">
                    <code className="min-w-0 truncate font-mono text-xs">
                      {mcpURL}
                    </code>
                    <CopyButton
                      size="sm"
                      text={mcpURL}
                      label={ts('Copy MCP URL')}
                      copiedLabel={ts('MCP URL copied')}
                    />
                  </span>
                </Row>
              )}
            </dl>
            {on && (
              <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                {ts(
                  'Add the MCP URL to Claude, ChatGPT, or another AI app as a connector, then sign in to Dagu Cloud. Workspace owners choose what connected apps may do.'
                )}
                <a
                  href={`${consoleURL}/ai-apps`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-foreground underline underline-offset-2"
                >
                  <ExternalLink className="h-3 w-3" aria-hidden="true" />
                  {ts('Open AI apps')}
                </a>
              </p>
            )}
            {(remoteAccess.levelConfigured ||
              remoteAccess.approvalsConfigured) && (
              <p className="text-sm text-muted-foreground flex items-start gap-2">
                <Info className="h-4 w-4 shrink-0 mt-0.5" />
                {ts(
                  'cloud.remote_access and cloud.remote_approvals in the server configuration set these. Change them there and restart Dagu.'
                )}
              </p>
            )}
            {error && (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            )}
            {showActivity && on && <AgentActivity remoteNode={remoteNode} />}
          </>
        )
      )}
      <RemoteAccessDialog
        open={choosing}
        level={remoteAccess?.level}
        onSave={async (level) => {
          await onChange({ level });
          setChoosing(false);
        }}
        onCancel={() => setChoosing(false)}
      />
    </section>
  );
}

function ConnectionState({
  remoteAccess,
}: {
  remoteAccess: LicenseRemoteAccess;
}) {
  const { ts } = useI18n();
  if (remoteAccess.connected) {
    return (
      <span className="inline-flex items-center gap-1.5">
        <span
          className="h-2 w-2 shrink-0 rounded-full bg-success"
          aria-hidden="true"
        />
        {ts('Connected')}
      </span>
    );
  }
  if (remoteAccess.error) {
    return (
      <span className="inline-flex items-start gap-1.5 text-warning">
        <AlertTriangle
          className="h-3.5 w-3.5 shrink-0 mt-0.5"
          aria-hidden="true"
        />
        {remoteAccess.error}
      </span>
    );
  }
  return <span className="text-muted-foreground">{ts('Connecting…')}</span>;
}

/** Lists the latest calls from Dagu Cloud that audit logs recorded. */
function AgentActivity({ remoteNode }: { remoteNode: string }) {
  const { ts } = useI18n();
  const { data } = useQuery(
    '/audit',
    {
      params: {
        query: { remoteNode, credentialType: 'cloud', limit: 50 },
      },
    },
    { refreshInterval: 30_000 }
  );
  const calls = (data?.entries ?? [])
    .filter((entry) => finishedCall.test(entry.action))
    .slice(0, ACTIVITY_ROWS);

  return (
    <div className="space-y-1.5">
      <h3 className="text-xs font-medium text-muted-foreground">
        {ts('Agent activity')}
      </h3>
      {calls.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {ts('No calls from Dagu Cloud yet.')}
        </p>
      ) : (
        <ul className="divide-y divide-border text-sm">
          {calls.map((entry) => {
            const details = parseDetails(entry.details);
            const failed = !entry.action.endsWith('.succeeded');
            return (
              <li
                key={entry.id}
                className="flex items-center gap-3 py-1.5 min-w-0"
              >
                <span className="w-28 shrink-0 whitespace-nowrap text-xs text-muted-foreground">
                  <RelativeTime timestamp={entry.timestamp} />
                </span>
                <span className="min-w-0 flex-1 truncate">
                  <span className="font-mono text-xs">
                    {[entry.mcpTool, details.mcp_action, details.resource_id]
                      .filter(Boolean)
                      .join(' ')}
                  </span>
                </span>
                <span className="hidden sm:block max-w-[12rem] truncate text-xs text-muted-foreground">
                  {entry.username}
                </span>
                {failed && (
                  <span className="shrink-0 text-xs text-warning">
                    {ts(entry.action.endsWith('.denied') ? 'Denied' : 'Failed')}
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

/** Reads the fields of an audit entry's details that name what a call did. */
function parseDetails(details?: string): {
  mcp_action?: string;
  resource_id?: string;
} {
  try {
    const parsed = JSON.parse(details ?? '{}');
    return typeof parsed === 'object' && parsed !== null ? parsed : {};
  } catch {
    return {};
  }
}

/**
 * Asks an administrator how far calls from Dagu Cloud may go, listing what
 * each role allows before any call reaches the server.
 */
function RemoteAccessDialog({
  open,
  level,
  onSave,
  onCancel,
}: {
  open: boolean;
  level?: LicenseRemoteAccessLevel;
  onSave: (level: OnLevel) => Promise<void>;
  onCancel: () => void;
}) {
  const { ts } = useI18n();
  const name = useId();
  const [selected, setSelected] = useState<OnLevel>(
    LicenseRemoteAccessLevel.viewer
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const on = level !== undefined && level !== LicenseRemoteAccessLevel.off;

  useEffect(() => {
    if (!open) return;
    setSelected(on ? (level as OnLevel) : LicenseRemoteAccessLevel.viewer);
    setError(null);
  }, [open, level, on]);

  async function save() {
    setSaving(true);
    setError(null);
    try {
      await onSave(selected);
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : ts('Could not change remote access.')
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !saving) onCancel();
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{ts('Choose remote access')}</DialogTitle>
          <DialogDescription>
            {ts(
              'The highest role a Dagu Cloud user gets on this server. Dagu Cloud may grant less, never more.'
            )}
          </DialogDescription>
        </DialogHeader>
        <fieldset className="space-y-2" disabled={saving}>
          <legend className="sr-only">{ts('Remote access')}</legend>
          {choices.map((choice) => {
            const id = `${name}-${choice.level}`;
            return (
              <label
                key={choice.level}
                htmlFor={id}
                className={cn(
                  'flex cursor-pointer items-start gap-3 rounded-md border p-3',
                  selected === choice.level
                    ? 'border-primary bg-primary/5'
                    : 'border-border'
                )}
              >
                <input
                  id={id}
                  type="radio"
                  name={name}
                  value={choice.level}
                  checked={selected === choice.level}
                  onChange={() => setSelected(choice.level)}
                  aria-labelledby={`${id}-title`}
                  aria-describedby={`${id}-allows`}
                  className="mt-1"
                />
                <span className="space-y-1">
                  <span
                    id={`${id}-title`}
                    className="block text-sm font-medium"
                  >
                    {ts(remoteAccessLevelLabels[choice.level])}
                  </span>
                  <span
                    id={`${id}-allows`}
                    className="block text-sm text-muted-foreground"
                  >
                    {ts(choice.allows)}
                  </span>
                </span>
              </label>
            );
          })}
        </fieldset>
        {selected === LicenseRemoteAccessLevel.developer && (
          <p
            role="alert"
            className="flex items-start gap-2 text-sm text-warning"
          >
            <AlertTriangle
              className="h-4 w-4 shrink-0 mt-0.5"
              aria-hidden="true"
            />
            {ts(
              'Developer lets the Dagu Cloud users this reaches run any command on this machine, as the user Dagu runs as. Choose it only where you would give them a shell here.'
            )}
          </p>
        )}
        <p className="text-sm text-muted-foreground">
          {ts(
            'Calls never reach the terminal, users, API keys, the license, or these settings. Each carries a short-lived assertion that Dagu Cloud signs for that call alone. Turning remote access off closes the connection and ends the calls in flight.'
          )}
        </p>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <DialogFooter className="gap-2">
          <Button variant="ghost" disabled={saving} onClick={onCancel}>
            {ts('Cancel')}
          </Button>
          <Button
            variant="primary"
            disabled={saving}
            onClick={() => void save()}
          >
            {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {ts(on ? 'Save' : 'Turn on')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
