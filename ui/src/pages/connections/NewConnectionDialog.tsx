// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ConnectionAccess,
  ConnectionProviderName,
  ConnectionRedirectMode,
  CreateOAuthAppRequestProvider,
  OAuthAppKind,
  OAuthAppStatus,
  OAuthClientType,
  type components,
} from '@/api/v1/schema';
import { InlineLoadError } from '@/components/PageStates';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { ExternalLink, Loader2 } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ScopeList } from './ConnectDialog';
import {
  type ConnectionPageState,
  CopyableValue,
  GLOBAL_WORKSPACE,
  LOCAL_NODE,
  PROVIDER_LABELS,
  connectionPath,
  signInRedirect,
} from './shared';

type Preset = components['schemas']['ConnectionPreset'];
type Provider = components['schemas']['ConnectionProvider'];
type ProviderList = components['schemas']['ConnectionProviderListResponse'];
type OAuthApp = components['schemas']['OAuthApp'];

type Step = 'gallery' | 'services' | 'agents' | 'app';

// The rule of Spec 107, Names.
const NAME_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}$/;

const QUERY = { params: { query: { remoteNode: LOCAL_NODE } } };

// Google's product names, which stay as they are in every locale.
const SERVICE_LABELS: Record<string, string> = {
  gmail: 'Gmail',
  calendar: 'Calendar',
  drive: 'Drive',
  docs: 'Docs',
  sheets: 'Sheets',
  slides: 'Slides',
  chat: 'Chat',
  people: 'People',
  search: 'Search',
};

// M1 connects Google only; C20, C25, and C34 enable the other tiles.
const GALLERY: { provider: ConnectionProviderName; enabled: boolean }[] = [
  { provider: ConnectionProviderName.google, enabled: true },
  { provider: ConnectionProviderName.microsoft, enabled: false },
  { provider: ConnectionProviderName.mcp, enabled: false },
  { provider: ConnectionProviderName.oauth2, enabled: false },
];

/** The scopes the chosen services ask for, with the provider's own. */
function requestedScopes(
  provider: Provider,
  chosen: Record<string, ConnectionAccess>
): string[] {
  const scopes = new Set(provider.userScopes);
  for (const preset of provider.services) {
    const access = chosen[preset.name];
    if (!access) continue;
    preset.readScopes.forEach((scope) => scopes.add(scope));
    if (access === ConnectionAccess.write) {
      preset.writeScopes.forEach((scope) => scopes.add(scope));
    }
  }
  return [...scopes];
}

/** One page that enables the APIs of every chosen service. */
function enableApisUrl(presets: Preset[]): string | undefined {
  const apis = [...new Set(presets.flatMap((preset) => preset.apis))];
  const link = presets.find((preset) => preset.enableApisUrl)?.enableApisUrl;
  if (!link || apis.length === 0) return undefined;
  const url = new URL(link);
  return `${url.origin}${url.pathname}?apiid=${apis.join(',')}`;
}

/**
 * The client an admin registers for this server: a web client when the
 * server receives the callback itself, else a desktop client for paste.
 */
function clientToRegister(providers: ProviderList) {
  const callback = providers.redirects.find(
    (offer) => offer.mode === ConnectionRedirectMode.server_callback
  );
  return callback
    ? { clientType: OAuthClientType.web, redirectUri: callback.redirectUri }
    : { clientType: OAuthClientType.desktop, redirectUri: undefined };
}

/** Whether a connection of this server can sign in through the app. */
function fits(app: OAuthApp, agent: boolean, providers: ProviderList): boolean {
  return (
    app.provider === ConnectionProviderName.google &&
    app.kind === OAuthAppKind.byo &&
    app.status === OAuthAppStatus.ok &&
    app.agent === agent &&
    !!signInRedirect(app.clientType, providers.redirects)
  );
}

/** The client type a Google client JSON describes, or null if unreadable. */
function clientFileType(text: string): OAuthClientType | null {
  try {
    const parsed: unknown = JSON.parse(text);
    if (parsed && typeof parsed === 'object') {
      if ('web' in parsed) return OAuthClientType.web;
      if ('installed' in parsed) return OAuthClientType.desktop;
    }
  } catch {
    // The server reports what is wrong with the file.
  }
  return null;
}

/**
 * Adds a connection to a workspace: the provider gallery, then the Google
 * wizard's Services, Agents, and OAuth app steps. Creating the connection
 * opens its page, where the Consent and Done steps run in the connect
 * dialog.
 */
export function NewConnectionDialog({
  workspace,
  existing,
  open,
  onClose,
}: {
  workspace: string;
  /** The names of the workspace's connections, which a new one cannot take. */
  existing: string[];
  open: boolean;
  onClose: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>('gallery');
  const [name, setName] = useState('');
  const [chosen, setChosen] = useState<Record<string, ConnectionAccess>>({});
  const [agent, setAgent] = useState<boolean | null>(null);
  const [appId, setAppId] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const {
    data: providers,
    error: providersError,
    mutate: reloadProviders,
  } = useQuery('/connection-providers', whenEnabled(open, QUERY));
  const google = providers?.providers.find(
    (provider) => provider.name === ConnectionProviderName.google
  );
  const presets = (google?.services ?? []).filter(
    (preset) => chosen[preset.name]
  );
  const nameValid = NAME_PATTERN.test(name);
  const taken = existing.includes(name);

  function go(to: Step) {
    setError(null);
    setStep(to);
  }

  function close() {
    // A create in flight owns the dialog until it answers.
    if (creating) return;
    setStep('gallery');
    setName('');
    setChosen({});
    setAgent(null);
    setAppId(null);
    setError(null);
    onClose();
  }

  async function create() {
    if (!appId) return;
    setCreating(true);
    setError(null);
    try {
      const { error: apiError } = await client.POST('/connections', {
        params: { query: { workspace, remoteNode: LOCAL_NODE } },
        body: {
          name,
          app: appId,
          services: presets.map((preset) => ({
            name: preset.name,
            access: chosen[preset.name]!,
          })),
        },
      });
      if (apiError) {
        throw new Error(
          apiError.message || ts('Could not create the connection.')
        );
      }
      const target = connectionPath(name, workspace);
      close();
      navigate(target, { state: { connect: true } as ConnectionPageState });
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : ts('Could not create the connection.')
      );
    } finally {
      setCreating(false);
    }
  }

  const next: Record<Step, { ready: boolean; to?: Step }> = {
    gallery: { ready: false },
    services: {
      ready: nameValid && !taken && presets.length > 0,
      to: 'agents',
    },
    agents: { ready: agent !== null, to: 'app' },
    app: { ready: !!appId },
  };
  const back: Partial<Record<Step, Step>> = {
    services: 'gallery',
    agents: 'services',
    app: 'agents',
  };

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && close()}>
      <DialogContent className="max-h-[90dvh] grid-rows-[auto_minmax(0,1fr)_auto] sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {step === 'gallery'
              ? ts('New connection')
              : ts('Connect Google Workspace')}
          </DialogTitle>
          <DialogDescription>
            {ts('The connection belongs to workspace {workspace}.', {
              workspace:
                workspace === GLOBAL_WORKSPACE ? ts('Global') : workspace,
            })}
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 space-y-3 overflow-y-auto text-sm">
          {error && (
            <div
              role="alert"
              className="rounded-md bg-destructive/10 p-2 text-destructive"
            >
              {error}
            </div>
          )}
          {!providers && providersError ? (
            <InlineLoadError
              error={providersError}
              onRetry={() => void reloadProviders()}
            />
          ) : (
            step !== 'gallery' &&
            !providers && (
              <p className="text-muted-foreground">{ts('Loading...')}</p>
            )
          )}
          {step === 'gallery' && <Gallery onPick={() => go('services')} />}
          {step === 'services' && google && (
            <ServicesStep
              provider={google}
              workspace={workspace}
              name={name}
              nameValid={nameValid}
              taken={taken}
              onName={setName}
              chosen={chosen}
              onChosen={setChosen}
            />
          )}
          {step === 'agents' && (
            <AgentsStep
              agent={agent}
              onAgent={(value) => {
                setAgent(value);
                setAppId(null);
              }}
            />
          )}
          {step === 'app' && providers && agent !== null && (
            <AppStep
              providers={providers}
              presets={presets}
              agent={agent}
              appId={appId}
              onApp={setAppId}
            />
          )}
        </div>

        <DialogFooter>
          {back[step] ? (
            <Button
              type="button"
              variant="ghost"
              disabled={creating}
              onClick={() => go(back[step]!)}
            >
              {ts('Back')}
            </Button>
          ) : (
            <Button type="button" variant="ghost" onClick={close}>
              {ts('Cancel')}
            </Button>
          )}
          {step === 'app' ? (
            <Button
              variant="primary"
              disabled={!next.app.ready || creating}
              onClick={() => void create()}
            >
              {creating && <Loader2 className="h-4 w-4 animate-spin" />}
              {ts('Create and sign in')}
            </Button>
          ) : step !== 'gallery' ? (
            <Button
              variant="primary"
              disabled={!next[step].ready}
              onClick={() => go(next[step].to!)}
            >
              {ts('Next')}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Gallery({ onPick }: { onPick: () => void }): React.ReactElement {
  const { ts } = useI18n();
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {GALLERY.map((tile) => (
        <button
          key={tile.provider}
          type="button"
          disabled={!tile.enabled}
          onClick={onPick}
          className="rounded-md border border-border bg-card px-3 py-2 text-left transition-colors enabled:hover:border-border-strong enabled:hover:bg-muted disabled:cursor-not-allowed disabled:opacity-60"
        >
          <span className="block font-medium">
            {PROVIDER_LABELS[tile.provider]}
          </span>
          <span className="block text-xs text-muted-foreground">
            {tile.enabled
              ? ts('Gmail, Calendar, Drive, Docs, and more.')
              : ts('Not available yet.')}
          </span>
        </button>
      ))}
    </div>
  );
}

function ServicesStep({
  provider,
  workspace,
  name,
  nameValid,
  taken,
  onName,
  chosen,
  onChosen,
}: {
  provider: Provider;
  workspace: string;
  name: string;
  nameValid: boolean;
  taken: boolean;
  onName: (name: string) => void;
  chosen: Record<string, ConnectionAccess>;
  onChosen: (chosen: Record<string, ConnectionAccess>) => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const scopes = requestedScopes(provider, chosen);
  const invalid = name !== '' && !nameValid;

  function toggle(service: string, on: boolean) {
    const nextChosen = { ...chosen };
    if (on) nextChosen[service] = ConnectionAccess.read;
    else delete nextChosen[service];
    onChosen(nextChosen);
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label htmlFor="connection-name">{ts('Name')}</Label>
        <Input
          id="connection-name"
          value={name}
          onChange={(event) => onName(event.target.value)}
          placeholder="work-google"
          autoComplete="off"
          className="h-8"
          aria-invalid={invalid || taken}
          aria-describedby={
            invalid || taken
              ? 'connection-name-error connection-name-hint'
              : 'connection-name-hint'
          }
        />
        {invalid && (
          <p id="connection-name-error" className="text-xs text-destructive">
            {ts(
              'Use only lowercase letters, digits, and hyphens, and start with a letter or digit.'
            )}
          </p>
        )}
        {taken && (
          <p id="connection-name-error" className="text-xs text-destructive">
            {ts('This workspace already has a connection named {name}.', {
              name,
            })}{' '}
            <Link
              to={connectionPath(name, workspace)}
              className="font-medium underline"
            >
              {ts('Open it')}
            </Link>
          </p>
        )}
        <p id="connection-name-hint" className="text-xs text-muted-foreground">
          {ts(
            'DAGs name the connection with this. Up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit.'
          )}
        </p>
      </div>

      <fieldset className="space-y-1.5">
        <legend className="mb-1 text-sm font-medium">{ts('Services')}</legend>
        {provider.services.map((preset) => {
          const label = SERVICE_LABELS[preset.name] ?? preset.name;
          const access = chosen[preset.name];
          return (
            <div
              key={preset.name}
              className="flex min-h-8 items-center gap-2 rounded-md border border-border px-2"
            >
              <Checkbox
                id={`service-${preset.name}`}
                checked={!!access}
                onCheckedChange={(checked) =>
                  toggle(preset.name, checked === true)
                }
              />
              <Label
                htmlFor={`service-${preset.name}`}
                className="min-w-0 flex-1"
              >
                {label}
              </Label>
              {preset.preview && (
                <Badge size="sm" variant="info">
                  {ts('Preview')}
                </Badge>
              )}
              {access && preset.writeScopes.length > 0 && (
                <Select
                  value={access}
                  onValueChange={(value) =>
                    onChosen({
                      ...chosen,
                      [preset.name]: value as ConnectionAccess,
                    })
                  }
                >
                  <SelectTrigger
                    className="h-7 w-[150px]"
                    aria-label={ts('Access for {service}', { service: label })}
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={ConnectionAccess.read}>
                      {ts('Read')}
                    </SelectItem>
                    <SelectItem value={ConnectionAccess.write}>
                      {ts('Read and write')}
                    </SelectItem>
                  </SelectContent>
                </Select>
              )}
            </div>
          );
        })}
      </fieldset>
      {provider.services.some(
        (preset) => preset.preview && chosen[preset.name]
      ) && (
        <p className="text-xs text-muted-foreground">
          {ts(
            'Preview services work only for accounts in Google’s Workspace Developer Preview program.'
          )}
        </p>
      )}
      {Object.keys(chosen).length > 0 && (
        <div className="space-y-1">
          <div className="text-xs font-medium text-muted-foreground">
            {ts('Scopes to ask for')}
          </div>
          <ScopeList scopes={scopes} />
        </div>
      )}
    </div>
  );
}

function AgentsStep({
  agent,
  onAgent,
}: {
  agent: boolean | null;
  onAgent: (agent: boolean) => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const choices = [
    {
      value: true,
      label: ts('AI agents'),
      description: ts(
        'Steps call Google’s MCP servers with mcp.call, and AI steps use their tools.'
      ),
    },
    {
      value: false,
      label: ts('Other automation'),
      description: ts('Steps call Google’s REST APIs with http.request.'),
    },
  ];
  return (
    <fieldset className="space-y-2">
      <legend className="mb-1 font-medium">
        {ts('Who will use this connection?')}
      </legend>
      {choices.map((choice) => (
        <label
          key={String(choice.value)}
          className={cn(
            'flex cursor-pointer items-start gap-2 rounded-md border border-border p-2',
            agent === choice.value && 'border-border-strong bg-muted'
          )}
        >
          <input
            type="radio"
            name="connection-agent"
            className="mt-1"
            checked={agent === choice.value}
            onChange={() => onAgent(choice.value)}
          />
          <span>
            <span className="block font-medium">{choice.label}</span>
            <span className="block text-xs text-muted-foreground">
              {choice.description}
            </span>
          </span>
        </label>
      ))}
      <p className="text-xs text-muted-foreground">
        {ts(
          'Google requires separate OAuth clients for AI agents and for other automation, so each connection uses one kind. For both, add two connections.'
        )}
      </p>
    </fieldset>
  );
}

function AppStep({
  providers,
  presets,
  agent,
  appId,
  onApp,
}: {
  providers: ProviderList;
  presets: Preset[];
  agent: boolean;
  appId: string | null;
  onApp: (id: string | null) => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const isAdmin = useIsAdmin();
  const { data, error, isLoading, mutate } = useQuery('/oauth-apps', QUERY);
  const apps = (data?.apps ?? []).filter((app) => fits(app, agent, providers));
  const selected = apps.find((app) => app.id === appId);
  const [registering, setRegistering] = useState(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  // Create follows the listed app: an app this step does not list is never
  // the one a connection is created on.
  const stale = !!data && !!appId && !selected;
  useEffect(() => {
    if (stale) onApp(null);
  }, [stale, onApp]);

  if (isLoading) {
    return <p className="text-muted-foreground">{ts('Loading...')}</p>;
  }
  if (error && !data) {
    return <InlineLoadError error={error} onRetry={() => void mutate()} />;
  }

  return (
    <div className="space-y-3">
      <fieldset className="space-y-1.5">
        <legend className="mb-1 font-medium">{ts('OAuth app')}</legend>
        {apps.length === 0 && (
          <p className="text-muted-foreground">
            {agent
              ? ts('No Google app for AI agents fits this server yet.')
              : ts('No Google app for other automation fits this server yet.')}
          </p>
        )}
        {apps.map((app) => (
          <label
            key={app.id}
            className={cn(
              'flex cursor-pointer items-center gap-2 rounded-md border border-border px-2 py-1.5',
              app.id === appId && 'border-border-strong bg-muted'
            )}
          >
            <input
              type="radio"
              name="connection-app"
              checked={app.id === appId}
              onChange={() => onApp(app.id)}
            />
            <span className="min-w-0 flex-1">
              <span className="block font-medium">{app.name}</span>
              <span className="block break-all text-xs text-muted-foreground">
                {app.clientId}
              </span>
            </span>
            <Badge size="sm">{app.clientType}</Badge>
          </label>
        ))}
      </fieldset>

      {selected && (
        <CopyableValue
          label={ts('Client ID, which a Workspace admin may have to trust')}
          value={selected.clientId}
        />
      )}

      {isAdmin ? (
        registering || apps.length === 0 ? (
          <RegisterApp
            providers={providers}
            presets={presets}
            agent={agent}
            onRegistered={(app) => {
              setRegistering(false);
              // Selected once listed, and only while this step is shown.
              void mutate().then(() => {
                if (mounted.current) onApp(app.id);
              });
            }}
            onCancel={apps.length > 0 ? () => setRegistering(false) : undefined}
          />
        ) : (
          <Button size="sm" onClick={() => setRegistering(true)}>
            {ts('Register another app')}
          </Button>
        )
      ) : (
        apps.length === 0 && <AskAdmin providers={providers} agent={agent} />
      )}

      <p className="rounded-md bg-warning/10 p-2 text-xs">
        {ts(
          'DAGs that run on this server, rather than on a worker, run as Dagu’s user, so their authors can read this connection’s tokens.'
        )}
      </p>
    </div>
  );
}

/** What a manager asks an admin to register (Spec 107, Permissions). */
function AskAdmin({
  providers,
  agent,
}: {
  providers: ProviderList;
  agent: boolean;
}): React.ReactElement {
  const { ts } = useI18n();
  const wanted = clientToRegister(providers);
  return (
    <div className="space-y-2 rounded-md border border-border p-2">
      <p className="font-medium">
        {ts('Ask an admin to register a Google OAuth app with:')}
      </p>
      <ul className="list-disc space-y-1 pl-5">
        <li>
          {wanted.clientType === OAuthClientType.web
            ? ts('Client type: Web application')
            : ts('Client type: Desktop app')}
        </li>
        <li>
          {agent
            ? ts('“Use this client for an AI-powered agent” ticked')
            : ts('“Use this client for an AI-powered agent” not ticked')}
        </li>
      </ul>
      {wanted.redirectUri && (
        <CopyableValue
          label={ts('Redirect URI to register in the client')}
          value={wanted.redirectUri}
        />
      )}
    </div>
  );
}

/**
 * The admin's checklist for a new Google client: enable the APIs, set up the
 * consent screen, create the client, and upload its JSON file.
 */
function RegisterApp({
  providers,
  presets,
  agent,
  onRegistered,
  onCancel,
}: {
  providers: ProviderList;
  presets: Preset[];
  agent: boolean;
  onRegistered: (app: OAuthApp) => void;
  /** Closes the checklist without registering, when an app already fits. */
  onCancel?: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const google = providers.providers.find(
    (provider) => provider.name === ConnectionProviderName.google
  );
  const links = google?.setupLinks;
  const wanted = clientToRegister(providers);
  const enableUrl = enableApisUrl(presets);
  const [appName, setAppName] = useState(
    agent ? 'Google (AI agents)' : 'Google'
  );
  const [file, setFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function upload() {
    if (!file) return;
    setError(null);
    const text = await file.text();
    const type = clientFileType(text);
    // The server stores a client that no redirect mode of this server can
    // use, so the browser refuses it before registering.
    if (type && !signInRedirect(type, providers.redirects)) {
      setError(
        type === OAuthClientType.web
          ? ts(
              'This server has no public URL, so it cannot sign in through a web client. Create a Desktop app client and upload its JSON instead.'
            )
          : ts('This server cannot sign in through a desktop client.')
      );
      return;
    }
    setUploading(true);
    const failed = ts('Could not register the app.');
    try {
      const { data, error: apiError } = await client.POST('/oauth-apps', {
        params: { query: { remoteNode: LOCAL_NODE } },
        body: {
          name: appName.trim(),
          provider: CreateOAuthAppRequestProvider.google,
          clientFile: text,
          agent,
        },
      });
      if (!mounted.current) return;
      if (apiError || !data) throw new Error(apiError?.message || failed);
      if (!fits(data, agent, providers)) {
        throw new Error(
          ts('The app was registered, but this server cannot sign in with it.')
        );
      }
      onRegistered(data);
    } catch (err) {
      if (!mounted.current) return;
      setError(err instanceof Error && err.message ? err.message : failed);
    } finally {
      if (mounted.current) setUploading(false);
    }
  }

  return (
    <ol className="list-decimal space-y-3 rounded-md border border-border p-2 pl-6">
      <li className="space-y-1">
        <p className="font-medium">{ts('Enable the APIs')}</p>
        {enableUrl ? (
          <ExternalButton href={enableUrl}>
            {ts('Enable APIs in Google Cloud')}
          </ExternalButton>
        ) : (
          <p className="text-muted-foreground">
            {ts('The chosen services need no API.')}
          </p>
        )}
      </li>
      <li className="space-y-1">
        <p className="font-medium">{ts('Set up the consent screen')}</p>
        {links?.consentScreen && (
          <ExternalButton href={links.consentScreen}>
            {ts('Open the consent screen')}
          </ExternalButton>
        )}
        <p className="rounded-md bg-warning/10 p-2 text-xs">
          {ts(
            'An app in Testing status accepts only its test users, and their sign-ins end after seven days. Publish the app to Production, or make it Internal in a Workspace organization, to avoid both.'
          )}
        </p>
        <p className="text-xs text-muted-foreground">
          {ts(
            'An app that Google has not verified shows “Google hasn’t verified this app” at sign-in. That is expected for your own app.'
          )}
        </p>
      </li>
      <li className="space-y-1">
        <p className="font-medium">{ts('Create the OAuth client')}</p>
        {links?.clients && (
          <ExternalButton href={links.clients}>
            {ts('Open Google Cloud clients')}
          </ExternalButton>
        )}
        <p>
          {wanted.clientType === OAuthClientType.web
            ? ts('Choose the application type Web application.')
            : ts(
                'Choose the application type Desktop app. This server has no public URL, so the sign-in ends on a page whose address you paste back.'
              )}
        </p>
        {wanted.redirectUri && (
          <CopyableValue
            label={ts('Redirect URI to register in the client')}
            value={wanted.redirectUri}
          />
        )}
        {agent && (
          <p>{ts('Tick “Use this client for an AI-powered agent”.')}</p>
        )}
      </li>
      <li className="space-y-1.5">
        <p className="font-medium">{ts('Upload the client JSON')}</p>
        {error && (
          <p
            role="alert"
            className="whitespace-normal break-words rounded-md bg-destructive/10 p-2 text-destructive"
          >
            {error}
          </p>
        )}
        <div className="space-y-1">
          <Label htmlFor="oauth-app-name">{ts('App name')}</Label>
          <Input
            id="oauth-app-name"
            value={appName}
            onChange={(event) => setAppName(event.target.value)}
            className="h-8"
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="oauth-client-file">{ts('Client JSON file')}</Label>
          <Input
            id="oauth-client-file"
            type="file"
            accept="application/json,.json"
            onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            className="h-9"
          />
        </div>
        <Button
          size="sm"
          disabled={!file || appName.trim() === '' || uploading}
          onClick={() => void upload()}
        >
          {uploading && <Loader2 className="h-4 w-4 animate-spin" />}
          {ts('Register app')}
        </Button>
        {onCancel && (
          <Button
            size="sm"
            variant="ghost"
            disabled={uploading}
            onClick={onCancel}
          >
            {ts('Cancel')}
          </Button>
        )}
      </li>
    </ol>
  );
}

function ExternalButton({
  href,
  children,
}: {
  href: string;
  children: React.ReactNode;
}): React.ReactElement {
  return (
    <Button asChild size="sm" variant="outline">
      <a href={href} target="_blank" rel="noopener noreferrer">
        <ExternalLink className="h-3.5 w-3.5" />
        {children}
      </a>
    </Button>
  );
}
