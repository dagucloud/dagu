import { Bell, Mail, MessageSquare, Send, Webhook } from 'lucide-react';

import {
  components,
  NotificationEventType,
  NotificationProviderType,
} from '../../../../../api/v1/schema';
import { TOKEN_KEY } from '../../../../../contexts/AuthContext';

export type NotificationSettings =
  components['schemas']['DAGNotificationSettings'];
export type NotificationTarget = components['schemas']['NotificationTarget'];
export type NotificationTargetInput =
  components['schemas']['NotificationTargetInput'];
export type NotificationChannel = components['schemas']['NotificationChannel'];
export type NotificationChannelInput =
  components['schemas']['NotificationChannelInput'];
export type NotificationSubscription =
  components['schemas']['NotificationSubscription'];
export type NotificationSubscriptionInput =
  components['schemas']['NotificationSubscriptionInput'];
export type TestResult = components['schemas']['TestDAGNotificationResult'];

export type DeliveryDraft = {
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

export type DraftTarget = DeliveryDraft & {
  id?: string;
  events: NotificationEventType[];
};

export type DraftChannel = DeliveryDraft & {
  id?: string;
};

export type DraftSubscription = {
  id?: string;
  channelId: string;
  enabled: boolean;
  events: NotificationEventType[];
};

export type DraftSettings = {
  enabled: boolean;
  events: NotificationEventType[];
  targets: DraftTarget[];
  subscriptions: DraftSubscription[];
};

export const EVENT_OPTIONS = [
  { value: NotificationEventType.dag_run_failed, label: 'Failed' },
  { value: NotificationEventType.dag_run_aborted, label: 'Aborted' },
  { value: NotificationEventType.dag_run_rejected, label: 'Rejected' },
  { value: NotificationEventType.dag_run_waiting, label: 'Waiting' },
  { value: NotificationEventType.dag_run_succeeded, label: 'Succeeded' },
];

export const PROVIDER_OPTIONS = [
  { value: NotificationProviderType.email, label: 'Email', icon: Mail },
  {
    value: NotificationProviderType.webhook,
    label: 'Generic Webhook',
    icon: Webhook,
  },
  {
    value: NotificationProviderType.slack,
    label: 'Slack',
    icon: MessageSquare,
  },
  { value: NotificationProviderType.telegram, label: 'Telegram', icon: Send },
];

export function defaultDraft(): DraftSettings {
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

export function blankDelivery(type: NotificationProviderType): DeliveryDraft {
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

export function blankTarget(type: NotificationProviderType): DraftTarget {
  return {
    ...blankDelivery(type),
    events: [],
  };
}

export function blankChannel(type: NotificationProviderType): DraftChannel {
  return blankDelivery(type);
}

export function isSlackIncomingWebhookURL(value: string): boolean {
  try {
    const parsed = new URL(value.trim());
    const hostname = parsed.hostname.toLowerCase().replace(/\.$/, '');
    return (
      (hostname === 'hooks.slack.com' || hostname === 'hooks.slack-gov.com') &&
      parsed.pathname.startsWith('/services/')
    );
  } catch {
    return false;
  }
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

export function draftChannelFromAPI(
  channel: NotificationChannel
): DraftChannel {
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

export function draftFromAPI(settings: NotificationSettings): DraftSettings {
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

export function channelInput(channel: DraftChannel): NotificationChannelInput {
  return deliveryInput(channel) as NotificationChannelInput;
}

export function targetInput(target: DraftTarget): NotificationTargetInput {
  return {
    id: target.id,
    ...deliveryInput(target),
    events: target.events.length > 0 ? target.events : undefined,
  } as NotificationTargetInput;
}

export function subscriptionInput(
  subscription: DraftSubscription
): NotificationSubscriptionInput {
  return {
    id: subscription.id,
    channelId: subscription.channelId,
    enabled: subscription.enabled,
    events: subscription.events.length > 0 ? subscription.events : undefined,
  };
}

export function authHeaders(): HeadersInit {
  const token = localStorage.getItem(TOKEN_KEY);
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

export async function readError(
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

export function providerIcon(type?: NotificationProviderType) {
  return (
    PROVIDER_OPTIONS.find((provider) => provider.value === type)?.icon || Bell
  );
}

export function providerLabel(type?: NotificationProviderType): string {
  return (
    PROVIDER_OPTIONS.find((provider) => provider.value === type)?.label ||
    'Channel'
  );
}

export function testEventForTarget(
  draft: DraftSettings,
  events?: NotificationEventType[]
): NotificationEventType {
  return events?.[0] || draft.events[0] || NotificationEventType.dag_run_failed;
}

export function deliveryLabel(target: DeliveryDraft): string {
  return target.name.trim() || providerLabel(target.type);
}
