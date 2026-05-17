import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  Loader2,
  Mail,
  Plus,
  Route as RouteIcon,
  Save,
  Trash2,
} from 'lucide-react';
import { useCallback, useContext, useEffect, useMemo, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { Input } from '@/components/ui/input';
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
import {
  WorkspaceKind,
  workspaceNameForSelection,
} from '@/lib/workspace';
import {
  ReusableChannelsSection,
  ReusableChannelsUnavailableCard,
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

const blankRouteSet: DraftRouteSet = {
  enabled: true,
  inheritGlobal: true,
  routes: [],
};

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
      events: route.events.length > 0 ? route.events : undefined,
    })),
  };
}

function blankRoute(channels: DraftChannel[]): DraftRoute {
  return {
    channelId: channels.find((channel) => channel.id)?.id || '',
    enabled: true,
    events: [],
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

type RouteSetSectionProps = {
  title: string;
  badge: string;
  draft: DraftRouteSet;
  channels: DraftChannel[];
  saving: boolean;
  showInheritGlobal?: boolean;
  disabled?: boolean;
  emptyText: string;
  onSave: () => void;
  onChange: (updater: (current: DraftRouteSet) => DraftRouteSet) => void;
};

function RouteSetSection({
  title,
  badge,
  draft,
  channels,
  saving,
  showInheritGlobal = false,
  disabled = false,
  emptyText,
  onSave,
  onChange,
}: RouteSetSectionProps) {
  const availableChannels = channels.filter((channel) => channel.id);
  const addRoute = () =>
    onChange((current) => ({
      ...current,
      routes: [...current.routes, blankRoute(channels)],
    }));
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
    <Card>
      <CardHeader className="grid-cols-[1fr_auto]">
        <div className="flex min-w-0 items-center gap-2">
          <RouteIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
          <CardTitle className="truncate text-sm">{title}</CardTitle>
          <Badge variant={draft.enabled ? 'success' : 'default'}>{badge}</Badge>
        </div>
        <div className="flex items-center gap-2">
          <Switch
            checked={draft.enabled}
            disabled={disabled}
            onCheckedChange={(enabled) =>
              onChange((current) => ({ ...current, enabled }))
            }
            aria-label={`Toggle ${title}`}
          />
          <Button
            variant="outline"
            size="sm"
            onClick={addRoute}
            disabled={disabled || availableChannels.length === 0}
          >
            <Plus className="h-4 w-4" />
            Add
          </Button>
          <Button size="sm" onClick={onSave} disabled={disabled || saving}>
            {saving ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Save className="h-4 w-4" />
            )}
            Save
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {showInheritGlobal && (
          <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
            <Checkbox
              checked={draft.inheritGlobal}
              disabled={disabled}
              onCheckedChange={(value) =>
                onChange((current) => ({
                  ...current,
                  inheritGlobal: !!value,
                }))
              }
            />
            Inherit global routes
          </label>
        )}

        {availableChannels.length === 0 ? (
          <div className="rounded-md border border-border px-3 py-4 text-sm text-muted-foreground">
            Create a reusable channel before adding routes.
          </div>
        ) : draft.routes.length === 0 ? (
          <div className="rounded-md border border-border px-3 py-4 text-sm text-muted-foreground">
            {emptyText}
          </div>
        ) : (
          <div className="space-y-3">
            {draft.routes.map((route, index) => {
              const channel = channels.find(
                (item) => item.id === route.channelId
              );
              const Icon = providerIcon(channel?.type);
              const usedChannelIds = new Set(
                draft.routes
                  .filter((_, routeIndex) => routeIndex !== index)
                  .map((item) => item.channelId)
              );
              return (
                <div
                  key={route.id || `${route.channelId}-${index}`}
                  className="space-y-3 rounded-md border border-border p-3"
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-2">
                      <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="truncate text-sm font-medium">
                        {channel?.name || route.channelId || 'Route'}
                      </span>
                      <Badge
                        variant={
                          route.enabled && channel?.enabled
                            ? 'success'
                            : 'default'
                        }
                      >
                        {route.enabled && channel?.enabled
                          ? 'Enabled'
                          : 'Disabled'}
                      </Badge>
                      {!channel && route.channelId && (
                        <Badge variant="error">Missing</Badge>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <Switch
                        checked={route.enabled}
                        disabled={disabled}
                        onCheckedChange={(enabled) =>
                          updateRoute(index, (current) => ({
                            ...current,
                            enabled,
                          }))
                        }
                        aria-label={`Toggle ${channel?.name || route.channelId}`}
                      />
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={disabled}
                        onClick={() => deleteRoute(index)}
                        aria-label={`Delete ${channel?.name || route.channelId}`}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </div>
                  </div>

                  <Select
                    value={route.channelId}
                    disabled={disabled}
                    onValueChange={(channelId) =>
                      updateRoute(index, (current) => ({
                        ...current,
                        channelId,
                      }))
                    }
                  >
                    <SelectTrigger>
                      <SelectValue placeholder="Select channel" />
                    </SelectTrigger>
                    <SelectContent>
                      {availableChannels.map((item) => (
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

                  <div className="flex flex-wrap gap-2">
                    {EVENT_OPTIONS.map((event) => {
                      const checked = route.events.includes(event.value);
                      return (
                        <label
                          key={event.value}
                          className="flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs"
                        >
                          <Checkbox
                            checked={checked}
                            disabled={disabled}
                            onCheckedChange={(value) =>
                              updateRoute(index, (current) => ({
                                ...current,
                                events: value
                                  ? [...current.events, event.value]
                                  : current.events.filter(
                                      (item) => item !== event.value
                                    ),
                              }))
                            }
                          />
                          {event.label}
                        </label>
                      );
                    })}
                    {route.events.length > 0 && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={disabled}
                        onClick={() =>
                          updateRoute(index, (current) => ({
                            ...current,
                            events: [],
                          }))
                        }
                      >
                        Default
                      </Button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export default function NotificationsPage() {
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
  const [smtpDraft, setSMTPDraft] = useState<SMTPDraft>(blankSMTPDraft);
  const [globalRoutes, setGlobalRoutes] = useState<DraftRouteSet>({
    ...blankRouteSet,
    routes: [],
  });
  const [workspaceRoutes, setWorkspaceRoutes] = useState<DraftRouteSet>({
    ...blankRouteSet,
    routes: [],
  });
  const [isSavingSettings, setIsSavingSettings] = useState(false);
  const [isSavingGlobalRoutes, setIsSavingGlobalRoutes] = useState(false);
  const [isSavingWorkspaceRoutes, setIsSavingWorkspaceRoutes] = useState(false);
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
    appBarContext.setTitle('Notifications');
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
          await readError(settingsResponse, 'Failed to load settings')
        );
      }
      const settings =
        (await settingsResponse.json()) as NotificationWorkspaceSettings;
      setSMTPDraft(smtpDraftFromAPI(settings));

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
          await readError(globalRoutesResponse, 'Failed to load global routes')
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
              'Failed to load workspace routes'
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
      setError(err instanceof Error ? err.message : 'Failed to load settings');
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
        throw new Error(await readError(response, 'Failed to save settings'));
      }
      const settings = (await response.json()) as NotificationWorkspaceSettings;
      setSMTPDraft(smtpDraftFromAPI(settings));
      setNotice('Notification settings saved');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings');
    } finally {
      setIsSavingSettings(false);
    }
  };

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
          await readError(response, 'Failed to save global routes')
        );
      }
      const routeSet = (await response.json()) as NotificationRouteSet;
      setGlobalRoutes(routeSetDraftFromAPI(routeSet));
      setNotice('Global routes saved');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save global routes'
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
          await readError(response, 'Failed to save workspace routes')
        );
      }
      const routeSet = (await response.json()) as NotificationRouteSet;
      setWorkspaceRoutes(routeSetDraftFromAPI(routeSet));
      setNotice('Workspace routes saved');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save workspace routes'
      );
    } finally {
      setIsSavingWorkspaceRoutes(false);
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
    return (
      <Card>
        <CardContent className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading notifications...
        </CardContent>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="grid-cols-[1fr_auto]">
          <div className="flex items-center gap-2">
            <Bell className="h-4 w-4 text-muted-foreground" />
            <CardTitle className="text-sm">Notifications</CardTitle>
            <Badge variant="default">
              {canConfigureWorkspaceRoutes ? selectedWorkspaceName : 'Global'}
            </Badge>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
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
        </CardContent>
      </Card>

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
        <>
          <RouteSetSection
            title="Global Routes"
            badge={globalRoutes.enabled ? 'Enabled' : 'Disabled'}
            draft={globalRoutes}
            channels={channels}
            saving={isSavingGlobalRoutes}
            emptyText="No global routes configured."
            onSave={saveGlobalRoutes}
            onChange={(updater) =>
              setGlobalRoutes((current) => updater(current))
            }
          />

          {canConfigureWorkspaceRoutes && (
            <RouteSetSection
              title={`${selectedWorkspaceName} Routes`}
              badge={
                workspaceRoutes.inheritGlobal ? 'Inherits Global' : 'Isolated'
              }
              draft={workspaceRoutes}
              channels={channels}
              saving={isSavingWorkspaceRoutes}
              showInheritGlobal
              emptyText="No workspace routes configured."
              onSave={saveWorkspaceRoutes}
              onChange={(updater) =>
                setWorkspaceRoutes((current) => updater(current))
              }
            />
          )}

          <ReusableChannelsSection
            channels={channels}
            savingChannelIndex={savingChannelIndex}
            onAdd={addChannel}
            onUpdate={updateChannel}
            onSave={saveChannel}
            onDelete={setDeleteChannelIndex}
          />
        </>
      ) : (
        <ReusableChannelsUnavailableCard showDAGLocalNote={false} />
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
