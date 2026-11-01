// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  ArrowRight,
  ChevronRight,
  Bell,
  MoreHorizontal,
  Send,
  Building2,
  CheckCircle2,
  Globe2,
  Info,
  Loader2,
  Mail,
  Plus,
  Trash2,
} from 'lucide-react';
import {
  type ReactElement,
  useContext,
  useEffect,
  useId,
  useState,
} from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import Title from '@/components/ui/title';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from '@/components/ui/dropdown-menu';
import { MonitoringOffer } from '@/components/MonitoringOffer';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { cn } from '@/lib/utils';
import { WorkspaceKind, workspaceNameForSelection } from '@/lib/workspace';
import {
  NotificationChannelsSection,
  useCommandChannelLockReason,
} from '@/features/dags/components/dag-details/notifications/NotificationSections';
import {
  channelInput,
  DraftChannel,
  draftChannelFromAPI,
  EVENT_OPTIONS,
  providerIcon,
  providerLabel,
} from '@/features/dags/components/dag-details/notifications/notificationDrafts';
import {
  components,
  NotificationEventType,
  NotificationProviderType,
  NotificationSMTPOAuthProvider,
} from '@/api/v1/schema';
import { Link } from 'react-router-dom';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type NotificationWorkspaceSettings =
  components['schemas']['NotificationWorkspaceSettings'];
type NotificationRouteSet = components['schemas']['NotificationRouteSet'];
type NotificationRouteSetInput =
  components['schemas']['NotificationRouteSetInput'];

type SMTPDraft = {
  mode: 'password' | 'oauth';
  host: string;
  port: string;
  username: string;
  password: string;
  from: string;
  passwordConfigured: boolean;
  clearPassword: boolean;
  oauthProvider: NotificationSMTPOAuthProvider;
  tenantId: string;
  clientId: string;
  clientSecret: string;
  refreshToken: string;
  serviceAccountJson: string;
  clientSecretConfigured: boolean;
  refreshTokenConfigured: boolean;
  serviceAccountJsonConfigured: boolean;
};

const blankSMTPDraft: SMTPDraft = {
  mode: 'password',
  host: '',
  port: '',
  username: '',
  password: '',
  from: '',
  passwordConfigured: false,
  clearPassword: false,
  oauthProvider: NotificationSMTPOAuthProvider.microsoft,
  tenantId: '',
  clientId: '',
  clientSecret: '',
  refreshToken: '',
  serviceAccountJson: '',
  clientSecretConfigured: false,
  refreshTokenConfigured: false,
  serviceAccountJsonConfigured: false,
};

const smtpOAuthDestinations: Record<
  NotificationSMTPOAuthProvider,
  { host: string; port: string }
> = {
  [NotificationSMTPOAuthProvider.microsoft]: {
    host: 'smtp.office365.com',
    port: '587',
  },
  [NotificationSMTPOAuthProvider.google_service_account]: {
    host: 'smtp.gmail.com',
    port: '587',
  },
  [NotificationSMTPOAuthProvider.google_refresh]: {
    host: 'smtp.gmail.com',
    port: '587',
  },
};

function smtpOAuthProviderFromAPI(
  provider?: NotificationSMTPOAuthProvider
): NotificationSMTPOAuthProvider {
  switch (provider) {
    case NotificationSMTPOAuthProvider.google_service_account:
      return NotificationSMTPOAuthProvider.google_service_account;
    case NotificationSMTPOAuthProvider.google_refresh:
      return NotificationSMTPOAuthProvider.google_refresh;
    default:
      return NotificationSMTPOAuthProvider.microsoft;
  }
}

type DraftRoute = {
  id?: string;
  channelId: string;
  enabled: boolean;
  events: NotificationEventType[];
};

type DraftRouteSet = {
  enabled: boolean;
  inheritGlobal: boolean;
  routes: DraftRoute[];
};

type RouteScopeKey = 'global' | 'workspace';

type NotificationHomeLink = {
  to: string;
  label: string;
  description: string;
};

type NotificationHomeSection = {
  title: string;
  links: NotificationHomeLink[];
};

const blankRouteSet: DraftRouteSet = {
  enabled: true,
  inheritGlobal: true,
  routes: [],
};

const DEFAULT_ROUTE_EVENTS = [
  NotificationEventType.dag_run_failed,
  NotificationEventType.dag_run_aborted,
  NotificationEventType.dag_run_rejected,
  NotificationEventType.dag_run_waiting,
];

function sameEvents(
  left: NotificationEventType[],
  right: NotificationEventType[]
): boolean {
  return (
    left.length === right.length && left.every((event) => right.includes(event))
  );
}

function smtpDraftFromAPI(settings: NotificationWorkspaceSettings): SMTPDraft {
  const smtp = settings.smtp;
  if (!smtp) {
    return { ...blankSMTPDraft };
  }
  return {
    mode: smtp.oauth ? 'oauth' : 'password',
    host: smtp.host || '',
    port: smtp.port || '',
    username: smtp.username || '',
    password: '',
    from: smtp.from || '',
    passwordConfigured: !!smtp.passwordConfigured,
    clearPassword: false,
    oauthProvider: smtpOAuthProviderFromAPI(smtp.oauth?.provider),
    tenantId: smtp.oauth?.tenantId || '',
    clientId: smtp.oauth?.clientId || '',
    clientSecret: '',
    refreshToken: '',
    serviceAccountJson: '',
    clientSecretConfigured: !!smtp.oauth?.clientSecretConfigured,
    refreshTokenConfigured: !!smtp.oauth?.refreshTokenConfigured,
    serviceAccountJsonConfigured: !!smtp.oauth?.serviceAccountJsonConfigured,
  };
}

function routeSetDraftFromAPI(routeSet?: NotificationRouteSet): DraftRouteSet {
  if (!routeSet) {
    return { ...blankRouteSet, routes: [] };
  }
  return {
    enabled: routeSet.enabled,
    inheritGlobal: routeSet.inheritGlobal,
    routes: (routeSet.routes || []).map((route) => ({
      id: route.id,
      channelId: route.channelId,
      enabled: route.enabled,
      events: route.events?.length ? route.events : [...DEFAULT_ROUTE_EVENTS],
    })),
  };
}

function routeSetInput(draft: DraftRouteSet): NotificationRouteSetInput {
  return {
    enabled: draft.enabled,
    inheritGlobal: draft.inheritGlobal,
    routes: draft.routes.map((route) => ({
      id: route.id,
      channelId: route.channelId,
      enabled: route.enabled,
      events: route.events,
    })),
  };
}

function smtpInput(draft: SMTPDraft) {
  const oauthDestination = smtpOAuthDestinations[draft.oauthProvider];
  const hasSMTP =
    draft.mode === 'oauth' ||
    draft.host.trim() ||
    draft.port.trim() ||
    draft.username.trim() ||
    draft.password.trim() ||
    draft.from.trim() ||
    draft.clearPassword;
  if (!hasSMTP) {
    return { smtp: null };
  }
  if (draft.mode === 'oauth') {
    return {
      smtp: {
        host: oauthDestination.host,
        port: oauthDestination.port,
        username: draft.username.trim() || undefined,
        from: draft.from.trim() || undefined,
        oauth: {
          provider: draft.oauthProvider,
          tenantId:
            draft.oauthProvider === NotificationSMTPOAuthProvider.microsoft
              ? draft.tenantId.trim() || undefined
              : undefined,
          clientId:
            draft.oauthProvider ===
            NotificationSMTPOAuthProvider.google_service_account
              ? undefined
              : draft.clientId.trim() || undefined,
          clientSecret: draft.clientSecret || undefined,
          refreshToken: draft.refreshToken || undefined,
          serviceAccountJson: draft.serviceAccountJson || undefined,
        },
      },
    };
  }
  return {
    smtp: {
      host: draft.host.trim() || undefined,
      port: draft.port.trim() || undefined,
      username: draft.username.trim() || undefined,
      password: draft.password.trim() || undefined,
      from: draft.from.trim() || undefined,
      clearPassword: draft.clearPassword || undefined,
    },
  };
}

function resetOAuthSecrets(draft: SMTPDraft): SMTPDraft {
  return {
    ...draft,
    clientSecret: '',
    refreshToken: '',
    serviceAccountJson: '',
    clientSecretConfigured: false,
    refreshTokenConfigured: false,
    serviceAccountJsonConfigured: false,
  };
}

function resetOAuthSecretState(draft: SMTPDraft): SMTPDraft {
  return {
    ...draft,
    clientSecretConfigured: false,
    refreshTokenConfigured: false,
    serviceAccountJsonConfigured: false,
  };
}

function NotificationHomeSectionLinks({
  section,
}: {
  section: NotificationHomeSection;
}): ReactElement {
  return (
    <section className="space-y-2">
      <h2 className="text-sm font-medium text-muted-foreground">
        {section.title}
      </h2>
      <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
        {section.links.map((link) => (
          <Link
            key={link.to}
            to={link.to}
            className="rounded-md border border-border bg-card px-4 py-3 transition-colors hover:border-border-strong hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <span className="block text-sm font-medium text-foreground">
              {link.label}
            </span>
            <span className="mt-1 block text-xs text-muted-foreground">
              {link.description}
            </span>
          </Link>
        ))}
      </div>
    </section>
  );
}

export default function NotificationsPage(): ReactElement {
  const { setTitle } = useContext(AppBarContext);

  useEffect(() => {
    setTitle('Notifications');
  }, [setTitle]);

  const sections: NotificationHomeSection[] = [
    {
      title: 'Setup',
      links: [
        {
          to: '/notification-rules',
          label: 'Rules',
          description: 'Set Global defaults and workspace overrides.',
        },
        {
          to: '/notification-channels',
          label: 'Channels',
          description:
            'Manage Slack, email, webhook, Telegram, and local command destinations.',
        },
      ],
    },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <Title>
        <I18nText text={'Notifications'} />
      </Title>

      {sections.map((section) => (
        <NotificationHomeSectionLinks key={section.title} section={section} />
      ))}
    </div>
  );
}

function channelLabel(channel?: DraftChannel, fallback?: string): string {
  return (
    channel?.name ||
    (channel?.type ? providerLabel(channel.type) : fallback) ||
    'Missing channel'
  );
}

function sameRouteSet(left: DraftRouteSet, right: DraftRouteSet): boolean {
  return (
    left.enabled === right.enabled &&
    left.inheritGlobal === right.inheritGlobal &&
    left.routes.length === right.routes.length &&
    left.routes.every((route, index) => {
      const other = right.routes[index];
      return (
        !!other &&
        other.id === route.id &&
        other.channelId === route.channelId &&
        other.enabled === route.enabled &&
        sameEvents(route.events, other.events)
      );
    })
  );
}

function NotificationHeader({
  activeTab = 'rules',
}: {
  activeTab?: 'rules' | 'channels';
}) {
  return (
    <header className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-[-0.01em]">
            <I18nText text={'Notifications'} />
          </h1>
          <p className="text-sm text-muted-foreground">
            <I18nText
              text={'Choose where workflow updates go and when they are sent.'}
            />
          </p>
        </div>
        {activeTab === 'rules' && (
          <Button asChild variant="outline">
            <Link to="/notification-channels">
              <Mail className="size-4" />
              <I18nText text={'Manage channels'} />
            </Link>
          </Button>
        )}
      </div>
      <nav
        aria-label="Notifications"
        className="inline-flex items-center border-b border-border"
      >
        {(['rules', 'channels'] as const).map((tab) => {
          const Icon = tab === 'rules' ? Bell : Mail;
          return (
            <Link
              key={tab}
              to={
                tab === 'rules'
                  ? '/notification-rules'
                  : '/notification-channels'
              }
              aria-current={activeTab === tab ? 'page' : undefined}
              className={cn(
                'inline-flex h-12 items-center gap-2 border-b-2 px-4 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                activeTab === tab
                  ? 'border-primary text-foreground [&_svg]:text-primary'
                  : 'border-transparent text-text-secondary hover:bg-muted hover:text-foreground'
              )}
            >
              <Icon className="size-4" />
              <I18nText text={tab === 'rules' ? 'Rules' : 'Channels'} />
            </Link>
          );
        })}
      </nav>
    </header>
  );
}

function ScopeSelector({
  activeScope,
  workspaceName,
  disabled,
  onChange,
}: {
  activeScope: RouteScopeKey;
  workspaceName: string;
  disabled: boolean;
  onChange: (scope: RouteScopeKey) => void;
}) {
  const { ts } = useI18n();
  const scopeId = useId();
  return (
    <div className="grid gap-2 sm:grid-cols-[auto_1fr] sm:gap-x-6">
      <label htmlFor={scopeId} className="text-sm font-medium sm:pt-3">
        <I18nText text={'Applies to'} />
      </label>
      <div className="space-y-2">
        <Select
          value={activeScope}
          disabled={disabled}
          onValueChange={(value) => onChange(value as RouteScopeKey)}
        >
          <SelectTrigger id={scopeId} className="w-full max-w-sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="global">
              <Globe2 className="size-4" />
              <I18nText text={'Global defaults'} />
            </SelectItem>
            {workspaceName && (
              <SelectItem value="workspace">
                <Building2 className="size-4" />
                {ts('{workspace} workspace', { workspace: workspaceName })}
              </SelectItem>
            )}
          </SelectContent>
        </Select>
        <p className="text-sm text-muted-foreground">
          <I18nText
            text={
              activeScope === 'global'
                ? 'Used unless a workspace or workflow has its own rules.'
                : 'Workspace rules apply unless a workflow has its own rules.'
            }
          />
        </p>
      </div>
    </div>
  );
}

function AddRuleDialog({
  open,
  channels,
  routes,
  onOpenChange,
  onAdd,
}: {
  open: boolean;
  channels: DraftChannel[];
  routes: DraftRoute[];
  onOpenChange: (open: boolean) => void;
  onAdd: (channelId: string) => void;
}) {
  const availableChannels = channels.filter(
    (channel) =>
      channel.id && !routes.some((route) => route.channelId === channel.id)
  );
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <I18nText text={'Add rule'} />
          </DialogTitle>
          <DialogDescription>
            <I18nText text={'Choose a destination for this rule.'} />
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-72 space-y-2 overflow-y-auto">
          {availableChannels.map((channel) => {
            const Icon = providerIcon(channel.type);
            return (
              <button
                key={channel.id}
                type="button"
                onClick={() => onAdd(channel.id!)}
                className="flex min-h-9 w-full items-center gap-3 rounded-md border border-border px-4 py-3 text-left hover:border-primary hover:bg-primary/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <Icon className="size-4 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate text-sm font-medium">
                  {channelLabel(channel)}
                </span>
                <span className="text-sm text-muted-foreground">
                  <I18nText
                    text={
                      channel.enabled
                        ? providerLabel(channel.type)
                        : 'Channel off'
                    }
                  />
                </span>
                <Plus className="size-4 text-primary" />
              </button>
            );
          })}
          {availableChannels.length === 0 && (
            <p className="py-4 text-sm text-muted-foreground">
              <I18nText
                text={
                  channels.length
                    ? 'Every channel already has a rule. Edit an existing rule or create another channel.'
                    : 'Create a channel before adding notification routes.'
                }
              />
            </p>
          )}
        </div>
        <DialogFooter className="border-t border-border">
          <Button asChild variant="outline">
            <Link to="/notification-channels">
              <Plus className="size-4" />
              <I18nText text={'Add channel'} />
            </Link>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function eventDotClass(event: NotificationEventType): string {
  switch (event) {
    case NotificationEventType.dag_run_failed:
      return 'bg-error';
    case NotificationEventType.dag_run_aborted:
    case NotificationEventType.dag_run_rejected:
      return 'bg-[var(--status-aborted)]';
    case NotificationEventType.dag_run_succeeded:
      return 'bg-success';
    default:
      return 'bg-warning';
  }
}

function RouteRuleCard({
  route,
  index,
  routes,
  channels,
  disabled,
  inherited,
  remoteNode,
  onUpdate,
  onDelete,
}: {
  route: DraftRoute;
  index: number;
  routes: DraftRoute[];
  channels: DraftChannel[];
  disabled: boolean;
  inherited: boolean;
  remoteNode: string;
  onUpdate: (index: number, updater: (route: DraftRoute) => DraftRoute) => void;
  onDelete: (index: number) => void;
}) {
  const { ts } = useI18n();
  const client = useClient();
  const fieldId = useId();
  const channel = channels.find((item) => item.id === route.channelId);
  const Icon = providerIcon(channel?.type);
  const label = channelLabel(channel, route.channelId);
  const commandLockReason = useCommandChannelLockReason();
  const lockReason =
    channel?.type === NotificationProviderType.command
      ? commandLockReason
      : undefined;
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{
    delivered: boolean;
    message: string;
  } | null>(null);
  const readOnly = disabled || inherited;

  useEffect(() => {
    setTestResult(null);
  }, [route.channelId, remoteNode]);

  const testChannel = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const { data, error } = await client.POST(
        '/notification-channels/{channelId}/test',
        {
          params: {
            path: { channelId: route.channelId },
            query: { remoteNode },
          },
        }
      );
      if (error) {
        throw new Error(
          error.message || ts('Failed to send test notification')
        );
      }
      const result = data?.results[0];
      setTestResult({
        delivered: !!result?.delivered,
        message: result?.delivered
          ? ts('Test delivered')
          : result?.error || ts('Delivery failed'),
      });
    } catch (error) {
      setTestResult({
        delivered: false,
        message:
          error instanceof Error
            ? error.message
            : ts('Failed to send test notification'),
      });
    } finally {
      setTesting(false);
    }
  };

  return (
    <section
      aria-label={ts('Rule for {channel}', { channel: label })}
      className="min-w-0 rounded-lg border border-border bg-card p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border pb-5">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-3">
          <label
            htmlFor={`${fieldId}-channel`}
            className="shrink-0 text-sm text-muted-foreground"
          >
            <I18nText text={'Send to'} />
          </label>
          <Select
            value={route.channelId}
            disabled={readOnly || testing}
            onValueChange={(channelId) =>
              onUpdate(index, (current) => ({ ...current, channelId }))
            }
          >
            <SelectTrigger
              id={`${fieldId}-channel`}
              className="w-full min-w-0 sm:w-64"
            >
              <span className="flex min-w-0 items-center gap-3">
                <Icon className="size-4 shrink-0 text-primary" />
                <SelectValue placeholder={ts('Select channel')} />
              </span>
            </SelectTrigger>
            <SelectContent>
              {!channel && (
                <SelectItem value={route.channelId}>
                  {ts('Missing channel')}
                </SelectItem>
              )}
              {channels
                .filter((item) => item.id)
                .map((item) => (
                  <SelectItem
                    key={item.id}
                    value={item.id!}
                    disabled={routes.some(
                      (other, otherIndex) =>
                        otherIndex !== index && other.channelId === item.id
                    )}
                  >
                    {channelLabel(item)}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
          {channel && (
            <span className="hidden border-l border-border pl-4 text-sm text-muted-foreground sm:block">
              <I18nText text={providerLabel(channel.type)} />
            </span>
          )}
        </div>
        <div className="flex items-center gap-4">
          <label className="flex min-h-9 cursor-pointer items-center gap-3 text-sm text-muted-foreground">
            <I18nText text={'Enabled'} />
            <Switch
              checked={route.enabled}
              disabled={readOnly}
              onCheckedChange={(enabled) =>
                onUpdate(index, (current) => ({ ...current, enabled }))
              }
              aria-label={ts('Toggle {title}', { title: label })}
              className="focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-card"
            />
          </label>
          {!inherited && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  disabled={disabled}
                  aria-label={ts('Rule actions for {channel}', {
                    channel: label,
                  })}
                >
                  <MoreHorizontal className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  className="gap-2 text-destructive"
                  onSelect={() => onDelete(index)}
                >
                  <Trash2 className="size-4" />
                  <I18nText text={'Delete rule'} />
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
      </div>
      <fieldset
        className="min-w-0 pt-5"
        disabled={readOnly}
        aria-describedby={`${fieldId}-events-help`}
      >
        <legend className="float-left mb-1 w-full text-sm font-semibold">
          <I18nText text={'Notify on'} />
        </legend>
        <p
          id={`${fieldId}-events-help`}
          className="clear-both mb-4 text-sm text-muted-foreground"
        >
          <I18nText
            text={'Send a notification when any selected event occurs.'}
          />
        </p>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {EVENT_OPTIONS.map((event) => {
            const checked = route.events.includes(event.value);
            return (
              <label
                key={event.value}
                className={cn(
                  'flex min-h-9 items-center gap-3 rounded-md border px-4 py-3 text-sm transition-colors focus-within:ring-2 focus-within:ring-ring',
                  readOnly
                    ? 'cursor-default'
                    : 'cursor-pointer hover:border-primary/60',
                  checked
                    ? 'border-primary/60 bg-primary/10 text-foreground'
                    : 'border-border-strong text-foreground'
                )}
              >
                <Checkbox
                  checked={checked}
                  disabled={readOnly}
                  className="size-4 border-muted-foreground/70 data-[state=checked]:bg-primary-fill data-[state=checked]:text-primary-foreground data-[state=checked]:border-primary"
                  onCheckedChange={(checked) =>
                    onUpdate(index, (current) => ({
                      ...current,
                      events: checked
                        ? [...current.events, event.value]
                        : current.events.filter(
                            (value) => value !== event.value
                          ),
                    }))
                  }
                />
                <span
                  aria-hidden="true"
                  className={cn(
                    'size-2.5 shrink-0 rounded-full',
                    eventDotClass(event.value)
                  )}
                />
                <I18nText text={event.label} />
              </label>
            );
          })}
        </div>
        {route.events.length === 0 && (
          <p role="alert" className="mt-3 text-sm text-destructive">
            <I18nText text={'Select at least one event before saving.'} />
          </p>
        )}
      </fieldset>
      {(!channel || !channel.enabled) && (
        <p className="mt-4 flex items-center gap-2 text-sm text-warning">
          <AlertTriangle className="size-4 shrink-0" />
          <I18nText
            text={
              !channel
                ? 'This channel is missing. Choose another destination.'
                : 'This channel is off. Enable it in Channels to receive notifications.'
            }
          />
        </p>
      )}
      {lockReason && (
        <p className="mt-4 flex items-center gap-2 text-sm text-warning">
          <AlertTriangle className="size-4 shrink-0" />
          <I18nText text={lockReason} />
        </p>
      )}
      <div className="mt-5 flex flex-wrap items-center gap-3 border-t border-border pt-4">
        <Button
          variant="outline"
          onClick={testChannel}
          disabled={disabled || testing || !channel || lockReason !== undefined}
        >
          {testing ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <Send className="size-4" />
          )}
          <I18nText text={testing ? 'Sending...' : 'Test channel'} />
        </Button>
        {testResult ? (
          <p
            role={testResult.delivered ? 'status' : 'alert'}
            className={cn(
              'text-sm',
              testResult.delivered ? 'text-success' : 'text-destructive'
            )}
          >
            {testResult.message}
          </p>
        ) : (
          <p className="text-sm text-muted-foreground">
            <I18nText
              text={'Sends a sample notification to this destination.'}
            />
          </p>
        )}
      </div>
    </section>
  );
}

type StatusCardProps = {
  error: string | null;
  notice: string | null;
};

function StatusCard({ error, notice }: StatusCardProps) {
  if (!error && !notice) {
    return null;
  }

  return (
    <div className="space-y-3">
      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <I18nText text={error} />
        </div>
      )}
      {notice && (
        <div className="flex items-start gap-2 rounded-md border border-success/30 bg-success/10 p-3 text-sm text-success">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
          <I18nText text={notice} />
        </div>
      )}
    </div>
  );
}

function LoadingCard({ label }: { label: string }) {
  return (
    <Card>
      <CardContent className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        {label}
      </CardContent>
    </Card>
  );
}

function apiErrorMessage(error: unknown, fallback: string): string | null {
  if (!error) {
    return null;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  if (typeof error === 'object' && error !== null && 'message' in error) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === 'string' && message.trim() !== '') {
      return message;
    }
  }
  return fallback;
}

export function NotificationRulesPage() {
  const { selectedRemoteNode, workspaceSelection } = useContext(AppBarContext);
  const scopeKey = JSON.stringify([
    selectedRemoteNode || 'local',
    workspaceSelection?.kind,
    workspaceNameForSelection(workspaceSelection),
  ]);
  return <NotificationRulesContent key={scopeKey} />;
}

function NotificationRulesContent() {
  const { ts } = useI18n();
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const workspaceSelection = appBarContext.workspaceSelection;
  const selectedWorkspaceName = workspaceNameForSelection(workspaceSelection);
  const canConfigureWorkspaceRoutes =
    workspaceSelection?.kind === WorkspaceKind.workspace &&
    !!selectedWorkspaceName;
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [globalRoutes, setGlobalRoutes] = useState<DraftRouteSet>({
    ...blankRouteSet,
    routes: [],
  });
  const [workspaceRoutes, setWorkspaceRoutes] = useState<DraftRouteSet>({
    ...blankRouteSet,
    routes: [],
  });
  const [isSavingGlobalRoutes, setIsSavingGlobalRoutes] = useState(false);
  const [isSavingWorkspaceRoutes, setIsSavingWorkspaceRoutes] = useState(false);
  const [channels, setChannels] = useState<DraftChannel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [activeScope, setActiveScope] = useState<RouteScopeKey>('global');
  const [addRuleOpen, setAddRuleOpen] = useState(false);

  const {
    data: channelsData,
    error: channelsLoadError,
    isLoading: channelsLoading,
  } = useQuery(
    '/notification-channels',
    {
      params: {
        query: { remoteNode },
      },
    },
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  const {
    data: globalRoutesData,
    error: globalRoutesLoadError,
    isLoading: globalRoutesLoading,
    mutate: mutateGlobalRoutes,
  } = useQuery(
    '/notification-routes/global',
    {
      params: {
        query: { remoteNode },
      },
    },
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  const {
    data: workspaceRoutesData,
    error: workspaceRoutesLoadError,
    isLoading: workspaceRoutesLoading,
    mutate: mutateWorkspaceRoutes,
  } = useQuery(
    '/notification-routes/workspaces/{workspaceName}',
    whenEnabled(canConfigureWorkspaceRoutes, {
      params: {
        path: { workspaceName: selectedWorkspaceName },
        query: { remoteNode },
      },
    }),
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  const isLoading =
    channelsLoading ||
    globalRoutesLoading ||
    (canConfigureWorkspaceRoutes && workspaceRoutesLoading);
  const loadError =
    apiErrorMessage(channelsLoadError, 'Failed to load channels') ??
    apiErrorMessage(globalRoutesLoadError, 'Failed to load Global rules') ??
    apiErrorMessage(workspaceRoutesLoadError, 'Failed to load workspace rules');

  useEffect(() => {
    appBarContext.setTitle('Notification Rules');
  }, [appBarContext]);

  useEffect(() => {
    if (!canConfigureWorkspaceRoutes && activeScope === 'workspace') {
      setActiveScope('global');
    }
  }, [activeScope, canConfigureWorkspaceRoutes]);

  useEffect(() => {
    if (channelsData) {
      setChannels((channelsData.channels || []).map(draftChannelFromAPI));
    }
  }, [channelsData]);

  useEffect(() => {
    if (globalRoutesData) {
      setGlobalRoutes(routeSetDraftFromAPI(globalRoutesData));
    }
  }, [globalRoutesData]);

  useEffect(() => {
    if (!canConfigureWorkspaceRoutes) {
      setWorkspaceRoutes({ ...blankRouteSet, routes: [] });
      return;
    }
    if (workspaceRoutesData) {
      setWorkspaceRoutes(routeSetDraftFromAPI(workspaceRoutesData));
    }
  }, [canConfigureWorkspaceRoutes, workspaceRoutesData]);

  if (loadError && !isLoading) {
    return <StatusCard error={loadError} notice={null} />;
  }

  const saveGlobalRoutes = async () => {
    setIsSavingGlobalRoutes(true);
    setError(null);
    setNotice(null);
    try {
      const { data: routeSet, error: apiError } = await client.PUT(
        '/notification-routes/global',
        {
          params: {
            query: { remoteNode },
          },
          body: routeSetInput(globalRoutes),
        }
      );
      if (apiError) {
        throw new Error(apiError.message || 'Failed to save Global rules');
      }
      if (routeSet) {
        setGlobalRoutes(routeSetDraftFromAPI(routeSet));
        mutateGlobalRoutes(routeSet, { revalidate: false });
      }
      setNotice('Global rules saved');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save Global rules'
      );
    } finally {
      setIsSavingGlobalRoutes(false);
    }
  };

  const saveWorkspaceRoutes = async () => {
    if (!canConfigureWorkspaceRoutes) {
      return;
    }
    setIsSavingWorkspaceRoutes(true);
    setError(null);
    setNotice(null);
    try {
      const { data: routeSet, error: apiError } = await client.PUT(
        '/notification-routes/workspaces/{workspaceName}',
        {
          params: {
            path: { workspaceName: selectedWorkspaceName },
            query: { remoteNode },
          },
          body: routeSetInput(workspaceRoutes),
        }
      );
      if (apiError) {
        throw new Error(apiError.message || 'Failed to save workspace rules');
      }
      if (routeSet) {
        setWorkspaceRoutes(routeSetDraftFromAPI(routeSet));
        mutateWorkspaceRoutes(routeSet, { revalidate: false });
      }
      setNotice(
        routeSet?.inheritGlobal
          ? 'Workspace now inherits Global rules'
          : 'Workspace rules saved'
      );
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : 'Failed to save workspace notifications'
      );
    } finally {
      setIsSavingWorkspaceRoutes(false);
    }
  };

  const isWorkspaceScope =
    activeScope === 'workspace' && canConfigureWorkspaceRoutes;
  const activeDraft = isWorkspaceScope ? workspaceRoutes : globalRoutes;
  const savedDraft = routeSetDraftFromAPI(
    isWorkspaceScope ? workspaceRoutesData : globalRoutesData
  );
  const hasUnsavedChanges = !sameRouteSet(activeDraft, savedDraft);
  // The save bar appears with the first edit and then keeps reporting state.
  const [hasEdited, setHasEdited] = useState(false);
  useEffect(() => {
    if (hasUnsavedChanges) {
      setHasEdited(true);
    }
  }, [hasUnsavedChanges]);
  const activeSaving = isSavingGlobalRoutes || isSavingWorkspaceRoutes;
  const inherited = isWorkspaceScope && workspaceRoutes.inheritGlobal;
  const displayedDraft = inherited ? globalRoutes : activeDraft;
  const canSave =
    hasUnsavedChanges &&
    !isLoading &&
    !activeSaving &&
    (inherited || activeDraft.routes.every((route) => route.events.length > 0));
  const updateActiveRoutes = (
    updater: (current: DraftRouteSet) => DraftRouteSet
  ) => {
    setError(null);
    setNotice(null);
    if (isWorkspaceScope) {
      setWorkspaceRoutes(updater);
    } else {
      setGlobalRoutes(updater);
    }
  };
  const updateRoute = (
    index: number,
    updater: (route: DraftRoute) => DraftRoute
  ) =>
    updateActiveRoutes((current) => ({
      ...current,
      routes: current.routes.map((route, routeIndex) =>
        routeIndex === index ? updater(route) : route
      ),
    }));
  const deleteRoute = (index: number) =>
    updateActiveRoutes((current) => ({
      ...current,
      routes: current.routes.filter((_, routeIndex) => routeIndex !== index),
    }));
  const addRoute = (channelId: string) => {
    updateActiveRoutes((current) => ({
      ...current,
      routes: [
        ...current.routes,
        { channelId, enabled: true, events: [...DEFAULT_ROUTE_EVENTS] },
      ],
    }));
    setAddRuleOpen(false);
  };
  const configureWorkspace = () => {
    updateActiveRoutes((current) => ({
      ...current,
      inheritGlobal: false,
      routes: current.routes.length
        ? current.routes
        : globalRoutes.routes.map(({ channelId, enabled, events }) => ({
            channelId,
            enabled,
            events: [...events],
          })),
    }));
  };
  const cancelChanges = () => updateActiveRoutes(() => savedDraft);
  const saveActiveRoutes = isWorkspaceScope
    ? saveWorkspaceRoutes
    : saveGlobalRoutes;

  return (
    <div className="flex max-w-5xl flex-col gap-4">
      <NotificationHeader />
      <StatusCard error={error ?? loadError} notice={notice} />
      {isLoading ? (
        <I18nProps>
          <LoadingCard label="Refreshing notification rules..." />
        </I18nProps>
      ) : (
        <>
          <ScopeSelector
            activeScope={activeScope}
            workspaceName={selectedWorkspaceName}
            disabled={activeSaving}
            onChange={(scope) => {
              setActiveScope(scope);
              setError(null);
              setNotice(null);
            }}
          />

          {isWorkspaceScope && (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border bg-muted/30 p-4">
              <div className="space-y-1">
                <p className="font-medium">
                  <I18nText
                    text={
                      inherited
                        ? 'Inheriting Global rules'
                        : 'Workspace override'
                    }
                  />
                </p>
                <p className="text-sm text-muted-foreground">
                  <I18nText
                    text={
                      inherited
                        ? 'Configure workspace rules to choose different events or destinations.'
                        : 'These rules replace Global defaults for this workspace.'
                    }
                  />
                </p>
              </div>
              <Button
                variant="outline"
                disabled={activeSaving}
                onClick={
                  inherited
                    ? configureWorkspace
                    : () =>
                        updateActiveRoutes((current) => ({
                          ...current,
                          inheritGlobal: true,
                        }))
                }
              >
                <I18nText
                  text={inherited ? 'Configure workspace' : 'Use Global rules'}
                />
              </Button>
            </div>
          )}

          <div className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-3">
                <h2 className="text-base font-semibold">
                  <I18nText text={'Notification rules'} />
                </h2>
                <span className="text-sm text-muted-foreground">
                  {ts(
                    displayedDraft.routes.length === 1
                      ? '{count} rule'
                      : '{count} rules',
                    { count: displayedDraft.routes.length }
                  )}
                </span>
              </div>
              {!inherited && (
                <div className="flex items-center gap-2">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon"
                        disabled={activeSaving}
                        aria-label={ts('Rule settings')}
                      >
                        <MoreHorizontal className="size-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        onSelect={() =>
                          updateActiveRoutes((current) => ({
                            ...current,
                            enabled: !current.enabled,
                          }))
                        }
                      >
                        <I18nText
                          text={
                            activeDraft.enabled
                              ? 'Turn off all rules'
                              : 'Turn on all rules'
                          }
                        />
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                  <Button
                    variant="primary"
                    onClick={() => setAddRuleOpen(true)}
                    disabled={activeSaving}
                  >
                    <Plus className="size-4" />
                    <I18nText text={'Add rule'} />
                  </Button>
                </div>
              )}
            </div>
            {!displayedDraft.enabled && (
              <p className="flex items-center gap-2 rounded-md border border-warning/30 bg-warning/10 p-4 text-sm text-warning">
                <Info className="size-4 shrink-0" />
                <I18nText
                  text={
                    'All rules are off for this scope. Notifications will not be sent.'
                  }
                />
              </p>
            )}
            {displayedDraft.routes.length ? (
              displayedDraft.routes.map((route, index) => (
                <RouteRuleCard
                  key={route.id || `${route.channelId}-${index}`}
                  route={route}
                  index={index}
                  routes={displayedDraft.routes}
                  channels={channels}
                  disabled={activeSaving}
                  inherited={inherited}
                  remoteNode={remoteNode}
                  onUpdate={updateRoute}
                  onDelete={deleteRoute}
                />
              ))
            ) : (
              <div className="rounded-lg border border-dashed border-border-strong px-6 py-10 text-center">
                <Bell className="mx-auto mb-3 size-7 text-muted-foreground" />
                <p className="font-medium">
                  <I18nText text={'No notification rules yet'} />
                </p>
                <p className="mt-2 text-sm text-muted-foreground">
                  <I18nText
                    text={
                      inherited
                        ? 'Global has no routes. DAGs will not notify unless a workspace or DAG is configured.'
                        : isWorkspaceScope
                          ? 'This workspace override has no routes, so DAGs here will not notify unless a DAG is configured.'
                          : 'Add a rule to choose a destination and the events to send.'
                    }
                  />
                </p>
                <MonitoringOffer className="mx-auto mt-6 max-w-xl" />
              </div>
            )}
            <p className="flex items-start gap-2 text-sm text-muted-foreground">
              <Info className="mt-0.5 size-4 shrink-0" />
              <I18nText
                text={
                  'One rule per channel. Select multiple events for each destination.'
                }
              />
            </p>
          </div>

          {(hasEdited || activeSaving) && (
            <div className="sticky bottom-0 z-10 flex flex-wrap items-center justify-between gap-4 rounded-lg border border-border-strong bg-card px-5 py-4 shadow-sm">
              <div className="flex items-start gap-3" role="status">
                <div>
                  <p className="font-medium">
                    <I18nText
                      text={
                        activeSaving
                          ? 'Saving...'
                          : hasUnsavedChanges
                            ? 'Unsaved changes'
                            : 'All changes saved'
                      }
                    />
                  </p>
                  <p className="mt-0.5 text-sm text-muted-foreground">
                    <I18nText text={'Rules take effect after saving.'} />
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-3">
                <Button
                  variant="outline"
                  className="px-5"
                  onClick={cancelChanges}
                  disabled={!hasUnsavedChanges || activeSaving}
                >
                  <I18nText text={'Cancel'} />
                </Button>
                <Button
                  variant="primary"
                  className="px-5"
                  onClick={saveActiveRoutes}
                  disabled={!canSave}
                >
                  {activeSaving && <Loader2 className="size-4 animate-spin" />}
                  <I18nText text={'Save changes'} />
                </Button>
              </div>
            </div>
          )}
        </>
      )}
      <AddRuleDialog
        open={addRuleOpen}
        onOpenChange={setAddRuleOpen}
        channels={channels}
        routes={activeDraft.routes}
        onAdd={addRoute}
      />
    </div>
  );
}

export function NotificationChannelsPage() {
  const { selectedRemoteNode } = useContext(AppBarContext);
  const remoteNode = selectedRemoteNode || 'local';
  return (
    <NotificationChannelsContent key={remoteNode} remoteNode={remoteNode} />
  );
}

function NotificationChannelsContent({ remoteNode }: { remoteNode: string }) {
  const client = useClient();
  const { ts } = useI18n();
  const appBarContext = useContext(AppBarContext);
  const [smtpDraft, setSMTPDraft] = useState<SMTPDraft>(blankSMTPDraft);
  const [isSavingSettings, setIsSavingSettings] = useState(false);
  const [channels, setChannels] = useState<DraftChannel[]>([]);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const {
    data: settingsData,
    error: settingsLoadError,
    isLoading: settingsLoading,
    mutate: mutateSettings,
  } = useQuery(
    '/notification-settings',
    {
      params: {
        query: { remoteNode },
      },
    },
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  const {
    data: channelsData,
    error: channelsLoadError,
    isLoading: channelsLoading,
    mutate: mutateChannels,
  } = useQuery(
    '/notification-channels',
    {
      params: {
        query: { remoteNode },
      },
    },
    {
      revalidateOnFocus: false,
      revalidateOnMount: true,
    }
  );

  const isLoading = settingsLoading || channelsLoading;
  const oauthDestination = smtpOAuthDestinations[smtpDraft.oauthProvider];
  const savedSMTP = settingsData?.smtp;
  const emailConfigured = !!(savedSMTP?.host || savedSMTP?.oauth);
  const loadError =
    apiErrorMessage(settingsLoadError, 'Failed to load email delivery') ??
    apiErrorMessage(channelsLoadError, 'Failed to load channels');

  useEffect(() => {
    appBarContext.setTitle('Notification Channels');
  }, [appBarContext]);

  useEffect(() => {
    if (settingsData) {
      setSMTPDraft(smtpDraftFromAPI(settingsData));
    }
  }, [settingsData]);

  useEffect(() => {
    if (channelsData) {
      setChannels((channelsData.channels || []).map(draftChannelFromAPI));
    }
  }, [channelsData]);

  if (loadError && !isLoading) {
    return <StatusCard error={loadError} notice={null} />;
  }

  const openSettings = () => {
    setSMTPDraft(
      settingsData ? smtpDraftFromAPI(settingsData) : { ...blankSMTPDraft }
    );
    setSettingsError(null);
    setNotice(null);
    setSettingsOpen(true);
  };

  const saveSettings = async () => {
    setIsSavingSettings(true);
    setSettingsError(null);
    setNotice(null);
    try {
      const { data: settings, error: apiError } = await client.PUT(
        '/notification-settings',
        {
          params: {
            query: { remoteNode },
          },
          body: smtpInput(smtpDraft),
        }
      );
      if (apiError) {
        throw new Error(apiError.message || 'Failed to save email delivery');
      }
      if (settings) {
        setSMTPDraft(smtpDraftFromAPI(settings));
        mutateSettings(settings, { revalidate: false });
      }
      setNotice('Email delivery saved');
      setSettingsOpen(false);
    } catch (err) {
      setSettingsError(
        err instanceof Error ? err.message : 'Failed to save email delivery'
      );
    } finally {
      setIsSavingSettings(false);
    }
  };

  const saveChannel = async (channel: DraftChannel) => {
    const response = channel.id
      ? await client.PUT('/notification-channels/{channelId}', {
          params: { path: { channelId: channel.id }, query: { remoteNode } },
          body: channelInput(channel),
        })
      : await client.POST('/notification-channels', {
          params: { query: { remoteNode } },
          body: channelInput(channel),
        });
    if (response.error || !response.data) {
      throw new Error(response.error?.message || ts('Failed to save channel'));
    }
    const saved = draftChannelFromAPI(response.data);
    setChannels((current) =>
      channel.id
        ? current.map((item) => (item.id === channel.id ? saved : item))
        : [...current, saved]
    );
    void mutateChannels();
  };

  const deleteChannel = async (channel: DraftChannel) => {
    const { error } = await client.DELETE(
      '/notification-channels/{channelId}',
      {
        params: { path: { channelId: channel.id! }, query: { remoteNode } },
      }
    );
    if (error) {
      throw new Error(error.message || ts('Failed to delete channel'));
    }
    setChannels((current) => current.filter((item) => item.id !== channel.id));
    void mutateChannels();
  };

  const testChannel = async (channelId: string) => {
    const { data, error } = await client.POST(
      '/notification-channels/{channelId}/test',
      {
        params: { path: { channelId }, query: { remoteNode } },
      }
    );
    if (error) {
      throw new Error(error.message || ts('Failed to send test notification'));
    }
    return data?.results[0];
  };

  return (
    <div className="flex max-w-5xl flex-col gap-4">
      <NotificationHeader activeTab="channels" />
      <StatusCard error={loadError} notice={notice} />
      {isLoading && (
        <I18nProps>
          <LoadingCard label="Refreshing notification channels..." />
        </I18nProps>
      )}

      {!isLoading && (
        <>
          <NotificationChannelsSection
            channels={channels}
            onSave={saveChannel}
            onDelete={deleteChannel}
            onTest={testChannel}
          />
          <section
            aria-label={ts('Email delivery')}
            className="flex flex-wrap items-center justify-between gap-5 rounded-lg border border-border bg-card p-4"
          >
            <div className="flex min-w-0 items-start gap-4">
              <span className="flex size-12 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                <Mail className="size-6" />
              </span>
              <div className="min-w-0 space-y-2">
                <div className="flex flex-wrap items-center gap-3">
                  <h2 className="text-sm font-semibold">
                    <I18nText text={'Email delivery'} />
                  </h2>
                  <Badge variant={emailConfigured ? 'success' : 'default'}>
                    <I18nText
                      text={emailConfigured ? 'Configured' : 'Not configured'}
                    />
                  </Badge>
                </div>
                <p className="text-sm text-muted-foreground">
                  <I18nText text={'Shared sender for email channels.'} />
                </p>
                <p className="break-all text-sm text-muted-foreground">
                  {emailConfigured
                    ? `${savedSMTP?.oauth ? 'OAuth 2.0' : 'SMTP'} · ${savedSMTP?.host || ''} · ${ts('Port')} ${savedSMTP?.port || ''}`
                    : ts(
                        'Configure email delivery before testing an email channel.'
                      )}
                </p>
              </div>
            </div>
            <Button variant="outline" onClick={openSettings}>
              <I18nText text={'Configure'} />
              <ChevronRight className="size-4" />
            </Button>
          </section>
          <p className="flex flex-wrap items-start gap-2 text-sm text-muted-foreground">
            <Info className="size-4 shrink-0" />
            <I18nText
              text={
                'Adding a channel does not send notifications. Set up a rule to start delivery.'
              }
            />
            <Link
              to="/notification-rules"
              className="inline-flex items-center gap-2 text-primary hover:underline"
            >
              <I18nText text={'Go to Rules'} />
              <ArrowRight className="size-4" />
            </Link>
          </p>
        </>
      )}
      <Dialog
        open={settingsOpen}
        onOpenChange={(open) => {
          if (!isSavingSettings) {
            setSettingsOpen(open);
          }
        }}
      >
        <DialogContent
          className="flex max-h-[90dvh] w-[calc(100%-2rem)] max-w-2xl flex-col gap-0 overflow-hidden p-0"
          onInteractOutside={(event) => event.preventDefault()}
        >
          <DialogHeader className="border-b border-border px-6 py-5 pr-12">
            <DialogTitle>
              <I18nText text={'Email delivery'} />
            </DialogTitle>
            <DialogDescription>
              <I18nText text={'Shared sender for email channels.'} />
            </DialogDescription>
          </DialogHeader>
          <form
            className="flex min-h-0 flex-col"
            onSubmit={(event) => {
              event.preventDefault();
              void saveSettings();
            }}
          >
            <fieldset
              disabled={isSavingSettings}
              className="min-h-0 space-y-4 overflow-y-auto p-6"
            >
              {settingsError && (
                <p role="alert" className="text-sm text-destructive">
                  {settingsError}
                </p>
              )}

              <div className="grid gap-3 md:grid-cols-2">
                <div className="space-y-2">
                  <p className="text-sm font-medium">
                    <I18nText text={'SMTP authentication'} />
                  </p>
                  <Select
                    value={smtpDraft.mode}
                    onValueChange={(value) =>
                      setSMTPDraft((current) =>
                        value === 'oauth'
                          ? {
                              ...current,
                              mode: 'oauth',
                              password: '',
                              clearPassword: false,
                            }
                          : { ...current, mode: 'password' }
                      )
                    }
                  >
                    <I18nProps>
                      <SelectTrigger
                        className="w-full"
                        aria-label="SMTP authentication"
                      >
                        <I18nProps>
                          <SelectValue placeholder="Authentication" />
                        </I18nProps>
                      </SelectTrigger>
                    </I18nProps>
                    <SelectContent>
                      <SelectItem value="password">
                        <I18nText text={'Password'} />
                      </SelectItem>
                      <SelectItem value="oauth">
                        <I18nText text={'OAuth 2.0'} />
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                {smtpDraft.mode === 'oauth' && (
                  <div className="space-y-2">
                    <p className="text-sm font-medium">
                      <I18nText text={'OAuth provider'} />
                    </p>
                    <Select
                      value={smtpDraft.oauthProvider}
                      onValueChange={(value) =>
                        setSMTPDraft((current) =>
                          resetOAuthSecrets({
                            ...current,
                            oauthProvider:
                              value as NotificationSMTPOAuthProvider,
                            tenantId: '',
                            clientId: '',
                          })
                        )
                      }
                    >
                      <I18nProps>
                        <SelectTrigger
                          className="w-full"
                          aria-label="OAuth provider"
                        >
                          <I18nProps>
                            <SelectValue placeholder="OAuth provider" />
                          </I18nProps>
                        </SelectTrigger>
                      </I18nProps>
                      <SelectContent>
                        <SelectItem
                          value={NotificationSMTPOAuthProvider.microsoft}
                        >
                          <I18nText text={'Microsoft 365'} />
                        </SelectItem>
                        <SelectItem
                          value={
                            NotificationSMTPOAuthProvider.google_service_account
                          }
                        >
                          <I18nText text={'Google Workspace service account'} />
                        </SelectItem>
                        <SelectItem
                          value={NotificationSMTPOAuthProvider.google_refresh}
                        >
                          <I18nText text={'Google refresh token'} />
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                )}
              </div>
              <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_120px]">
                <label className="block space-y-2">
                  <span className="text-sm font-medium">
                    <I18nText text={'SMTP host'} />
                  </span>
                  <I18nProps>
                    <Input
                      value={
                        smtpDraft.mode === 'oauth'
                          ? oauthDestination.host
                          : smtpDraft.host
                      }
                      placeholder="SMTP host"
                      disabled={smtpDraft.mode === 'oauth'}
                      onChange={(event) =>
                        setSMTPDraft((current) => ({
                          ...current,
                          host: event.target.value,
                        }))
                      }
                    />
                  </I18nProps>
                </label>
                <label className="block space-y-2">
                  <span className="text-sm font-medium">
                    <I18nText text={'Port'} />
                  </span>
                  <I18nProps>
                    <Input
                      value={
                        smtpDraft.mode === 'oauth'
                          ? oauthDestination.port
                          : smtpDraft.port
                      }
                      placeholder="Port"
                      inputMode="numeric"
                      disabled={smtpDraft.mode === 'oauth'}
                      onChange={(event) =>
                        setSMTPDraft((current) => ({
                          ...current,
                          port: event.target.value,
                        }))
                      }
                    />
                  </I18nProps>
                </label>
              </div>
              <div className="grid gap-3 md:grid-cols-2">
                <label className="block space-y-2">
                  <span className="text-sm font-medium">
                    <I18nText
                      text={
                        smtpDraft.mode === 'oauth'
                          ? 'Sender mailbox'
                          : 'Username'
                      }
                    />
                  </span>
                  <I18nProps>
                    <Input
                      value={smtpDraft.username}
                      placeholder={
                        smtpDraft.mode === 'oauth'
                          ? 'Sender mailbox'
                          : 'Username'
                      }
                      onChange={(event) => {
                        const username = event.target.value;
                        setSMTPDraft((current) => {
                          const next = { ...current, username };
                          return current.mode === 'oauth' &&
                            username !== current.username
                            ? resetOAuthSecretState(next)
                            : next;
                        });
                      }}
                    />
                  </I18nProps>
                </label>
                {smtpDraft.mode === 'password' && (
                  <I18nProps>
                    <label className="block space-y-2">
                      <span className="text-sm font-medium">
                        <I18nText text={'Password'} />
                      </span>
                      <I18nProps>
                        <Input
                          type="password"
                          value={smtpDraft.password}
                          placeholder={
                            smtpDraft.passwordConfigured
                              ? 'Password configured'
                              : 'Password'
                          }
                          onChange={(event) =>
                            setSMTPDraft((current) => ({
                              ...current,
                              password: event.target.value,
                              clearPassword: false,
                            }))
                          }
                        />
                      </I18nProps>
                    </label>
                  </I18nProps>
                )}
                {smtpDraft.mode === 'oauth' && (
                  <I18nProps>
                    <label className="block space-y-2">
                      <span className="text-sm font-medium">
                        <I18nText text={'Default sender'} />
                      </span>
                      <I18nProps>
                        <Input
                          value={smtpDraft.from}
                          placeholder="Default sender"
                          onChange={(event) =>
                            setSMTPDraft((current) => ({
                              ...current,
                              from: event.target.value,
                            }))
                          }
                        />
                      </I18nProps>
                    </label>
                  </I18nProps>
                )}
              </div>
              {smtpDraft.mode === 'password' && (
                <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px]">
                  <label className="block space-y-2">
                    <span className="text-sm font-medium">
                      <I18nText text={'Default sender'} />
                    </span>
                    <I18nProps>
                      <Input
                        value={smtpDraft.from}
                        placeholder="Default sender"
                        onChange={(event) =>
                          setSMTPDraft((current) => ({
                            ...current,
                            from: event.target.value,
                          }))
                        }
                      />
                    </I18nProps>
                  </label>
                  <label className="flex h-9 self-end items-center gap-2 rounded-md border border-border px-3 text-sm">
                    <Checkbox
                      checked={smtpDraft.clearPassword}
                      disabled={!smtpDraft.passwordConfigured}
                      onCheckedChange={(value) =>
                        setSMTPDraft((current) => ({
                          ...current,
                          password: '',
                          clearPassword: !!value,
                        }))
                      }
                    />
                    <I18nText text={'Clear password'} />
                  </label>
                </div>
              )}
              {smtpDraft.mode === 'oauth' &&
                smtpDraft.oauthProvider ===
                  NotificationSMTPOAuthProvider.microsoft && (
                  <>
                    <div className="grid gap-3 md:grid-cols-2">
                      <label className="block space-y-2">
                        <span className="text-sm font-medium">
                          <I18nText text={'Microsoft tenant ID'} />
                        </span>
                        <I18nProps>
                          <Input
                            value={smtpDraft.tenantId}
                            placeholder="Microsoft tenant ID"
                            onChange={(event) => {
                              const tenantId = event.target.value;
                              setSMTPDraft((current) =>
                                resetOAuthSecretState({ ...current, tenantId })
                              );
                            }}
                          />
                        </I18nProps>
                      </label>
                      <label className="block space-y-2">
                        <span className="text-sm font-medium">
                          <I18nText text={'Client ID'} />
                        </span>
                        <I18nProps>
                          <Input
                            value={smtpDraft.clientId}
                            placeholder="Client ID"
                            onChange={(event) => {
                              const clientId = event.target.value;
                              setSMTPDraft((current) =>
                                resetOAuthSecretState({ ...current, clientId })
                              );
                            }}
                          />
                        </I18nProps>
                      </label>
                    </div>
                    <label className="block space-y-2">
                      <span className="text-sm font-medium">
                        <I18nText text={'Client secret'} />
                      </span>
                      <I18nProps>
                        <Input
                          type="password"
                          value={smtpDraft.clientSecret}
                          placeholder={
                            smtpDraft.clientSecretConfigured
                              ? 'Client secret configured'
                              : 'Client secret'
                          }
                          onChange={(event) =>
                            setSMTPDraft((current) => ({
                              ...current,
                              clientSecret: event.target.value,
                            }))
                          }
                        />
                      </I18nProps>
                    </label>
                  </>
                )}
              {smtpDraft.mode === 'oauth' &&
                smtpDraft.oauthProvider ===
                  NotificationSMTPOAuthProvider.google_service_account && (
                  <I18nProps>
                    <Textarea
                      value={smtpDraft.serviceAccountJson}
                      placeholder={
                        smtpDraft.serviceAccountJsonConfigured
                          ? 'Service-account JSON configured'
                          : 'Service-account JSON'
                      }
                      onChange={(event) =>
                        setSMTPDraft((current) => ({
                          ...current,
                          serviceAccountJson: event.target.value,
                        }))
                      }
                    />
                  </I18nProps>
                )}
              {smtpDraft.mode === 'oauth' &&
                smtpDraft.oauthProvider ===
                  NotificationSMTPOAuthProvider.google_refresh && (
                  <>
                    <label className="block space-y-2">
                      <span className="text-sm font-medium">
                        <I18nText text={'Client ID'} />
                      </span>
                      <I18nProps>
                        <Input
                          value={smtpDraft.clientId}
                          placeholder="Google OAuth client ID"
                          onChange={(event) => {
                            const clientId = event.target.value;
                            setSMTPDraft((current) =>
                              resetOAuthSecretState({ ...current, clientId })
                            );
                          }}
                        />
                      </I18nProps>
                    </label>
                    <label className="block space-y-2">
                      <span className="text-sm font-medium">
                        <I18nText text={'Client secret'} />
                      </span>
                      <I18nProps>
                        <Input
                          type="password"
                          value={smtpDraft.clientSecret}
                          placeholder={
                            smtpDraft.clientSecretConfigured
                              ? 'Client secret configured'
                              : 'Client secret'
                          }
                          onChange={(event) =>
                            setSMTPDraft((current) => ({
                              ...current,
                              clientSecret: event.target.value,
                            }))
                          }
                        />
                      </I18nProps>
                    </label>
                    <label className="block space-y-2">
                      <span className="text-sm font-medium">
                        <I18nText text={'Refresh token'} />
                      </span>
                      <I18nProps>
                        <Input
                          type="password"
                          value={smtpDraft.refreshToken}
                          placeholder={
                            smtpDraft.refreshTokenConfigured
                              ? 'Refresh token configured'
                              : 'Refresh token'
                          }
                          onChange={(event) =>
                            setSMTPDraft((current) => ({
                              ...current,
                              refreshToken: event.target.value,
                            }))
                          }
                        />
                      </I18nProps>
                    </label>
                    <p className="text-xs text-muted-foreground">
                      <I18nText
                        text={
                          'The refresh token must have been granted with offline access and the https://mail.google.com/ scope.'
                        }
                      />
                    </p>
                  </>
                )}
            </fieldset>
            <DialogFooter className="gap-2 border-t border-border px-6 py-4">
              <Button
                type="button"
                variant="outline"
                disabled={isSavingSettings}
                onClick={() => setSettingsOpen(false)}
              >
                <I18nText text={'Cancel'} />
              </Button>
              <Button
                type="submit"
                variant="primary"
                disabled={isSavingSettings}
              >
                {isSavingSettings && (
                  <Loader2 className="size-4 animate-spin" />
                )}
                <I18nText text={'Save changes'} />
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
