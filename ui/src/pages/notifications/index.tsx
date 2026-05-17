import {
  AlertTriangle,
  Building2,
  CheckCircle2,
  Globe2,
  Info,
  Loader2,
  Mail,
  Plus,
  Route as RouteIcon,
  Save,
  Trash2,
} from 'lucide-react';
import {
  type ReactElement,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { Input } from '@/components/ui/input';
import Title from '@/components/ui/title';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useLicense } from '@/hooks/useLicense';
import { cn } from '@/lib/utils';
import { WorkspaceKind, workspaceNameForSelection } from '@/lib/workspace';
import {
  NotificationChannelsSection,
  NotificationChannelsUnavailableCard,
} from '@/features/dags/components/dag-details/notifications/NotificationSections';
import {
  authHeaders,
  blankChannel,
  channelInput,
  deliveryLabel,
  DraftChannel,
  draftChannelFromAPI,
  EVENT_OPTIONS,
  NotificationChannel,
  providerIcon,
  providerLabel,
  readError,
} from '@/features/dags/components/dag-details/notifications/notificationDrafts';
import {
  components,
  NotificationEventType,
  NotificationProviderType,
} from '@/api/v1/schema';
import { Link } from 'react-router-dom';

type NotificationWorkspaceSettings =
  components['schemas']['NotificationWorkspaceSettings'];
type NotificationRouteSet = components['schemas']['NotificationRouteSet'];
type NotificationRouteSetInput =
  components['schemas']['NotificationRouteSetInput'];

type SMTPDraft = {
  host: string;
  port: string;
  username: string;
  password: string;
  from: string;
  passwordConfigured: boolean;
  clearPassword: boolean;
};

const blankSMTPDraft: SMTPDraft = {
  host: '',
  port: '',
  username: '',
  password: '',
  from: '',
  passwordConfigured: false,
  clearPassword: false,
};

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

function routeEventsForDisplay(route: DraftRoute): NotificationEventType[] {
  return route.events.length > 0 ? route.events : DEFAULT_ROUTE_EVENTS;
}

function smtpDraftFromAPI(settings: NotificationWorkspaceSettings): SMTPDraft {
  const smtp = settings.smtp;
  if (!smtp) {
    return { ...blankSMTPDraft };
  }
  return {
    host: smtp.host || '',
    port: smtp.port || '',
    username: smtp.username || '',
    password: '',
    from: smtp.from || '',
    passwordConfigured: !!smtp.passwordConfigured,
    clearPassword: false,
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
      events: route.events || [],
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
      events: routeEventsForDisplay(route),
    })),
  };
}

function blankRoute(
  channels: DraftChannel[],
  usedChannelIds = new Set<string>()
): DraftRoute {
  return {
    channelId:
      channels.find((channel) => channel.id && !usedChannelIds.has(channel.id))
        ?.id || '',
    enabled: true,
    events: [...DEFAULT_ROUTE_EVENTS],
  };
}

function smtpInput(draft: SMTPDraft) {
  const hasSMTP =
    draft.host.trim() ||
    draft.port.trim() ||
    draft.username.trim() ||
    draft.password.trim() ||
    draft.from.trim() ||
    draft.clearPassword;
  if (!hasSMTP) {
    return { smtp: null };
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

function NotificationHomeSectionLinks({
  section,
}: {
  section: NotificationHomeSection;
}): ReactElement {
  return (
    <section className="space-y-2">
      <h3 className="text-xs font-semibold uppercase text-muted-foreground">
        {section.title}
      </h3>
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
            'Manage Slack, email, webhook, and Telegram destinations.',
        },
      ],
    },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <Title>Notifications</Title>

      {sections.map((section) => (
        <NotificationHomeSectionLinks key={section.title} section={section} />
      ))}
    </div>
  );
}

function eventLabel(value: NotificationEventType): string {
  return (
    EVENT_OPTIONS.find((event) => event.value === value)?.label || String(value)
  );
}

function eventChipClass(
  value: NotificationEventType,
  checked: boolean
): string {
  if (!checked) {
    return 'border-border bg-transparent text-muted-foreground';
  }

  switch (value) {
    case NotificationEventType.dag_run_succeeded:
      return 'status-success';
    case NotificationEventType.dag_run_failed:
      return 'status-failed';
    case NotificationEventType.dag_run_aborted:
    case NotificationEventType.dag_run_rejected:
      return 'status-aborted';
    case NotificationEventType.dag_run_waiting:
      return 'status-running';
    default:
      return 'status-neutral';
  }
}

function channelLabel(channel?: DraftChannel, fallback?: string): string {
  if (channel?.name) {
    return channel.name;
  }
  if (channel?.type) {
    return providerLabel(channel.type);
  }
  return fallback || 'Missing channel';
}

function routeStateLabel(route: DraftRoute, channel?: DraftChannel): string {
  if (!route.enabled) return 'Route off';
  if (!channel) return 'Missing channel';
  if (!channel.enabled) return 'Channel off';
  return 'On';
}

function hasUnusedChannel(
  channels: DraftChannel[],
  routes: DraftRoute[]
): boolean {
  const usedChannelIds = new Set(routes.map((route) => route.channelId));
  return channels.some(
    (channel) => channel.id && !usedChannelIds.has(channel.id)
  );
}

type NotificationRulesHeaderProps = {
  canAddRoute: boolean;
  saving: boolean;
  onAddRoute: () => void;
  onSave: () => void;
};

function NotificationRulesHeader({
  canAddRoute,
  saving,
  onAddRoute,
  onSave,
}: NotificationRulesHeaderProps) {
  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-normal text-foreground">
            Notification Rules
          </h1>
          <p className="text-sm text-muted-foreground">
            Global rules apply by default. Workspace and DAG settings override
            them only when configured.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={onAddRoute}
            disabled={!canAddRoute}
          >
            <Plus className="h-4 w-4" />
            Add route
          </Button>
          <Button size="sm" onClick={onSave} disabled={saving}>
            {saving ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Save className="h-4 w-4" />
            )}
            Save changes
          </Button>
        </div>
      </div>

      <div className="flex items-center gap-1 border-b border-border">
        <span className="inline-flex h-10 items-center gap-2 border-b-2 border-primary px-3 text-sm font-medium text-foreground">
          <RouteIcon className="h-4 w-4 text-primary" />
          Rules
        </span>
        <Link
          to="/notification-channels"
          className="inline-flex h-10 items-center gap-2 border-b-2 border-transparent px-3 text-sm font-medium text-muted-foreground hover:text-foreground"
        >
          <Mail className="h-4 w-4" />
          Channels
        </Link>
      </div>
    </div>
  );
}

type ScopeSelectorProps = {
  activeScope: RouteScopeKey;
  workspaceName: string;
  canConfigureWorkspaceRoutes: boolean;
  globalRoutes: DraftRouteSet;
  workspaceRoutes: DraftRouteSet;
  onChange: (scope: RouteScopeKey) => void;
};

function ScopeSelector({
  activeScope,
  workspaceName,
  canConfigureWorkspaceRoutes,
  globalRoutes,
  workspaceRoutes,
  onChange,
}: ScopeSelectorProps) {
  return (
    <Card className="self-start">
      <CardHeader>
        <CardTitle className="text-sm">1. Scope</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <button
          type="button"
          onClick={() => onChange('global')}
          className={cn(
            'w-full rounded-md border px-3 py-3 text-left transition-colors',
            activeScope === 'global'
              ? 'border-primary bg-primary/10'
              : 'border-border hover:bg-muted'
          )}
        >
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 font-medium">
              <Globe2 className="h-4 w-4 text-muted-foreground" />
              Global
            </div>
            <Badge variant={globalRoutes.enabled ? 'success' : 'default'}>
              {globalRoutes.routes.length}
            </Badge>
          </div>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            Default for every DAG unless a workspace or DAG is configured.
          </p>
        </button>

        <button
          type="button"
          onClick={() => canConfigureWorkspaceRoutes && onChange('workspace')}
          disabled={!canConfigureWorkspaceRoutes}
          className={cn(
            'w-full rounded-md border px-3 py-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60',
            activeScope === 'workspace'
              ? 'border-primary bg-primary/10'
              : 'border-border hover:bg-muted'
          )}
        >
          <div className="flex items-center justify-between gap-3">
            <div className="flex min-w-0 items-center gap-2 font-medium">
              <Building2 className="h-4 w-4 shrink-0 text-muted-foreground" />
              <span className="truncate">
                {workspaceName
                  ? `${workspaceName} workspace`
                  : 'This workspace'}
              </span>
            </div>
            <Badge
              variant={
                canConfigureWorkspaceRoutes &&
                !workspaceRoutes.inheritGlobal &&
                workspaceRoutes.enabled
                  ? 'success'
                  : 'default'
              }
            >
              {canConfigureWorkspaceRoutes
                ? workspaceRoutes.inheritGlobal
                  ? 'Inherit'
                  : workspaceRoutes.routes.length
                : '-'}
            </Badge>
          </div>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            {canConfigureWorkspaceRoutes
              ? workspaceRoutes.inheritGlobal
                ? 'Uses Global until configured.'
                : `Overrides Global for ${workspaceName}.`
              : 'Select a workspace to configure this.'}
          </p>
        </button>
      </CardContent>
    </Card>
  );
}

type RouteBuilderProps = {
  title: string;
  description: string;
  draft: DraftRouteSet;
  channels: DraftChannel[];
  disabled?: boolean;
  showWorkspaceInclude?: boolean;
  channelsHref: string;
  emptyText: string;
  onAddRoute: () => void;
  onChange: (updater: (current: DraftRouteSet) => DraftRouteSet) => void;
};

function RouteBuilder({
  title,
  description,
  draft,
  channels,
  disabled = false,
  showWorkspaceInclude = false,
  channelsHref,
  emptyText,
  onAddRoute,
  onChange,
}: RouteBuilderProps) {
  const availableChannels = channels.filter((channel) => channel.id);
  const isWorkspaceInheritMode = showWorkspaceInclude && draft.inheritGlobal;
  const routeControlsDisabled = disabled || isWorkspaceInheritMode;
  const canAddRoute =
    availableChannels.length > 0 &&
    !isWorkspaceInheritMode &&
    hasUnusedChannel(availableChannels, draft.routes);
  const updateRoute = (
    index: number,
    updater: (route: DraftRoute) => DraftRoute
  ) =>
    onChange((current) => ({
      ...current,
      routes: current.routes.map((route, routeIndex) =>
        routeIndex === index ? updater(route) : route
      ),
    }));
  const deleteRoute = (index: number) =>
    onChange((current) => ({
      ...current,
      routes: current.routes.filter((_, routeIndex) => routeIndex !== index),
    }));

  return (
    <Card className="min-w-0">
      <CardHeader className="grid-cols-[1fr_auto]">
        <div className="min-w-0 space-y-1">
          <CardTitle className="truncate text-sm">
            2. Send notifications
          </CardTitle>
          <p className="text-xs text-muted-foreground">
            {title}. {description}
          </p>
        </div>
        {!isWorkspaceInheritMode && (
          <label className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">Enabled</span>
            <Switch
              checked={draft.enabled}
              disabled={disabled}
              onCheckedChange={(enabled) =>
                onChange((current) => ({ ...current, enabled }))
              }
              aria-label={`Toggle ${title}`}
            />
          </label>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {showWorkspaceInclude && (
          <div className="grid gap-2 sm:grid-cols-2">
            <button
              type="button"
              disabled={disabled}
              onClick={() =>
                onChange((current) => ({
                  ...current,
                  inheritGlobal: true,
                }))
              }
              className={cn(
                'rounded-md border px-3 py-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60',
                draft.inheritGlobal
                  ? 'border-primary bg-primary/10'
                  : 'border-border hover:bg-muted'
              )}
            >
              <span className="block text-sm font-medium">
                Inherit Global
              </span>
              <span className="mt-1 block text-xs text-muted-foreground">
                Use the Global rules for this workspace.
              </span>
            </button>
            <button
              type="button"
              disabled={disabled}
              onClick={() =>
                onChange((current) => ({
                  ...current,
                  inheritGlobal: false,
                }))
              }
              className={cn(
                'rounded-md border px-3 py-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60',
                !draft.inheritGlobal
                  ? 'border-primary bg-primary/10'
                  : 'border-border hover:bg-muted'
              )}
            >
              <span className="block text-sm font-medium">
                Configure Workspace
              </span>
              <span className="mt-1 block text-xs text-muted-foreground">
                Override Global with workspace-specific rules.
              </span>
            </button>
          </div>
        )}

        {isWorkspaceInheritMode ? (
          <div className="rounded-md border border-border bg-muted/30 px-3 py-4 text-sm text-muted-foreground">
            This workspace currently inherits Global rules. Workspace routes are
            ignored until Configure Workspace is selected.
          </div>
        ) : availableChannels.length === 0 ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border px-3 py-4 text-sm text-muted-foreground">
            <span>Create a channel before adding notification routes.</span>
            <Button asChild variant="ghost" size="sm">
              <Link to={channelsHref}>Manage channels</Link>
            </Button>
          </div>
        ) : draft.routes.length === 0 ? (
          <div className="rounded-md border border-border px-3 py-4 text-sm text-muted-foreground">
            {emptyText}
          </div>
        ) : (
          <div className="rounded-md border border-border">
            <div className="divide-y divide-border">
              {draft.routes.map((route, index) => (
                <RouteRuleRow
                  key={route.id || `${route.channelId}-${index}`}
                  route={route}
                  index={index}
                  routes={draft.routes}
                  channels={availableChannels}
                  disabled={routeControlsDisabled}
                  onUpdate={updateRoute}
                  onDelete={deleteRoute}
                />
              ))}
            </div>
          </div>
        )}

        {!isWorkspaceInheritMode && availableChannels.length > 0 && (
          <button
            type="button"
            onClick={onAddRoute}
            disabled={routeControlsDisabled || !canAddRoute}
            className="flex h-10 w-full items-center justify-center gap-2 rounded-md border border-dashed border-border text-sm text-primary transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:text-muted-foreground"
          >
            <Plus className="h-4 w-4" />
            Add another route
          </button>
        )}
      </CardContent>
    </Card>
  );
}

type RouteRuleRowProps = {
  route: DraftRoute;
  index: number;
  routes: DraftRoute[];
  channels: DraftChannel[];
  disabled: boolean;
  onUpdate: (index: number, updater: (route: DraftRoute) => DraftRoute) => void;
  onDelete: (index: number) => void;
};

function RouteRuleRow({
  route,
  index,
  routes,
  channels,
  disabled,
  onUpdate,
  onDelete,
}: RouteRuleRowProps) {
  const channel = channels.find((item) => item.id === route.channelId);
  const Icon = providerIcon(channel?.type);
  const effectiveEvents = routeEventsForDisplay(route);
  const usesOperationalEvents = sameEvents(
    effectiveEvents,
    DEFAULT_ROUTE_EVENTS
  );
  const usedChannelIds = new Set(
    routes
      .filter((_, routeIndex) => routeIndex !== index)
      .map((item) => item.channelId)
  );

  return (
    <div className="grid gap-3 px-3 py-3 2xl:grid-cols-[minmax(280px,1fr)_minmax(220px,280px)_auto] 2xl:items-center">
      <div className="min-w-0 space-y-2">
        <div className="text-xs font-medium text-muted-foreground">
          When any selected event happens
        </div>
        <div className="flex flex-wrap gap-2">
          {EVENT_OPTIONS.map((event) => {
            const checked = effectiveEvents.includes(event.value);
            return (
              <label
                key={event.value}
                className={cn(
                  'flex h-8 items-center gap-2 rounded-md border px-3 text-xs',
                  eventChipClass(event.value, checked)
                )}
              >
                <Checkbox
                  checked={checked}
                  disabled={
                    disabled || (checked && effectiveEvents.length === 1)
                  }
                  onCheckedChange={(value) =>
                    onUpdate(index, (current) => {
                      const currentEvents = routeEventsForDisplay(current);
                      const nextEvents = value
                        ? [...currentEvents, event.value]
                        : currentEvents.filter((item) => item !== event.value);
                      return {
                        ...current,
                        events: nextEvents,
                      };
                    })
                  }
                />
                {event.label}
              </label>
            );
          })}
          {!usesOperationalEvents && (
            <Button
              variant="ghost"
              size="sm"
              disabled={disabled}
              onClick={() =>
                onUpdate(index, (current) => ({
                  ...current,
                  events: [...DEFAULT_ROUTE_EVENTS],
                }))
              }
            >
              Use operational events
            </Button>
          )}
        </div>
      </div>

      <div className="min-w-0 space-y-2">
        <div className="text-xs font-medium text-muted-foreground">Send to</div>
        <Select
          value={route.channelId}
          disabled={disabled}
          onValueChange={(channelId) =>
            onUpdate(index, (current) => ({
              ...current,
              channelId,
            }))
          }
        >
          <SelectTrigger>
            <SelectValue placeholder="Select channel" />
          </SelectTrigger>
          <SelectContent>
            {channels.map((item) => (
              <SelectItem
                key={item.id}
                value={item.id || ''}
                disabled={!!item.id && usedChannelIds.has(item.id)}
              >
                {item.name || providerLabel(item.type)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="flex items-center gap-2 2xl:justify-end">
        <div className="hidden min-w-0 items-center gap-2 text-sm text-muted-foreground 2xl:flex">
          <Icon className="h-4 w-4 shrink-0" />
          <span className="max-w-40 truncate">
            {channelLabel(channel, route.channelId)}
          </span>
        </div>
        <Badge
          variant={route.enabled && channel?.enabled ? 'success' : 'default'}
        >
          {routeStateLabel(route, channel)}
        </Badge>
        <Switch
          checked={route.enabled}
          disabled={disabled}
          onCheckedChange={(enabled) =>
            onUpdate(index, (current) => ({
              ...current,
              enabled,
            }))
          }
          aria-label={`Toggle ${channelLabel(channel, route.channelId)}`}
        />
        <Button
          variant="ghost"
          size="sm"
          disabled={disabled}
          onClick={() => onDelete(index)}
          aria-label={`Delete ${channelLabel(channel, route.channelId)}`}
        >
          <Trash2 className="h-4 w-4 text-destructive" />
        </Button>
      </div>
    </div>
  );
}

type RoutePreviewPanelProps = {
  scopeLabel: string;
  draft: DraftRouteSet;
  channels: DraftChannel[];
};

function RoutePreviewPanel({
  scopeLabel,
  draft,
  channels,
}: RoutePreviewPanelProps) {
  const previews = draft.routes
    .filter((route) => route.enabled)
    .flatMap((route) => {
      const channel = channels.find((item) => item.id === route.channelId);
      if (!channel?.enabled) {
        return [];
      }
      return routeEventsForDisplay(route).map((event) => ({
        id: `${route.id || route.channelId}-${event}`,
        event,
        channel,
      }));
    })
    .slice(0, 6);

  return (
    <Card className="self-start">
      <CardHeader>
        <CardTitle className="text-sm">3. Effective rules</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {!draft.enabled ? (
          <p className="text-sm text-muted-foreground">
            Routes are disabled for this scope.
          </p>
        ) : previews.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No enabled route currently sends notifications.
          </p>
        ) : (
          <div className="divide-y divide-border rounded-md border border-border">
            {previews.map((item) => {
              const Icon = providerIcon(item.channel.type);
              return (
                <div key={item.id} className="space-y-1 px-3 py-2 text-sm">
                  <div className="flex items-center gap-2">
                    <span
                      className={cn(
                        'rounded-md border px-2 py-0.5 text-xs',
                        eventChipClass(item.event, true)
                      )}
                    >
                      {eventLabel(item.event)}
                    </span>
                    <span className="text-muted-foreground">
                      from {scopeLabel}
                    </span>
                  </div>
                  <div className="flex min-w-0 items-center gap-2 text-muted-foreground">
                    <RouteIcon className="h-3.5 w-3.5 shrink-0" />
                    <span>send to</span>
                    <Icon className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate text-foreground">
                      {channelLabel(item.channel)}
                    </span>
                  </div>
                </div>
              );
            })}
          </div>
        )}
        <div className="flex items-start gap-2 border-t border-border pt-3 text-xs text-muted-foreground">
          <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>
            Dagu uses the most specific configured scope: DAG, then workspace,
            then Global.
          </span>
        </div>
      </CardContent>
    </Card>
  );
}

function ChannelHelpPanel() {
  return (
    <Card>
      <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
            <Info className="h-4 w-4" />
          </div>
          <div className="space-y-1">
            <div className="text-sm font-medium">
              Need a new Slack, email, webhook, or Telegram destination?
            </div>
            <div className="text-sm text-muted-foreground">
              Create and test notification channels before using them in routes.
            </div>
          </div>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link to="/notification-channels">
            <Mail className="h-4 w-4" />
            Manage channels
          </Link>
        </Button>
      </CardContent>
    </Card>
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
          <span>{error}</span>
        </div>
      )}
      {notice && (
        <div className="flex items-start gap-2 rounded-md border border-success/30 bg-success/10 p-3 text-sm text-success">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{notice}</span>
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

export function NotificationRulesPage() {
  const config = useConfig();
  const license = useLicense();
  const appBarContext = useContext(AppBarContext);
  const reusableChannelsLicensed =
    !license.community && (license.valid || license.gracePeriod);
  const workspaceSelection = appBarContext.workspaceSelection;
  const selectedWorkspaceName = workspaceNameForSelection(workspaceSelection);
  const canConfigureWorkspaceRoutes =
    workspaceSelection?.kind === WorkspaceKind.workspace &&
    !!selectedWorkspaceName;
  const query = useMemo(
    () =>
      `?remoteNode=${encodeURIComponent(appBarContext.selectedRemoteNode || 'local')}`,
    [appBarContext.selectedRemoteNode]
  );
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
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [activeScope, setActiveScope] = useState<RouteScopeKey>('global');

  useEffect(() => {
    appBarContext.setTitle('Notification Rules');
  }, [appBarContext]);

  useEffect(() => {
    if (!canConfigureWorkspaceRoutes && activeScope === 'workspace') {
      setActiveScope('global');
    }
  }, [activeScope, canConfigureWorkspaceRoutes]);

  const fetchRules = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      if (!reusableChannelsLicensed) {
        setChannels([]);
        setGlobalRoutes({ ...blankRouteSet, routes: [] });
        setWorkspaceRoutes({ ...blankRouteSet, routes: [] });
        return;
      }
      const [response, globalRoutesResponse, workspaceRoutesResponse] =
        await Promise.all([
          fetch(`${config.apiURL}/notification-channels${query}`, {
            headers: authHeaders(),
          }),
          fetch(`${config.apiURL}/notification-routes/global${query}`, {
            headers: authHeaders(),
          }),
          canConfigureWorkspaceRoutes
            ? fetch(
                `${config.apiURL}/notification-routes/workspaces/${encodeURIComponent(selectedWorkspaceName)}${query}`,
                { headers: authHeaders() }
              )
            : Promise.resolve(null),
        ]);
      if (!response.ok) {
        throw new Error(await readError(response, 'Failed to load channels'));
      }
      if (!globalRoutesResponse.ok) {
        throw new Error(
          await readError(
            globalRoutesResponse,
            'Failed to load Global rules'
          )
        );
      }
      const data = (await response.json()) as {
        channels: NotificationChannel[];
      };
      setChannels((data.channels || []).map(draftChannelFromAPI));
      const globalRouteSet =
        (await globalRoutesResponse.json()) as NotificationRouteSet;
      setGlobalRoutes(routeSetDraftFromAPI(globalRouteSet));
      if (workspaceRoutesResponse) {
        if (!workspaceRoutesResponse.ok) {
          throw new Error(
            await readError(
              workspaceRoutesResponse,
              'Failed to load workspace rules'
            )
          );
        }
        const workspaceRouteSet =
          (await workspaceRoutesResponse.json()) as NotificationRouteSet;
        setWorkspaceRoutes(routeSetDraftFromAPI(workspaceRouteSet));
      } else {
        setWorkspaceRoutes({ ...blankRouteSet, routes: [] });
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load rules');
    } finally {
      setIsLoading(false);
    }
  }, [
    canConfigureWorkspaceRoutes,
    config.apiURL,
    query,
    reusableChannelsLicensed,
    selectedWorkspaceName,
  ]);

  useEffect(() => {
    fetchRules();
  }, [fetchRules]);

  const saveGlobalRoutes = async () => {
    if (!reusableChannelsLicensed) return;
    setIsSavingGlobalRoutes(true);
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        `${config.apiURL}/notification-routes/global${query}`,
        {
          method: 'PUT',
          headers: authHeaders(),
          body: JSON.stringify(routeSetInput(globalRoutes)),
        }
      );
      if (!response.ok) {
        throw new Error(
          await readError(response, 'Failed to save Global rules')
        );
      }
      const routeSet = (await response.json()) as NotificationRouteSet;
      setGlobalRoutes(routeSetDraftFromAPI(routeSet));
      setNotice('Global rules saved');
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : 'Failed to save Global rules'
      );
    } finally {
      setIsSavingGlobalRoutes(false);
    }
  };

  const saveWorkspaceRoutes = async () => {
    if (!reusableChannelsLicensed || !canConfigureWorkspaceRoutes) return;
    setIsSavingWorkspaceRoutes(true);
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        `${config.apiURL}/notification-routes/workspaces/${encodeURIComponent(selectedWorkspaceName)}${query}`,
        {
          method: 'PUT',
          headers: authHeaders(),
          body: JSON.stringify(routeSetInput(workspaceRoutes)),
        }
      );
      if (!response.ok) {
        throw new Error(
          await readError(response, 'Failed to save workspace rules')
        );
      }
      const routeSet = (await response.json()) as NotificationRouteSet;
      setWorkspaceRoutes(routeSetDraftFromAPI(routeSet));
      setNotice(
        routeSet.inheritGlobal
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

  const activeDraft =
    activeScope === 'workspace' && canConfigureWorkspaceRoutes
      ? workspaceRoutes
      : globalRoutes;
  const activeTitle =
    activeScope === 'workspace' && canConfigureWorkspaceRoutes
      ? `${selectedWorkspaceName} workspace`
      : 'Global';
  const activeDescription =
    activeScope === 'workspace' && canConfigureWorkspaceRoutes
      ? workspaceRoutes.inheritGlobal
        ? 'This workspace currently inherits Global rules.'
        : 'Overrides Global for DAGs in this workspace.'
      : 'Default for every DAG unless workspace or DAG settings are configured.';
  const activeSaving =
    activeScope === 'workspace' && canConfigureWorkspaceRoutes
      ? isSavingWorkspaceRoutes
      : isSavingGlobalRoutes;
  const activeEmptyText =
    activeScope === 'workspace' && canConfigureWorkspaceRoutes
      ? 'This workspace override has no routes, so DAGs here will not notify unless a DAG is configured.'
      : 'Global has no routes. DAGs will not notify unless a workspace or DAG is configured.';
  const activeChannels = channels.filter((channel) => channel.id);
  const activeWorkspaceInheritsGlobal =
    activeScope === 'workspace' &&
    canConfigureWorkspaceRoutes &&
    workspaceRoutes.inheritGlobal;
  const previewDraft = activeWorkspaceInheritsGlobal
    ? globalRoutes
    : activeDraft;
  const previewScopeLabel = activeWorkspaceInheritsGlobal
    ? 'Global'
    : activeTitle;
  const canAddActiveRoute =
    reusableChannelsLicensed &&
    activeChannels.length > 0 &&
    !activeWorkspaceInheritsGlobal &&
    hasUnusedChannel(activeChannels, activeDraft.routes);
  const updateActiveRoutes = (
    updater: (current: DraftRouteSet) => DraftRouteSet
  ) => {
    if (activeScope === 'workspace' && canConfigureWorkspaceRoutes) {
      setWorkspaceRoutes((current) => updater(current));
      return;
    }
    setGlobalRoutes((current) => updater(current));
  };
  const addActiveRoute = () => {
    updateActiveRoutes((current) => {
      const usedChannelIds = new Set(
        current.routes.map((route) => route.channelId)
      );
      return {
        ...current,
        routes: [...current.routes, blankRoute(activeChannels, usedChannelIds)],
      };
    });
  };
  const saveActiveRoutes =
    activeScope === 'workspace' && canConfigureWorkspaceRoutes
      ? saveWorkspaceRoutes
      : saveGlobalRoutes;

  if (isLoading) {
    return <LoadingCard label="Loading notification rules..." />;
  }

  return (
    <div className="space-y-4">
      <StatusCard error={error} notice={notice} />

      {reusableChannelsLicensed ? (
        <>
          <NotificationRulesHeader
            canAddRoute={canAddActiveRoute}
            saving={activeSaving}
            onAddRoute={addActiveRoute}
            onSave={saveActiveRoutes}
          />

          <div className="grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)_320px]">
            <ScopeSelector
              activeScope={activeScope}
              workspaceName={selectedWorkspaceName}
              canConfigureWorkspaceRoutes={canConfigureWorkspaceRoutes}
              globalRoutes={globalRoutes}
              workspaceRoutes={workspaceRoutes}
              onChange={setActiveScope}
            />

            <RouteBuilder
              title={activeTitle}
              description={activeDescription}
              draft={activeDraft}
              channels={channels}
              channelsHref="/notification-channels"
              showWorkspaceInclude={
                activeScope === 'workspace' && canConfigureWorkspaceRoutes
              }
              emptyText={activeEmptyText}
              onAddRoute={addActiveRoute}
              onChange={updateActiveRoutes}
            />

            <RoutePreviewPanel
              scopeLabel={previewScopeLabel}
              draft={previewDraft}
              channels={channels}
            />
          </div>

          <ChannelHelpPanel />
        </>
      ) : (
        <NotificationChannelsUnavailableCard showDAGLocalNote={false} />
      )}
    </div>
  );
}

export function NotificationChannelsPage() {
  const config = useConfig();
  const license = useLicense();
  const appBarContext = useContext(AppBarContext);
  const reusableChannelsLicensed =
    !license.community && (license.valid || license.gracePeriod);
  const query = useMemo(
    () =>
      `?remoteNode=${encodeURIComponent(appBarContext.selectedRemoteNode || 'local')}`,
    [appBarContext.selectedRemoteNode]
  );
  const [smtpDraft, setSMTPDraft] = useState<SMTPDraft>(blankSMTPDraft);
  const [isSavingSettings, setIsSavingSettings] = useState(false);
  const [channels, setChannels] = useState<DraftChannel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [savingChannelIndex, setSavingChannelIndex] = useState<number | null>(
    null
  );
  const [deleteChannelIndex, setDeleteChannelIndex] = useState<number | null>(
    null
  );
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    appBarContext.setTitle('Notification Channels');
  }, [appBarContext]);

  const fetchSettings = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const settingsResponse = await fetch(
        `${config.apiURL}/notification-settings${query}`,
        { headers: authHeaders() }
      );
      if (!settingsResponse.ok) {
        throw new Error(
          await readError(settingsResponse, 'Failed to load email delivery')
        );
      }
      const settings =
        (await settingsResponse.json()) as NotificationWorkspaceSettings;
      setSMTPDraft(smtpDraftFromAPI(settings));

      if (!reusableChannelsLicensed) {
        setChannels([]);
        return;
      }

      const response = await fetch(
        `${config.apiURL}/notification-channels${query}`,
        {
          headers: authHeaders(),
        }
      );
      if (!response.ok) {
        throw new Error(await readError(response, 'Failed to load channels'));
      }
      const data = (await response.json()) as {
        channels: NotificationChannel[];
      };
      setChannels((data.channels || []).map(draftChannelFromAPI));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load channels');
    } finally {
      setIsLoading(false);
    }
  }, [config.apiURL, query, reusableChannelsLicensed]);

  useEffect(() => {
    fetchSettings();
  }, [fetchSettings]);

  const saveSettings = async () => {
    setIsSavingSettings(true);
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        `${config.apiURL}/notification-settings${query}`,
        {
          method: 'PUT',
          headers: authHeaders(),
          body: JSON.stringify(smtpInput(smtpDraft)),
        }
      );
      if (!response.ok) {
        throw new Error(
          await readError(response, 'Failed to save email delivery')
        );
      }
      const settings = (await response.json()) as NotificationWorkspaceSettings;
      setSMTPDraft(smtpDraftFromAPI(settings));
      setNotice('Email delivery saved');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save email delivery'
      );
    } finally {
      setIsSavingSettings(false);
    }
  };

  const addChannel = () => {
    if (!reusableChannelsLicensed) return;
    setChannels((current) => [
      ...current,
      blankChannel(NotificationProviderType.email),
    ]);
  };

  const updateChannel = (
    index: number,
    updater: (channel: DraftChannel) => DraftChannel
  ) => {
    setChannels((current) =>
      current.map((channel, channelIndex) =>
        channelIndex === index ? updater(channel) : channel
      )
    );
  };

  const saveChannel = async (index: number) => {
    if (!reusableChannelsLicensed) return;
    const channel = channels[index];
    if (!channel) return;
    setSavingChannelIndex(index);
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        channel.id
          ? `${config.apiURL}/notification-channels/${encodeURIComponent(channel.id)}${query}`
          : `${config.apiURL}/notification-channels${query}`,
        {
          method: channel.id ? 'PUT' : 'POST',
          headers: authHeaders(),
          body: JSON.stringify(channelInput(channel)),
        }
      );
      if (!response.ok) {
        throw new Error(await readError(response, 'Failed to save channel'));
      }
      const data = (await response.json()) as NotificationChannel;
      setChannels((current) =>
        current.map((item, itemIndex) =>
          itemIndex === index ? draftChannelFromAPI(data) : item
        )
      );
      setNotice('Channel saved');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save channel');
    } finally {
      setSavingChannelIndex(null);
    }
  };

  const deleteChannel = async () => {
    if (!reusableChannelsLicensed) return;
    if (deleteChannelIndex === null) return;
    const channel = channels[deleteChannelIndex];
    if (!channel) return;
    setDeleteChannelIndex(null);
    if (!channel.id) {
      setChannels((current) =>
        current.filter((_, index) => index !== deleteChannelIndex)
      );
      return;
    }
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        `${config.apiURL}/notification-channels/${encodeURIComponent(channel.id)}${query}`,
        {
          method: 'DELETE',
          headers: authHeaders(),
        }
      );
      if (!response.ok) {
        throw new Error(await readError(response, 'Failed to delete channel'));
      }
      setChannels((current) =>
        current.filter((_, index) => index !== deleteChannelIndex)
      );
      setNotice('Channel deleted');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete channel');
    }
  };

  if (isLoading) {
    return <LoadingCard label="Loading notification channels..." />;
  }

  return (
    <div className="space-y-4">
      <StatusCard error={error} notice={notice} />

      <Card>
        <CardHeader className="grid-cols-[1fr_auto]">
          <div className="flex items-center gap-2">
            <Mail className="h-4 w-4 text-muted-foreground" />
            <CardTitle className="text-sm">Email Delivery</CardTitle>
            <Badge variant={smtpDraft.host ? 'success' : 'default'}>
              {smtpDraft.host ? 'Configured' : 'Not Configured'}
            </Badge>
          </div>
          <Button size="sm" onClick={saveSettings} disabled={isSavingSettings}>
            {isSavingSettings ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Save className="h-4 w-4" />
            )}
            Save
          </Button>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_120px]">
            <Input
              value={smtpDraft.host}
              placeholder="SMTP host"
              onChange={(event) =>
                setSMTPDraft((current) => ({
                  ...current,
                  host: event.target.value,
                }))
              }
            />
            <Input
              value={smtpDraft.port}
              placeholder="Port"
              inputMode="numeric"
              onChange={(event) =>
                setSMTPDraft((current) => ({
                  ...current,
                  port: event.target.value,
                }))
              }
            />
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <Input
              value={smtpDraft.username}
              placeholder="Username"
              onChange={(event) =>
                setSMTPDraft((current) => ({
                  ...current,
                  username: event.target.value,
                }))
              }
            />
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
          </div>
          <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px]">
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
            <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
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
              Clear password
            </label>
          </div>
        </CardContent>
      </Card>

      {reusableChannelsLicensed ? (
        <NotificationChannelsSection
          channels={channels}
          savingChannelIndex={savingChannelIndex}
          onAdd={addChannel}
          onUpdate={updateChannel}
          onSave={saveChannel}
          onDelete={setDeleteChannelIndex}
        />
      ) : (
        <NotificationChannelsUnavailableCard showDAGLocalNote={false} />
      )}

      <ConfirmDialog
        title="Delete Channel"
        buttonText="Delete"
        visible={deleteChannelIndex !== null}
        dismissModal={() => setDeleteChannelIndex(null)}
        onSubmit={deleteChannel}
      >
        Delete{' '}
        {deleteChannelIndex !== null && channels[deleteChannelIndex]
          ? deliveryLabel(channels[deleteChannelIndex])
          : 'channel'}
        ?
      </ConfirmDialog>
    </div>
  );
}
