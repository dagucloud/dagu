// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ConnectionAuthorizationPhase,
  ConnectionRedirectMode,
  ConnectionStatus,
  type components,
} from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
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
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { CheckCircle2, ExternalLink, Loader2, XCircle } from 'lucide-react';
import React, { useState } from 'react';
import { type Connection, CopyableValue, LOCAL_NODE } from './shared';
import { type ConnectState, useConnectSession } from './useConnectSession';

type ConnectionProbe = components['schemas']['ConnectionProbe'];

const QUERY = { params: { query: { remoteNode: LOCAL_NODE } } };

/**
 * The redirect the server picks when the browser names no mode: the first
 * offer for the app's client type, skipping loopback, which needs the CLI's
 * listener.
 */
function usePredictedRedirect(connection: Connection, open: boolean) {
  const { data: providers } = useQuery(
    '/connection-providers',
    whenEnabled(open, QUERY)
  );
  const { data: app } = useQuery(
    '/oauth-apps/{appId}',
    whenEnabled(open, {
      params: {
        path: { appId: connection.app },
        query: { remoteNode: LOCAL_NODE },
      },
    })
  );
  if (!providers || !app) return { app, redirect: undefined, known: false };
  const redirect = providers.redirects.find(
    (offer) =>
      offer.clientType === app.clientType &&
      offer.mode !== ConnectionRedirectMode.loopback
  );
  return { app, redirect, known: true };
}

/**
 * Connects or reconnects a connection: the consent popup, the paste field
 * for a desktop client, and the account to confirm. The redirect URI to
 * register and the client ID an admin must trust show before consent, since
 * Google reports their errors on its own page.
 */
export function ConnectDialog({
  connection,
  open,
  onClose,
  onConnected,
}: {
  connection: Connection;
  open: boolean;
  onClose: () => void;
  onConnected: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const flow = useConnectSession(connection.name, connection.workspace);
  const predicted = usePredictedRedirect(connection, open);
  const [pasted, setPasted] = useState('');
  const phase = flow.state?.phase;
  const mode = flow.session?.mode ?? predicted.redirect?.mode;
  const redirectUri =
    flow.session?.redirectUri ?? predicted.redirect?.redirectUri;
  const clientId = flow.session?.clientId ?? predicted.app?.clientId;
  const isPaste = mode === ConnectionRedirectMode.paste;
  const unavailable = !flow.session && predicted.known && !predicted.redirect;
  const title =
    connection.status === ConnectionStatus.pending
      ? ts('Connect {name}', { name: connection.name })
      : ts('Reconnect {name}', { name: connection.name });

  function close() {
    flow.reset();
    setPasted('');
    onClose();
  }

  async function confirm() {
    if (await flow.confirm()) onConnected();
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && close()}>
      <DialogContent className="max-h-[90dvh] grid-rows-[auto_minmax(0,1fr)_auto] sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {ts(
              'The live tokens stay in use until you confirm the account that signed in.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 space-y-3 overflow-y-auto text-sm">
          {flow.error && (
            <div
              role="alert"
              className="rounded-md bg-destructive/10 p-2 text-destructive"
            >
              {flow.error}
            </div>
          )}

          {phase === ConnectionAuthorizationPhase.confirm && flow.state ? (
            <ConfirmAccount state={flow.state} connection={connection} />
          ) : phase === ConnectionAuthorizationPhase.connected ? (
            <p className="flex items-center gap-2 text-success">
              <CheckCircle2 className="h-4 w-4" />
              {ts('{name} is connected.', { name: connection.name })}
            </p>
          ) : (
            <>
              {phase === ConnectionAuthorizationPhase.failed && (
                <div
                  role="alert"
                  className="rounded-md bg-destructive/10 p-2 text-destructive"
                >
                  {ts('Signing in failed: {reason}', {
                    reason: flow.state?.reason ?? '',
                  })}
                </div>
              )}
              {unavailable && (
                <p className="rounded-md bg-warning/10 p-2">
                  {ts(
                    "This server has no public URL, so a web client can't receive the sign-in. Set server.public_url, or use an app with a desktop client."
                  )}
                </p>
              )}
              {isPaste && phase !== ConnectionAuthorizationPhase.waiting && (
                <p className="text-muted-foreground">
                  {ts(
                    'After you consent, the popup ends on a page that fails to load. Copy that page’s address from the popup and paste it here.'
                  )}
                </p>
              )}
              <div className="space-y-2">
                {redirectUri && (
                  <CopyableValue
                    label={ts('Redirect URI to register in the client')}
                    value={redirectUri}
                  />
                )}
                {clientId && (
                  <CopyableValue
                    label={ts(
                      'Client ID, which a Workspace admin may have to trust'
                    )}
                    value={clientId}
                  />
                )}
              </div>
              {phase === ConnectionAuthorizationPhase.waiting &&
                flow.session && (
                  <WaitingForConsent
                    authorizationUrl={flow.session.authorizationUrl}
                    expiresAt={flow.session.expiresAt}
                    popupBlocked={flow.popupBlocked}
                    shareable={mode === ConnectionRedirectMode.server_callback}
                  />
                )}
              {isPaste && phase === ConnectionAuthorizationPhase.waiting && (
                <div className="space-y-1.5">
                  <Label htmlFor="connection-pasted-url">
                    {ts('Address the popup ended on')}
                  </Label>
                  <Input
                    id="connection-pasted-url"
                    value={pasted}
                    onChange={(event) => setPasted(event.target.value)}
                    placeholder="http://127.0.0.1:1/oauth/callback?code=…"
                    autoComplete="off"
                    className="h-8"
                  />
                </div>
              )}
            </>
          )}
        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={close}>
            {phase === ConnectionAuthorizationPhase.connected
              ? ts('Close')
              : ts('Cancel')}
          </Button>
          {phase === ConnectionAuthorizationPhase.confirm ? (
            <Button
              variant="primary"
              disabled={flow.busy}
              onClick={() => void confirm()}
            >
              {flow.busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {ts('Confirm account')}
            </Button>
          ) : isPaste && phase === ConnectionAuthorizationPhase.waiting ? (
            <Button
              variant="primary"
              disabled={flow.busy || pasted.trim() === ''}
              onClick={() => void flow.complete(pasted.trim())}
            >
              {flow.busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {ts('Continue')}
            </Button>
          ) : phase !== ConnectionAuthorizationPhase.connected &&
            phase !== ConnectionAuthorizationPhase.waiting ? (
            <Button
              variant="primary"
              disabled={flow.busy || unavailable}
              onClick={() => {
                setPasted('');
                void flow.start();
              }}
            >
              {flow.busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {phase === ConnectionAuthorizationPhase.failed
                ? ts('Try again')
                : ts('Open sign-in')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function WaitingForConsent({
  authorizationUrl,
  expiresAt,
  popupBlocked,
  shareable,
}: {
  authorizationUrl: string;
  expiresAt: string;
  popupBlocked: boolean;
  shareable: boolean;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <div className="space-y-2 rounded-md border border-border p-2">
      <p className="flex items-center gap-2">
        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        {popupBlocked
          ? ts('The browser blocked the popup. Open the sign-in page instead.')
          : ts('Waiting for consent in the popup…')}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Button asChild size="sm" variant="outline">
          <a href={authorizationUrl} target="_blank" rel="noopener noreferrer">
            <ExternalLink className="h-3.5 w-3.5" />
            {ts('Open sign-in page')}
          </a>
        </Button>
        {shareable && (
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <CopyButton
              text={authorizationUrl}
              size="sm"
              label={ts('Copy sign-in link')}
            />
            {ts('Copy sign-in link')}
          </span>
        )}
      </div>
      <p className="text-xs text-muted-foreground">
        {shareable
          ? ts(
              'The account’s owner may sign in from their own browser with the copied link. The session ends at {time}.',
              { time: dayjs(expiresAt).format('HH:mm') }
            )
          : ts('The session ends at {time}.', {
              time: dayjs(expiresAt).format('HH:mm'),
            })}
      </p>
    </div>
  );
}

function ConfirmAccount({
  state,
  connection,
}: {
  state: ConnectState;
  connection: Connection;
}): React.ReactElement {
  const { ts } = useI18n();
  const otherAccount =
    !!connection.account &&
    !!state.account &&
    connection.account.toLowerCase() !== state.account.toLowerCase();
  return (
    <div className="space-y-3">
      <div>
        <div className="text-xs font-medium text-muted-foreground">
          {ts('Signed in as')}
        </div>
        <div className="font-medium">{state.account}</div>
      </div>
      {otherAccount && (
        <p className="rounded-md bg-warning/10 p-2">
          {ts(
            'This is not {account}, the account the connection holds now. Confirming replaces it.',
            { account: connection.account ?? '' }
          )}
        </p>
      )}
      {!!state.duplicates?.length && (
        <p className="rounded-md bg-warning/10 p-2">
          {ts(
            'Other connections in this workspace already hold this account through the same client: {names}.',
            { names: state.duplicates.join(', ') }
          )}
        </p>
      )}
      {state.refreshTokenExpiresAt && (
        <p className="rounded-md bg-warning/10 p-2">
          {ts('The sign-in ends {date}.', {
            date: dayjs(state.refreshTokenExpiresAt).format('MMM D, YYYY'),
          })}{' '}
          {state.refreshTokenFix}
        </p>
      )}
      {!!state.probes?.length && <ProbeResults probes={state.probes} />}
      {!!state.grantedScopes?.length && (
        <div className="space-y-1">
          <div className="text-xs font-medium text-muted-foreground">
            {ts('Granted scopes')}
          </div>
          <ScopeList scopes={state.grantedScopes} />
        </div>
      )}
    </div>
  );
}

/** Each service's check result. */
export function ProbeResults({
  probes,
}: {
  probes: ConnectionProbe[];
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <ul aria-label={ts('Service checks')} className="space-y-1">
      {probes.map((probe) => (
        <li key={probe.service} className="flex items-start gap-2">
          {probe.ok ? (
            <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-success" />
          ) : (
            <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
          )}
          <span className="min-w-0">
            <span className="font-medium">{probe.service}</span>
            {probe.ok ? (
              <span className="text-muted-foreground"> · {ts('OK')}</span>
            ) : (
              <span className="block whitespace-normal break-words text-muted-foreground">
                {probe.error}
              </span>
            )}
          </span>
        </li>
      ))}
    </ul>
  );
}

export function ScopeList({ scopes }: { scopes: string[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {scopes.map((scope) => (
        <Badge
          key={scope}
          variant="outline"
          className="h-auto min-h-5 max-w-full whitespace-normal break-all font-mono"
        >
          {scope}
        </Badge>
      ))}
    </div>
  );
}
