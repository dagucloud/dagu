import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  FlaskConical,
  Link2,
  Loader2,
  Plus,
  RefreshCw,
  Save,
  Settings,
  Trash2,
  XCircle,
} from 'lucide-react';
import { useMemo } from 'react';
import { Link } from 'react-router-dom';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  NotificationEventType,
  NotificationProviderType,
} from '../../../../../api/v1/schema';
import {
  blankChannel,
  blankTarget,
  DeliveryDraft,
  deliveryLabel,
  DraftChannel,
  DraftSettings,
  DraftSubscription,
  DraftTarget,
  EVENT_OPTIONS,
  providerIcon,
  providerLabel,
  PROVIDER_OPTIONS,
  TestResult,
} from './notificationDrafts';

type ProviderFieldsProps = {
  draft: DeliveryDraft;
  onChange: (next: DeliveryDraft) => void;
};

function ProviderFields({ draft, onChange }: ProviderFieldsProps) {
  const update = (patch: Partial<DeliveryDraft>) =>
    onChange({ ...draft, ...patch });

  if (draft.type === NotificationProviderType.email) {
    return (
      <div className="grid gap-3 md:grid-cols-2">
        <Input
          value={draft.email.to}
          placeholder="To"
          onChange={(event) =>
            update({ email: { ...draft.email, to: event.target.value } })
          }
        />
        <Input
          value={draft.email.from}
          placeholder="From"
          onChange={(event) =>
            update({ email: { ...draft.email, from: event.target.value } })
          }
        />
        <Input
          value={draft.email.cc}
          placeholder="Cc"
          onChange={(event) =>
            update({ email: { ...draft.email, cc: event.target.value } })
          }
        />
        <Input
          value={draft.email.bcc}
          placeholder="Bcc"
          onChange={(event) =>
            update({ email: { ...draft.email, bcc: event.target.value } })
          }
        />
        <Input
          value={draft.email.subjectPrefix}
          placeholder="Subject prefix"
          onChange={(event) =>
            update({
              email: { ...draft.email, subjectPrefix: event.target.value },
            })
          }
        />
        <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
          <Checkbox
            checked={draft.email.attachLogs}
            onCheckedChange={(value) =>
              update({
                email: { ...draft.email, attachLogs: !!value },
              })
            }
          />
          Attach logs
        </label>
      </div>
    );
  }

  if (draft.type === NotificationProviderType.webhook) {
    return (
      <div className="space-y-3">
        <Input
          value={draft.webhook.url}
          placeholder={
            draft.webhook.urlConfigured
              ? `URL configured (${draft.webhook.urlPreview || 'saved'})`
              : 'URL'
          }
          onChange={(event) =>
            update({
              webhook: { ...draft.webhook, url: event.target.value },
            })
          }
        />
        {draft.webhook.headerPreviews &&
          Object.keys(draft.webhook.headerPreviews).length > 0 && (
            <div className="flex flex-wrap gap-2">
              {Object.entries(draft.webhook.headerPreviews).map(
                ([key, value]) => (
                  <Badge key={key} variant="outline">
                    {key}: {value}
                  </Badge>
                )
              )}
            </div>
          )}
        <Textarea
          value={draft.webhook.headers}
          placeholder="Header-Name: value"
          onChange={(event) =>
            update({
              webhook: { ...draft.webhook, headers: event.target.value },
            })
          }
        />
        <Input
          type="password"
          value={draft.webhook.hmacSecret}
          placeholder={
            draft.webhook.hmacSecretConfigured
              ? 'HMAC secret configured'
              : 'HMAC secret'
          }
          onChange={(event) =>
            update({
              webhook: { ...draft.webhook, hmacSecret: event.target.value },
            })
          }
        />
        <div className="grid gap-2 md:grid-cols-2">
          <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
            <Checkbox
              checked={draft.webhook.clearHeaders}
              onCheckedChange={(value) =>
                update({
                  webhook: { ...draft.webhook, clearHeaders: !!value },
                })
              }
            />
            Clear headers
          </label>
          <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
            <Checkbox
              checked={draft.webhook.clearHmacSecret}
              onCheckedChange={(value) =>
                update({
                  webhook: { ...draft.webhook, clearHmacSecret: !!value },
                })
              }
            />
            Clear HMAC
          </label>
          <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
            <Checkbox
              checked={draft.webhook.allowInsecureHttp}
              onCheckedChange={(value) =>
                update({
                  webhook: { ...draft.webhook, allowInsecureHttp: !!value },
                })
              }
            />
            Allow HTTP
          </label>
          <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
            <Checkbox
              checked={draft.webhook.allowPrivateNetwork}
              onCheckedChange={(value) =>
                update({
                  webhook: { ...draft.webhook, allowPrivateNetwork: !!value },
                })
              }
            />
            Allow private network
          </label>
        </div>
      </div>
    );
  }

  if (draft.type === NotificationProviderType.slack) {
    return (
      <Input
        type="password"
        value={draft.slack.webhookUrl}
        placeholder={
          draft.slack.webhookUrlConfigured
            ? `Webhook URL configured (${draft.slack.webhookUrlPreview || 'saved'})`
            : 'Slack webhook URL'
        }
        onChange={(event) =>
          update({
            slack: { ...draft.slack, webhookUrl: event.target.value },
          })
        }
      />
    );
  }

  return (
    <div className="grid gap-3 md:grid-cols-2">
      <Input
        type="password"
        value={draft.telegram.botToken}
        placeholder={
          draft.telegram.botTokenConfigured
            ? `Bot token configured (${draft.telegram.botTokenPreview || 'saved'})`
            : 'Bot token'
        }
        onChange={(event) =>
          update({
            telegram: { ...draft.telegram, botToken: event.target.value },
          })
        }
      />
      <Input
        value={draft.telegram.chatId}
        placeholder="Chat ID"
        onChange={(event) =>
          update({
            telegram: { ...draft.telegram, chatId: event.target.value },
          })
        }
      />
    </div>
  );
}

type EventFilterEditorProps = {
  events: NotificationEventType[];
  onChange: (events: NotificationEventType[]) => void;
};

function EventFilterEditor({ events, onChange }: EventFilterEditorProps) {
  return (
    <div className="flex flex-wrap gap-2">
      {EVENT_OPTIONS.map((event) => {
        const checked = events.includes(event.value);
        return (
          <label
            key={event.value}
            className="flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs"
          >
            <Checkbox
              checked={checked}
              onCheckedChange={(value) =>
                onChange(
                  value
                    ? [...events, event.value]
                    : events.filter((item) => item !== event.value)
                )
              }
            />
            {event.label}
          </label>
        );
      })}
      {events.length > 0 && (
        <Button variant="ghost" size="sm" onClick={() => onChange([])}>
          Inherit
        </Button>
      )}
    </div>
  );
}

type NotificationOverviewCardProps = {
  draft: DraftSettings;
  error: string | null;
  notice: string | null;
  testResults: TestResult[];
  isSaving: boolean;
  testingTargetId: string | null;
  testableDestinationCount: number;
  onEnabledChange: (enabled: boolean) => void;
  onEventsChange: (events: NotificationEventType[]) => void;
  onRefresh: () => void;
  onTestAll: () => void;
  onSave: () => void;
};

export function NotificationOverviewCard({
  draft,
  error,
  notice,
  testResults,
  isSaving,
  testingTargetId,
  testableDestinationCount,
  onEnabledChange,
  onEventsChange,
  onRefresh,
  onTestAll,
  onSave,
}: NotificationOverviewCardProps) {
  return (
    <Card>
      <CardHeader className="grid-cols-[1fr_auto]">
        <div className="flex items-center gap-2">
          <Bell className="h-4 w-4 text-muted-foreground" />
          <CardTitle className="text-sm">Notifications</CardTitle>
          <Badge variant={draft.enabled ? 'success' : 'default'}>
            {draft.enabled ? 'Enabled' : 'Disabled'}
          </Badge>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Switch
            checked={draft.enabled}
            onCheckedChange={onEnabledChange}
            aria-label="Toggle notifications"
          />
          <Button variant="outline" size="sm" onClick={onRefresh}>
            <RefreshCw className="h-4 w-4" />
            Refresh
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={onTestAll}
            disabled={
              testableDestinationCount === 0 || testingTargetId !== null
            }
          >
            {testingTargetId === '__all__' ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <FlaskConical className="h-4 w-4" />
            )}
            Test
          </Button>
          <Button size="sm" onClick={onSave} disabled={isSaving}>
            {isSaving ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Save className="h-4 w-4" />
            )}
            Save
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
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

        <div className="flex flex-wrap gap-2">
          {EVENT_OPTIONS.map((event) => {
            const checked = draft.events.includes(event.value);
            return (
              <label
                key={event.value}
                className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm"
              >
                <Checkbox
                  checked={checked}
                  onCheckedChange={(value) =>
                    onEventsChange(
                      value
                        ? [...draft.events, event.value]
                        : draft.events.filter((item) => item !== event.value)
                    )
                  }
                />
                {event.label}
              </label>
            );
          })}
        </div>

        {testResults.length > 0 && (
          <div className="grid gap-2 sm:grid-cols-2">
            {testResults.map((result) => (
              <div
                key={`${result.targetId}-${result.provider}`}
                className="flex items-center gap-2 rounded-md border border-border px-3 py-2 text-sm"
              >
                {result.delivered ? (
                  <CheckCircle2 className="h-4 w-4 text-success" />
                ) : (
                  <XCircle className="h-4 w-4 text-destructive" />
                )}
                <span className="min-w-0 flex-1 truncate">
                  {result.targetName || result.provider}
                </span>
                <Badge variant={result.delivered ? 'success' : 'error'}>
                  {result.delivered ? 'Delivered' : 'Failed'}
                </Badge>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

type WorkspaceChannelsSectionProps = {
  channels: DraftChannel[];
  savingChannelIndex: number | null;
  onAdd: () => void;
  onUpdate: (
    index: number,
    updater: (channel: DraftChannel) => DraftChannel
  ) => void;
  onSave: (index: number) => void;
  onDelete: (index: number) => void;
};

export function WorkspaceChannelsSection({
  channels,
  savingChannelIndex,
  onAdd,
  onUpdate,
  onSave,
  onDelete,
}: WorkspaceChannelsSectionProps) {
  return (
    <>
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">
          Workspace Channels
        </h3>
        <Button variant="outline" size="sm" onClick={onAdd}>
          <Plus className="h-4 w-4" />
          Add
        </Button>
      </div>

      {channels.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-sm text-muted-foreground">
            No channels configured.
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {channels.map((channel, index) => {
            const Icon = providerIcon(channel.type);
            return (
              <Card key={channel.id || `new-${index}`}>
                <CardHeader className="grid-cols-[1fr_auto]">
                  <div className="flex min-w-0 items-center gap-2">
                    <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                    <CardTitle className="truncate text-sm">
                      {deliveryLabel(channel)}
                    </CardTitle>
                    <Badge variant={channel.enabled ? 'success' : 'default'}>
                      {channel.enabled ? 'Enabled' : 'Disabled'}
                    </Badge>
                    {!channel.id && <Badge variant="warning">New</Badge>}
                  </div>
                  <div className="flex items-center gap-2">
                    <Switch
                      checked={channel.enabled}
                      onCheckedChange={(enabled) =>
                        onUpdate(index, (current) => ({
                          ...current,
                          enabled,
                        }))
                      }
                      aria-label={`Toggle ${deliveryLabel(channel)}`}
                    />
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => onSave(index)}
                      disabled={savingChannelIndex !== null}
                    >
                      {savingChannelIndex === index ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        <Save className="h-4 w-4" />
                      )}
                      Save
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onDelete(index)}
                      aria-label={`Delete ${deliveryLabel(channel)}`}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </div>
                </CardHeader>
                <CardContent className="space-y-4">
                  <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px]">
                    <Input
                      value={channel.name}
                      placeholder="Channel name"
                      onChange={(event) =>
                        onUpdate(index, (current) => ({
                          ...current,
                          name: event.target.value,
                        }))
                      }
                    />
                    <Select
                      value={channel.type}
                      onValueChange={(value) =>
                        onUpdate(index, (current) => ({
                          ...blankChannel(value as NotificationProviderType),
                          id: current.id,
                          name: current.name,
                          enabled: current.enabled,
                        }))
                      }
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {PROVIDER_OPTIONS.map((provider) => (
                          <SelectItem
                            key={provider.value}
                            value={provider.value}
                          >
                            {provider.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <ProviderFields
                    draft={channel}
                    onChange={(next) =>
                      onUpdate(index, () => ({
                        ...next,
                        id: channel.id,
                      }))
                    }
                  />
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}
    </>
  );
}

type DAGSubscriptionsSectionProps = {
  draft: DraftSettings;
  channels: DraftChannel[];
  testingTargetId: string | null;
  manageChannelsHref?: string;
  onAdd: () => void;
  onUpdate: (
    index: number,
    updater: (subscription: DraftSubscription) => DraftSubscription
  ) => void;
  onDelete: (index: number) => void;
  onTest: (targetId?: string, events?: NotificationEventType[]) => void;
};

export function DAGSubscriptionsSection({
  draft,
  channels,
  testingTargetId,
  manageChannelsHref,
  onAdd,
  onUpdate,
  onDelete,
  onTest,
}: DAGSubscriptionsSectionProps) {
  const channelsById = useMemo(() => {
    const map = new Map<string, DraftChannel>();
    channels.forEach((channel) => {
      if (channel.id) {
        map.set(channel.id, channel);
      }
    });
    return map;
  }, [channels]);

  return (
    <>
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">
          DAG Subscriptions
        </h3>
        <div className="flex items-center gap-2">
          {manageChannelsHref && (
            <Button asChild variant="ghost" size="sm">
              <Link to={manageChannelsHref}>
                <Settings className="h-4 w-4" />
                Manage channels
              </Link>
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            onClick={onAdd}
            disabled={channels.filter((channel) => channel.id).length === 0}
          >
            <Plus className="h-4 w-4" />
            Add
          </Button>
        </div>
      </div>

      {draft.subscriptions.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-sm text-muted-foreground">
            No channel subscriptions configured.
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {draft.subscriptions.map((subscription, index) => {
            const channel = channelsById.get(subscription.channelId);
            const Icon = providerIcon(channel?.type);
            const usedChannelIds = new Set(
              draft.subscriptions
                .filter((_, subIndex) => subIndex !== index)
                .map((item) => item.channelId)
            );
            return (
              <Card
                key={subscription.id || `${subscription.channelId}-${index}`}
              >
                <CardHeader className="grid-cols-[1fr_auto]">
                  <div className="flex min-w-0 items-center gap-2">
                    <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                    <CardTitle className="truncate text-sm">
                      {channel?.name || subscription.channelId}
                    </CardTitle>
                    <Badge
                      variant={
                        subscription.enabled && channel?.enabled
                          ? 'success'
                          : 'default'
                      }
                    >
                      {subscription.enabled && channel?.enabled
                        ? 'Enabled'
                        : 'Disabled'}
                    </Badge>
                    {!channel && <Badge variant="error">Missing</Badge>}
                  </div>
                  <div className="flex items-center gap-2">
                    <Switch
                      checked={subscription.enabled}
                      onCheckedChange={(enabled) =>
                        onUpdate(index, (current) => ({
                          ...current,
                          enabled,
                        }))
                      }
                      aria-label={`Toggle ${channel?.name || subscription.channelId}`}
                    />
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        subscription.id &&
                        onTest(subscription.id, subscription.events)
                      }
                      disabled={!subscription.id || testingTargetId !== null}
                    >
                      {testingTargetId === subscription.id ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                      ) : (
                        <FlaskConical className="h-4 w-4" />
                      )}
                      Test
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onDelete(index)}
                      aria-label={`Delete ${channel?.name || subscription.channelId}`}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </div>
                </CardHeader>
                <CardContent className="space-y-4">
                  <Select
                    value={subscription.channelId}
                    onValueChange={(channelId) =>
                      onUpdate(index, (current) => ({
                        ...current,
                        channelId,
                      }))
                    }
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {channels
                        .filter((item) => item.id)
                        .map((item) => (
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

                  <EventFilterEditor
                    events={subscription.events}
                    onChange={(events) =>
                      onUpdate(index, (current) => ({
                        ...current,
                        events,
                      }))
                    }
                  />
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}
    </>
  );
}

export function ReusableChannelsUnavailableCard({
  showDAGLocalNote = true,
}: {
  showDAGLocalNote?: boolean;
}) {
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <Bell className="h-4 w-4 text-muted-foreground" />
          <CardTitle className="text-sm">Workspace Channels</CardTitle>
        </div>
      </CardHeader>
      <CardContent className="text-sm text-muted-foreground">
        Reusable workspace channels require an active Dagu license or trial.
        {showDAGLocalNote && ' DAG-local targets remain available.'}
      </CardContent>
    </Card>
  );
}

type DAGLocalTargetsSectionProps = {
  draft: DraftSettings;
  testingTargetId: string | null;
  onAdd: () => void;
  onUpdate: (
    index: number,
    updater: (target: DraftTarget) => DraftTarget
  ) => void;
  onDelete: (index: number) => void;
  onTest: (targetId?: string, events?: NotificationEventType[]) => void;
};

export function DAGLocalTargetsSection({
  draft,
  testingTargetId,
  onAdd,
  onUpdate,
  onDelete,
  onTest,
}: DAGLocalTargetsSectionProps) {
  if (draft.targets.length === 0) {
    return (
      <div className="flex justify-end">
        <Button variant="ghost" size="sm" onClick={onAdd}>
          <Link2 className="h-4 w-4" />
          Add DAG-local target
        </Button>
      </div>
    );
  }

  return (
    <>
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">
          DAG-local Targets
        </h3>
        <Button variant="outline" size="sm" onClick={onAdd}>
          <Plus className="h-4 w-4" />
          Add local
        </Button>
      </div>
      <div className="space-y-3">
        {draft.targets.map((target, index) => {
          const Icon = providerIcon(target.type);
          return (
            <Card key={target.id || index}>
              <CardHeader className="grid-cols-[1fr_auto]">
                <div className="flex min-w-0 items-center gap-2">
                  <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                  <CardTitle className="truncate text-sm">
                    {deliveryLabel(target)}
                  </CardTitle>
                  <Badge variant={target.enabled ? 'success' : 'default'}>
                    {target.enabled ? 'Enabled' : 'Disabled'}
                  </Badge>
                </div>
                <div className="flex items-center gap-2">
                  <Switch
                    checked={target.enabled}
                    onCheckedChange={(enabled) =>
                      onUpdate(index, (current) => ({
                        ...current,
                        enabled,
                      }))
                    }
                    aria-label={`Toggle ${deliveryLabel(target)}`}
                  />
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      target.id && onTest(target.id, target.events)
                    }
                    disabled={!target.id || testingTargetId !== null}
                  >
                    {testingTargetId === target.id ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <FlaskConical className="h-4 w-4" />
                    )}
                    Test
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => onDelete(index)}
                    aria-label={`Delete ${deliveryLabel(target)}`}
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px]">
                  <Input
                    value={target.name}
                    placeholder="Target name"
                    onChange={(event) =>
                      onUpdate(index, (current) => ({
                        ...current,
                        name: event.target.value,
                      }))
                    }
                  />
                  <Select
                    value={target.type}
                    onValueChange={(value) =>
                      onUpdate(index, (current) => ({
                        ...blankTarget(value as NotificationProviderType),
                        id: current.id,
                        name: current.name,
                        enabled: current.enabled,
                        events: current.events,
                      }))
                    }
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {PROVIDER_OPTIONS.map((provider) => (
                        <SelectItem key={provider.value} value={provider.value}>
                          {provider.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <EventFilterEditor
                  events={target.events}
                  onChange={(events) =>
                    onUpdate(index, (current) => ({
                      ...current,
                      events,
                    }))
                  }
                />

                <ProviderFields
                  draft={target}
                  onChange={(next) =>
                    onUpdate(index, (current) => ({
                      ...next,
                      id: current.id,
                      events: current.events,
                    }))
                  }
                />
              </CardContent>
            </Card>
          );
        })}
      </div>
    </>
  );
}
