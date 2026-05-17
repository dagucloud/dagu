import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  FlaskConical,
  Link2,
  Loader2,
  Mail,
  MessageSquare,
  Plus,
  RefreshCw,
  Save,
  Send,
  Trash2,
  Webhook,
  XCircle,
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
import { Textarea } from '@/components/ui/textarea';
import {
  components,
  NotificationEventType,
  NotificationProviderType,
} from '../../../../api/v1/schema';
import { AppBarContext } from '../../../../contexts/AppBarContext';
import { TOKEN_KEY } from '../../../../contexts/AuthContext';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useLicense } from '../../../../hooks/useLicense';

type NotificationSettings = components['schemas']['DAGNotificationSettings'];
type NotificationTarget = components['schemas']['NotificationTarget'];
type NotificationTargetInput = components['schemas']['NotificationTargetInput'];
type NotificationChannel = components['schemas']['NotificationChannel'];
type NotificationChannelInput =
  components['schemas']['NotificationChannelInput'];
type NotificationSubscription =
  components['schemas']['NotificationSubscription'];
type NotificationSubscriptionInput =
  components['schemas']['NotificationSubscriptionInput'];
type TestResult = components['schemas']['TestDAGNotificationResult'];

type DeliveryDraft = {
  name: string;
  type: NotificationProviderType;
  enabled: boolean;
  email: {
    from: string;
    to: string;
    cc: string;
    bcc: string;
    subjectPrefix: string;
    attachLogs: boolean;
  };
  webhook: {
    url: string;
    headers: string;
    hmacSecret: string;
    urlPreview?: string;
    urlConfigured?: boolean;
    headerPreviews?: Record<string, string>;
    hmacSecretConfigured?: boolean;
    clearHeaders: boolean;
    clearHmacSecret: boolean;
    allowInsecureHttp: boolean;
    allowPrivateNetwork: boolean;
  };
  slack: {
    webhookUrl: string;
    webhookUrlPreview?: string;
    webhookUrlConfigured?: boolean;
  };
  telegram: {
    botToken: string;
    botTokenPreview?: string;
    botTokenConfigured?: boolean;
    chatId: string;
  };
};

type DraftTarget = DeliveryDraft & {
  id?: string;
  events: NotificationEventType[];
};

type DraftChannel = DeliveryDraft & {
  id?: string;
};

type DraftSubscription = {
  id?: string;
  channelId: string;
  enabled: boolean;
  events: NotificationEventType[];
};

type DraftSettings = {
  enabled: boolean;
  events: NotificationEventType[];
  targets: DraftTarget[];
  subscriptions: DraftSubscription[];
};

type NotificationsTabProps = {
  fileName: string;
};

const EVENT_OPTIONS = [
  { value: NotificationEventType.dag_run_failed, label: 'Failed' },
  { value: NotificationEventType.dag_run_aborted, label: 'Aborted' },
  { value: NotificationEventType.dag_run_rejected, label: 'Rejected' },
  { value: NotificationEventType.dag_run_waiting, label: 'Waiting' },
  { value: NotificationEventType.dag_run_succeeded, label: 'Succeeded' },
];

const PROVIDER_OPTIONS = [
  { value: NotificationProviderType.email, label: 'Email', icon: Mail },
  { value: NotificationProviderType.webhook, label: 'Webhook', icon: Webhook },
  {
    value: NotificationProviderType.slack,
    label: 'Slack',
    icon: MessageSquare,
  },
  { value: NotificationProviderType.telegram, label: 'Telegram', icon: Send },
];

function defaultDraft(): DraftSettings {
  return {
    enabled: true,
    events: [
      NotificationEventType.dag_run_failed,
      NotificationEventType.dag_run_aborted,
      NotificationEventType.dag_run_rejected,
      NotificationEventType.dag_run_waiting,
    ],
    targets: [],
    subscriptions: [],
  };
}

function blankDelivery(type: NotificationProviderType): DeliveryDraft {
  const label =
    PROVIDER_OPTIONS.find((provider) => provider.value === type)?.label ||
    'Channel';
  return {
    name: label,
    type,
    enabled: true,
    email: {
      from: '',
      to: '',
      cc: '',
      bcc: '',
      subjectPrefix: '',
      attachLogs: false,
    },
    webhook: {
      url: '',
      headers: '',
      hmacSecret: '',
      clearHeaders: false,
      clearHmacSecret: false,
      allowInsecureHttp: false,
      allowPrivateNetwork: false,
    },
    slack: {
      webhookUrl: '',
    },
    telegram: {
      botToken: '',
      chatId: '',
    },
  };
}

function blankTarget(type: NotificationProviderType): DraftTarget {
  return {
    ...blankDelivery(type),
    events: [],
  };
}

function blankChannel(type: NotificationProviderType): DraftChannel {
  return blankDelivery(type);
}

function joinAddresses(values?: string[]): string {
  return values?.join(', ') || '';
}

function splitList(value: string): string[] {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
}

function parseHeaders(value: string): Record<string, string> | undefined {
  const headers: Record<string, string> = {};
  value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .forEach((line) => {
      const separator = line.includes(':') ? ':' : '=';
      const [rawKey, ...rest] = line.split(separator);
      const key = (rawKey || '').trim();
      const headerValue = rest.join(separator).trim();
      if (key && headerValue) {
        headers[key] = headerValue;
      }
    });
  return Object.keys(headers).length > 0 ? headers : undefined;
}

function applyEmailDraft(
  draft: DeliveryDraft,
  email?: components['schemas']['NotificationEmailTarget']
) {
  if (!email) return;
  draft.email = {
    from: email.from || '',
    to: joinAddresses(email.to),
    cc: joinAddresses(email.cc),
    bcc: joinAddresses(email.bcc),
    subjectPrefix: email.subjectPrefix || '',
    attachLogs: !!email.attachLogs,
  };
}

function applyWebhookDraft(
  draft: DeliveryDraft,
  webhook?: components['schemas']['NotificationWebhookTarget']
) {
  if (!webhook) return;
  draft.webhook.urlPreview = webhook.urlPreview;
  draft.webhook.urlConfigured = webhook.urlConfigured;
  draft.webhook.headerPreviews = webhook.headers;
  draft.webhook.hmacSecretConfigured = webhook.hmacSecretConfigured;
  draft.webhook.allowInsecureHttp = !!webhook.allowInsecureHttp;
  draft.webhook.allowPrivateNetwork = !!webhook.allowPrivateNetwork;
}

function applySlackDraft(
  draft: DeliveryDraft,
  slack?: components['schemas']['NotificationSlackTarget']
) {
  if (!slack) return;
  draft.slack.webhookUrlPreview = slack.webhookUrlPreview;
  draft.slack.webhookUrlConfigured = slack.webhookUrlConfigured;
}

function applyTelegramDraft(
  draft: DeliveryDraft,
  telegram?: components['schemas']['NotificationTelegramTarget']
) {
  if (!telegram) return;
  draft.telegram.botTokenPreview = telegram.botTokenPreview;
  draft.telegram.botTokenConfigured = telegram.botTokenConfigured;
  draft.telegram.chatId = telegram.chatId || '';
}

function draftTargetFromAPI(target: NotificationTarget): DraftTarget {
  const draft = blankTarget(target.type);
  draft.id = target.id;
  draft.name = target.name || '';
  draft.enabled = target.enabled;
  draft.events = target.events || [];
  applyEmailDraft(draft, target.email);
  applyWebhookDraft(draft, target.webhook);
  applySlackDraft(draft, target.slack);
  applyTelegramDraft(draft, target.telegram);
  return draft;
}

function draftChannelFromAPI(channel: NotificationChannel): DraftChannel {
  const draft = blankChannel(channel.type);
  draft.id = channel.id;
  draft.name = channel.name;
  draft.enabled = channel.enabled;
  applyEmailDraft(draft, channel.email);
  applyWebhookDraft(draft, channel.webhook);
  applySlackDraft(draft, channel.slack);
  applyTelegramDraft(draft, channel.telegram);
  return draft;
}

function draftSubscriptionFromAPI(
  subscription: NotificationSubscription
): DraftSubscription {
  return {
    id: subscription.id,
    channelId: subscription.channelId,
    enabled: subscription.enabled,
    events: subscription.events || [],
  };
}

function draftFromAPI(settings: NotificationSettings): DraftSettings {
  return {
    enabled: settings.enabled,
    events: settings.events,
    targets: settings.targets.map(draftTargetFromAPI),
    subscriptions: settings.subscriptions.map(draftSubscriptionFromAPI),
  };
}

function deliveryInput(target: DeliveryDraft) {
  const input = {
    name: target.name.trim(),
    type: target.type,
    enabled: target.enabled,
  };
  if (target.type === NotificationProviderType.email) {
    return {
      ...input,
      email: {
        from: target.email.from.trim() || undefined,
        to: splitList(target.email.to),
        cc: splitList(target.email.cc),
        bcc: splitList(target.email.bcc),
        subjectPrefix: target.email.subjectPrefix.trim() || undefined,
        attachLogs: target.email.attachLogs,
      },
    };
  }
  if (target.type === NotificationProviderType.webhook) {
    return {
      ...input,
      webhook: {
        url: target.webhook.url.trim() || undefined,
        headers: target.webhook.clearHeaders
          ? {}
          : parseHeaders(target.webhook.headers),
        hmacSecret: target.webhook.hmacSecret.trim() || undefined,
        clearHeaders: target.webhook.clearHeaders || undefined,
        clearHmacSecret: target.webhook.clearHmacSecret || undefined,
        allowInsecureHttp: target.webhook.allowInsecureHttp || undefined,
        allowPrivateNetwork: target.webhook.allowPrivateNetwork || undefined,
      },
    };
  }
  if (target.type === NotificationProviderType.slack) {
    return {
      ...input,
      slack: {
        webhookUrl: target.slack.webhookUrl.trim() || undefined,
      },
    };
  }
  return {
    ...input,
    telegram: {
      botToken: target.telegram.botToken.trim() || undefined,
      chatId: target.telegram.chatId.trim() || undefined,
    },
  };
}

function channelInput(channel: DraftChannel): NotificationChannelInput {
  return deliveryInput(channel) as NotificationChannelInput;
}

function targetInput(target: DraftTarget): NotificationTargetInput {
  return {
    id: target.id,
    ...deliveryInput(target),
    events: target.events.length > 0 ? target.events : undefined,
  } as NotificationTargetInput;
}

function subscriptionInput(
  subscription: DraftSubscription
): NotificationSubscriptionInput {
  return {
    id: subscription.id,
    channelId: subscription.channelId,
    enabled: subscription.enabled,
    events: subscription.events.length > 0 ? subscription.events : undefined,
  };
}

function authHeaders(): HeadersInit {
  const token = localStorage.getItem(TOKEN_KEY);
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function readError(
  response: Response,
  fallback: string
): Promise<string> {
  try {
    const data = await response.json();
    return data.message || fallback;
  } catch {
    return fallback;
  }
}

function providerIcon(type?: NotificationProviderType) {
  return (
    PROVIDER_OPTIONS.find((provider) => provider.value === type)?.icon || Bell
  );
}

function providerLabel(type?: NotificationProviderType): string {
  return (
    PROVIDER_OPTIONS.find((provider) => provider.value === type)?.label ||
    'Channel'
  );
}

function testEventForTarget(
  draft: DraftSettings,
  events?: NotificationEventType[]
): NotificationEventType {
  return events?.[0] || draft.events[0] || NotificationEventType.dag_run_failed;
}

function deliveryLabel(target: DeliveryDraft): string {
  return target.name.trim() || providerLabel(target.type);
}

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

function NotificationsTab({ fileName }: NotificationsTabProps) {
  const config = useConfig();
  const license = useLicense();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const reusableChannelsLicensed =
    !license.community && (license.valid || license.gracePeriod);
  const [draft, setDraft] = useState<DraftSettings>(defaultDraft);
  const [channels, setChannels] = useState<DraftChannel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [savingChannelIndex, setSavingChannelIndex] = useState<number | null>(
    null
  );
  const [testingTargetId, setTestingTargetId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<TestResult[]>([]);
  const [deleteTargetIndex, setDeleteTargetIndex] = useState<number | null>(
    null
  );
  const [deleteChannelIndex, setDeleteChannelIndex] = useState<number | null>(
    null
  );
  const [deleteSubscriptionIndex, setDeleteSubscriptionIndex] = useState<
    number | null
  >(null);
  const query = useMemo(
    () => `?remoteNode=${encodeURIComponent(remoteNode)}`,
    [remoteNode]
  );
  const channelsById = useMemo(() => {
    const map = new Map<string, DraftChannel>();
    channels.forEach((channel) => {
      if (channel.id) {
        map.set(channel.id, channel);
      }
    });
    return map;
  }, [channels]);
  const testableDestinationCount =
    draft.targets.length +
    (reusableChannelsLicensed ? draft.subscriptions.length : 0);

  const fetchData = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const settingsRequest = fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/notifications${query}`,
        { headers: authHeaders() }
      );
      const channelRequest = reusableChannelsLicensed
        ? fetch(`${config.apiURL}/notification-channels${query}`, {
            headers: authHeaders(),
          })
        : Promise.resolve<Response | null>(null);
      const [settingsResponse, channelsResponse] = await Promise.all([
        settingsRequest,
        channelRequest,
      ]);

      if (settingsResponse.status === 404) {
        setDraft(defaultDraft());
      } else if (!settingsResponse.ok) {
        throw new Error(
          await readError(settingsResponse, 'Failed to load notifications')
        );
      } else {
        const data = (await settingsResponse.json()) as NotificationSettings;
        setDraft(draftFromAPI(data));
      }

      if (!channelsResponse) {
        setChannels([]);
      } else {
        if (!channelsResponse.ok) {
          throw new Error(
            await readError(channelsResponse, 'Failed to load channels')
          );
        }
        const channelData =
          (await channelsResponse.json()) as components['schemas']['NotificationChannelListResponse'];
        setChannels(channelData.channels.map(draftChannelFromAPI));
      }
      setTestResults([]);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to load notifications'
      );
    } finally {
      setIsLoading(false);
    }
  }, [config.apiURL, fileName, query, reusableChannelsLicensed]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

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

  const updateTarget = (
    index: number,
    updater: (target: DraftTarget) => DraftTarget
  ) => {
    setDraft((current) => ({
      ...current,
      targets: current.targets.map((target, targetIndex) =>
        targetIndex === index ? updater(target) : target
      ),
    }));
  };

  const updateSubscription = (
    index: number,
    updater: (subscription: DraftSubscription) => DraftSubscription
  ) => {
    setDraft((current) => ({
      ...current,
      subscriptions: current.subscriptions.map((subscription, subIndex) =>
        subIndex === index ? updater(subscription) : subscription
      ),
    }));
  };

  const addChannel = () => {
    if (!reusableChannelsLicensed) return;
    setChannels((current) => [
      ...current,
      blankChannel(NotificationProviderType.email),
    ]);
  };

  const addSubscription = () => {
    if (!reusableChannelsLicensed) return;
    const used = new Set(draft.subscriptions.map((sub) => sub.channelId));
    const channel = channels.find((item) => item.id && !used.has(item.id));
    if (!channel?.id) {
      setError('Save a channel before adding another subscription.');
      return;
    }
    const channelId = channel.id;
    setDraft((current) => ({
      ...current,
      subscriptions: [
        ...current.subscriptions,
        {
          channelId,
          enabled: true,
          events: [],
        },
      ],
    }));
  };

  const addLocalTarget = () => {
    setDraft((current) => ({
      ...current,
      targets: [
        ...current.targets,
        blankTarget(NotificationProviderType.email),
      ],
    }));
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

  const saveSettings = async () => {
    setIsSaving(true);
    setError(null);
    setNotice(null);
    try {
      const body: components['schemas']['UpdateDAGNotificationsRequest'] = {
        enabled: draft.enabled,
        events: draft.events,
        targets: draft.targets.map(targetInput),
        ...(reusableChannelsLicensed
          ? { subscriptions: draft.subscriptions.map(subscriptionInput) }
          : {}),
      };
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/notifications${query}`,
        {
          method: 'PUT',
          headers: authHeaders(),
          body: JSON.stringify(body),
        }
      );
      if (!response.ok) {
        throw new Error(
          await readError(response, 'Failed to save notifications')
        );
      }
      const data = (await response.json()) as NotificationSettings;
      setDraft(draftFromAPI(data));
      setNotice('Saved');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save notifications'
      );
    } finally {
      setIsSaving(false);
    }
  };

  const testNotifications = async (
    targetId?: string,
    events?: NotificationEventType[]
  ) => {
    setTestingTargetId(targetId || '__all__');
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/notifications/test${query}`,
        {
          method: 'POST',
          headers: authHeaders(),
          body: JSON.stringify({
            targetId,
            eventType: testEventForTarget(draft, events),
          }),
        }
      );
      if (!response.ok) {
        throw new Error(
          await readError(response, 'Failed to send test notification')
        );
      }
      const data =
        (await response.json()) as components['schemas']['TestDAGNotificationResponse'];
      setTestResults(data.results);
      setNotice('Test sent');
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to send test notification'
      );
    } finally {
      setTestingTargetId(null);
    }
  };

  const removeTarget = () => {
    if (deleteTargetIndex === null) return;
    setDraft((current) => ({
      ...current,
      targets: current.targets.filter(
        (_, index) => index !== deleteTargetIndex
      ),
    }));
    setDeleteTargetIndex(null);
  };

  const removeSubscription = () => {
    if (!reusableChannelsLicensed) return;
    if (deleteSubscriptionIndex === null) return;
    setDraft((current) => ({
      ...current,
      subscriptions: current.subscriptions.filter(
        (_, index) => index !== deleteSubscriptionIndex
      ),
    }));
    setDeleteSubscriptionIndex(null);
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
            <Badge variant={draft.enabled ? 'success' : 'default'}>
              {draft.enabled ? 'Enabled' : 'Disabled'}
            </Badge>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Switch
              checked={draft.enabled}
              onCheckedChange={(enabled) =>
                setDraft((current) => ({ ...current, enabled }))
              }
              aria-label="Toggle notifications"
            />
            <Button variant="outline" size="sm" onClick={fetchData}>
              <RefreshCw className="h-4 w-4" />
              Refresh
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => testNotifications()}
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
            <Button size="sm" onClick={saveSettings} disabled={isSaving}>
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
                      setDraft((current) => ({
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

      {reusableChannelsLicensed ? (
        <>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-foreground">
              Workspace Channels
            </h3>
            <Button variant="outline" size="sm" onClick={addChannel}>
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
                        <Badge
                          variant={channel.enabled ? 'success' : 'default'}
                        >
                          {channel.enabled ? 'Enabled' : 'Disabled'}
                        </Badge>
                        {!channel.id && <Badge variant="warning">New</Badge>}
                      </div>
                      <div className="flex items-center gap-2">
                        <Switch
                          checked={channel.enabled}
                          onCheckedChange={(enabled) =>
                            updateChannel(index, (current) => ({
                              ...current,
                              enabled,
                            }))
                          }
                          aria-label={`Toggle ${deliveryLabel(channel)}`}
                        />
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => saveChannel(index)}
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
                          onClick={() => setDeleteChannelIndex(index)}
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
                            updateChannel(index, (current) => ({
                              ...current,
                              name: event.target.value,
                            }))
                          }
                        />
                        <Select
                          value={channel.type}
                          onValueChange={(value) =>
                            updateChannel(index, (current) => ({
                              ...blankChannel(
                                value as NotificationProviderType
                              ),
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
                          updateChannel(index, () => ({
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

          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-foreground">
              DAG Subscriptions
            </h3>
            <Button
              variant="outline"
              size="sm"
              onClick={addSubscription}
              disabled={channels.filter((channel) => channel.id).length === 0}
            >
              <Plus className="h-4 w-4" />
              Add
            </Button>
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
                    key={
                      subscription.id || `${subscription.channelId}-${index}`
                    }
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
                            updateSubscription(index, (current) => ({
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
                            testNotifications(
                              subscription.id,
                              subscription.events
                            )
                          }
                          disabled={
                            !subscription.id || testingTargetId !== null
                          }
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
                          onClick={() => setDeleteSubscriptionIndex(index)}
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
                          updateSubscription(index, (current) => ({
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
                                disabled={
                                  !!item.id && usedChannelIds.has(item.id)
                                }
                              >
                                {item.name || providerLabel(item.type)}
                              </SelectItem>
                            ))}
                        </SelectContent>
                      </Select>

                      <EventFilterEditor
                        events={subscription.events}
                        onChange={(events) =>
                          updateSubscription(index, (current) => ({
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
      ) : (
        <Card>
          <CardHeader>
            <div className="flex items-center gap-2">
              <Bell className="h-4 w-4 text-muted-foreground" />
              <CardTitle className="text-sm">Workspace Channels</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            Reusable workspace channels require an active Dagu license or trial.
            DAG-local targets remain available.
          </CardContent>
        </Card>
      )}

      {draft.targets.length > 0 && (
        <>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-foreground">
              DAG-local Targets
            </h3>
            <Button variant="outline" size="sm" onClick={addLocalTarget}>
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
                          updateTarget(index, (current) => ({
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
                          target.id &&
                          testNotifications(target.id, target.events)
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
                        onClick={() => setDeleteTargetIndex(index)}
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
                          updateTarget(index, (current) => ({
                            ...current,
                            name: event.target.value,
                          }))
                        }
                      />
                      <Select
                        value={target.type}
                        onValueChange={(value) =>
                          updateTarget(index, (current) => ({
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

                    <EventFilterEditor
                      events={target.events}
                      onChange={(events) =>
                        updateTarget(index, (current) => ({
                          ...current,
                          events,
                        }))
                      }
                    />

                    <ProviderFields
                      draft={target}
                      onChange={(next) =>
                        updateTarget(index, (current) => ({
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
      )}

      {draft.targets.length === 0 && (
        <div className="flex justify-end">
          <Button variant="ghost" size="sm" onClick={addLocalTarget}>
            <Link2 className="h-4 w-4" />
            Add DAG-local target
          </Button>
        </div>
      )}

      <ConfirmDialog
        title="Delete Target"
        buttonText="Delete"
        visible={deleteTargetIndex !== null}
        dismissModal={() => setDeleteTargetIndex(null)}
        onSubmit={removeTarget}
      >
        Delete{' '}
        {deleteTargetIndex !== null && draft.targets[deleteTargetIndex]
          ? deliveryLabel(draft.targets[deleteTargetIndex])
          : 'target'}
        ?
      </ConfirmDialog>

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

      <ConfirmDialog
        title="Delete Subscription"
        buttonText="Delete"
        visible={deleteSubscriptionIndex !== null}
        dismissModal={() => setDeleteSubscriptionIndex(null)}
        onSubmit={removeSubscription}
      >
        Delete this subscription?
      </ConfirmDialog>
    </div>
  );
}

function EventFilterEditor({
  events,
  onChange,
}: {
  events: NotificationEventType[];
  onChange: (events: NotificationEventType[]) => void;
}) {
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

export default NotificationsTab;
