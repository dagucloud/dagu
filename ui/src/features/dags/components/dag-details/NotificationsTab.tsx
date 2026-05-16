import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  FlaskConical,
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

import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
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

type NotificationSettings = components['schemas']['DAGNotificationSettings'];
type NotificationTarget = components['schemas']['NotificationTarget'];
type NotificationTargetInput = components['schemas']['NotificationTargetInput'];
type TestResult = components['schemas']['TestDAGNotificationResult'];

type DraftTarget = {
  id?: string;
  name: string;
  type: NotificationProviderType;
  enabled: boolean;
  events: NotificationEventType[];
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

type DraftSettings = {
  enabled: boolean;
  events: NotificationEventType[];
  targets: DraftTarget[];
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
  };
}

function blankTarget(type: NotificationProviderType): DraftTarget {
  const label =
    PROVIDER_OPTIONS.find((provider) => provider.value === type)?.label ||
    'Target';
  return {
    name: label,
    type,
    enabled: true,
    events: [],
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

function draftTargetFromAPI(target: NotificationTarget): DraftTarget {
  const draft = blankTarget(target.type);
  draft.id = target.id;
  draft.name = target.name || '';
  draft.enabled = target.enabled;
  draft.events = target.events || [];
  if (target.email) {
    draft.email = {
      from: target.email.from || '',
      to: joinAddresses(target.email.to),
      cc: joinAddresses(target.email.cc),
      bcc: joinAddresses(target.email.bcc),
      subjectPrefix: target.email.subjectPrefix || '',
      attachLogs: !!target.email.attachLogs,
    };
  }
  if (target.webhook) {
    draft.webhook.urlPreview = target.webhook.urlPreview;
    draft.webhook.urlConfigured = target.webhook.urlConfigured;
    draft.webhook.headerPreviews = target.webhook.headers;
    draft.webhook.hmacSecretConfigured = target.webhook.hmacSecretConfigured;
    draft.webhook.allowInsecureHttp = !!target.webhook.allowInsecureHttp;
    draft.webhook.allowPrivateNetwork = !!target.webhook.allowPrivateNetwork;
  }
  if (target.slack) {
    draft.slack.webhookUrlPreview = target.slack.webhookUrlPreview;
    draft.slack.webhookUrlConfigured = target.slack.webhookUrlConfigured;
  }
  if (target.telegram) {
    draft.telegram.botTokenPreview = target.telegram.botTokenPreview;
    draft.telegram.botTokenConfigured = target.telegram.botTokenConfigured;
    draft.telegram.chatId = target.telegram.chatId || '';
  }
  return draft;
}

function draftFromAPI(settings: NotificationSettings): DraftSettings {
  return {
    enabled: settings.enabled,
    events: settings.events,
    targets: settings.targets.map(draftTargetFromAPI),
  };
}

function targetInput(target: DraftTarget): NotificationTargetInput {
  const input: NotificationTargetInput = {
    id: target.id,
    name: target.name.trim() || undefined,
    type: target.type,
    enabled: target.enabled,
    events: target.events.length > 0 ? target.events : undefined,
  };
  if (target.type === NotificationProviderType.email) {
    input.email = {
      from: target.email.from.trim() || undefined,
      to: splitList(target.email.to),
      cc: splitList(target.email.cc),
      bcc: splitList(target.email.bcc),
      subjectPrefix: target.email.subjectPrefix.trim() || undefined,
      attachLogs: target.email.attachLogs,
    };
  }
  if (target.type === NotificationProviderType.webhook) {
    input.webhook = {
      url: target.webhook.url.trim() || undefined,
      headers: target.webhook.clearHeaders
        ? {}
        : parseHeaders(target.webhook.headers),
      hmacSecret: target.webhook.hmacSecret.trim() || undefined,
      clearHeaders: target.webhook.clearHeaders || undefined,
      clearHmacSecret: target.webhook.clearHmacSecret || undefined,
      allowInsecureHttp: target.webhook.allowInsecureHttp || undefined,
      allowPrivateNetwork: target.webhook.allowPrivateNetwork || undefined,
    };
  }
  if (target.type === NotificationProviderType.slack) {
    input.slack = {
      webhookUrl: target.slack.webhookUrl.trim() || undefined,
    };
  }
  if (target.type === NotificationProviderType.telegram) {
    input.telegram = {
      botToken: target.telegram.botToken.trim() || undefined,
      chatId: target.telegram.chatId.trim() || undefined,
    };
  }
  return input;
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

function providerIcon(type: NotificationProviderType) {
  return (
    PROVIDER_OPTIONS.find((provider) => provider.value === type)?.icon || Bell
  );
}

function testEventForTarget(
  draft: DraftSettings,
  target?: DraftTarget
): NotificationEventType {
  return (
    target?.events[0] ||
    draft.events[0] ||
    NotificationEventType.dag_run_failed
  );
}

function targetLabel(target: DraftTarget): string {
  return (
    target.name.trim() ||
    PROVIDER_OPTIONS.find((provider) => provider.value === target.type)
      ?.label ||
    'Target'
  );
}

function NotificationsTab({ fileName }: NotificationsTabProps) {
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [draft, setDraft] = useState<DraftSettings>(defaultDraft);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [testingTargetId, setTestingTargetId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<TestResult[]>([]);
  const [deleteIndex, setDeleteIndex] = useState<number | null>(null);
  const query = useMemo(
    () => `?remoteNode=${encodeURIComponent(remoteNode)}`,
    [remoteNode]
  );

  const fetchSettings = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/notifications${query}`,
        { headers: authHeaders() }
      );
      if (response.status === 404) {
        setDraft(defaultDraft());
        setTestResults([]);
        return;
      }
      if (!response.ok) {
        throw new Error(
          await readError(response, 'Failed to load notifications')
        );
      }
      const data = (await response.json()) as NotificationSettings;
      setDraft(draftFromAPI(data));
      setTestResults([]);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to load notifications'
      );
    } finally {
      setIsLoading(false);
    }
  }, [config.apiURL, fileName, query]);

  useEffect(() => {
    fetchSettings();
  }, [fetchSettings]);

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

  const addTarget = () => {
    setDraft((current) => ({
      ...current,
      targets: [
        ...current.targets,
        blankTarget(NotificationProviderType.email),
      ],
    }));
  };

  const saveSettings = async () => {
    setIsSaving(true);
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/notifications${query}`,
        {
          method: 'PUT',
          headers: authHeaders(),
          body: JSON.stringify({
            enabled: draft.enabled,
            events: draft.events,
            targets: draft.targets.map(targetInput),
          }),
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

  const testNotifications = async (target?: DraftTarget) => {
    const targetId = target?.id;
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
            eventType: testEventForTarget(draft, target),
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
    if (deleteIndex === null) return;
    setDraft((current) => ({
      ...current,
      targets: current.targets.filter((_, index) => index !== deleteIndex),
    }));
    setDeleteIndex(null);
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
            <Button variant="outline" size="sm" onClick={fetchSettings}>
              <RefreshCw className="h-4 w-4" />
              Refresh
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => testNotifications()}
              disabled={draft.targets.length === 0 || testingTargetId !== null}
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

      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">Targets</h3>
        <Button variant="outline" size="sm" onClick={addTarget}>
          <Plus className="h-4 w-4" />
          Add
        </Button>
      </div>

      {draft.targets.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-sm text-muted-foreground">
            No targets configured.
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {draft.targets.map((target, index) => {
            const Icon = providerIcon(target.type);
            return (
              <Card key={target.id || index}>
                <CardHeader className="grid-cols-[1fr_auto]">
                  <div className="flex min-w-0 items-center gap-2">
                    <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                    <CardTitle className="truncate text-sm">
                      {targetLabel(target)}
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
                      aria-label={`Toggle ${targetLabel(target)}`}
                    />
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => target.id && testNotifications(target)}
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
                      onClick={() => setDeleteIndex(index)}
                      aria-label={`Delete ${targetLabel(target)}`}
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

                  <div className="flex flex-wrap gap-2">
                    {EVENT_OPTIONS.map((event) => {
                      const checked = target.events.includes(event.value);
                      return (
                        <label
                          key={event.value}
                          className="flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs"
                        >
                          <Checkbox
                            checked={checked}
                            onCheckedChange={(value) =>
                              updateTarget(index, (current) => ({
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
                    {target.events.length > 0 && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          updateTarget(index, (current) => ({
                            ...current,
                            events: [],
                          }))
                        }
                      >
                        Inherit
                      </Button>
                    )}
                  </div>

                  {target.type === NotificationProviderType.email && (
                    <div className="grid gap-3 md:grid-cols-2">
                      <Input
                        value={target.email.to}
                        placeholder="To"
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            email: { ...current.email, to: event.target.value },
                          }))
                        }
                      />
                      <Input
                        value={target.email.from}
                        placeholder="From"
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            email: {
                              ...current.email,
                              from: event.target.value,
                            },
                          }))
                        }
                      />
                      <Input
                        value={target.email.cc}
                        placeholder="Cc"
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            email: { ...current.email, cc: event.target.value },
                          }))
                        }
                      />
                      <Input
                        value={target.email.bcc}
                        placeholder="Bcc"
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            email: {
                              ...current.email,
                              bcc: event.target.value,
                            },
                          }))
                        }
                      />
                      <Input
                        value={target.email.subjectPrefix}
                        placeholder="Subject prefix"
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            email: {
                              ...current.email,
                              subjectPrefix: event.target.value,
                            },
                          }))
                        }
                      />
                      <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
                        <Checkbox
                          checked={target.email.attachLogs}
                          onCheckedChange={(value) =>
                            updateTarget(index, (current) => ({
                              ...current,
                              email: {
                                ...current.email,
                                attachLogs: !!value,
                              },
                            }))
                          }
                        />
                        Attach logs
                      </label>
                    </div>
                  )}

                  {target.type === NotificationProviderType.webhook && (
                    <div className="space-y-3">
                      <Input
                        value={target.webhook.url}
                        placeholder={
                          target.webhook.urlConfigured
                            ? `URL configured (${target.webhook.urlPreview || 'saved'})`
                            : 'URL'
                        }
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            webhook: {
                              ...current.webhook,
                              url: event.target.value,
                            },
                          }))
                        }
                      />
                      {target.webhook.headerPreviews &&
                        Object.keys(target.webhook.headerPreviews).length >
                          0 && (
                          <div className="flex flex-wrap gap-2">
                            {Object.entries(target.webhook.headerPreviews).map(
                              ([key, value]) => (
                                <Badge key={key} variant="outline">
                                  {key}: {value}
                                </Badge>
                              )
                            )}
                          </div>
                        )}
                      <Textarea
                        value={target.webhook.headers}
                        placeholder="Header-Name: value"
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            webhook: {
                              ...current.webhook,
                              headers: event.target.value,
                            },
                          }))
                        }
                      />
                      <Input
                        type="password"
                        value={target.webhook.hmacSecret}
                        placeholder={
                          target.webhook.hmacSecretConfigured
                            ? 'HMAC secret configured'
                            : 'HMAC secret'
                        }
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            webhook: {
                              ...current.webhook,
                              hmacSecret: event.target.value,
                            },
                          }))
                        }
                      />
                      <div className="grid gap-2 md:grid-cols-2">
                        <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
                          <Checkbox
                            checked={target.webhook.clearHeaders}
                            onCheckedChange={(value) =>
                              updateTarget(index, (current) => ({
                                ...current,
                                webhook: {
                                  ...current.webhook,
                                  clearHeaders: !!value,
                                },
                              }))
                            }
                          />
                          Clear headers
                        </label>
                        <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
                          <Checkbox
                            checked={target.webhook.clearHmacSecret}
                            onCheckedChange={(value) =>
                              updateTarget(index, (current) => ({
                                ...current,
                                webhook: {
                                  ...current.webhook,
                                  clearHmacSecret: !!value,
                                },
                              }))
                            }
                          />
                          Clear HMAC
                        </label>
                        <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
                          <Checkbox
                            checked={target.webhook.allowInsecureHttp}
                            onCheckedChange={(value) =>
                              updateTarget(index, (current) => ({
                                ...current,
                                webhook: {
                                  ...current.webhook,
                                  allowInsecureHttp: !!value,
                                },
                              }))
                            }
                          />
                          Allow HTTP
                        </label>
                        <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
                          <Checkbox
                            checked={target.webhook.allowPrivateNetwork}
                            onCheckedChange={(value) =>
                              updateTarget(index, (current) => ({
                                ...current,
                                webhook: {
                                  ...current.webhook,
                                  allowPrivateNetwork: !!value,
                                },
                              }))
                            }
                          />
                          Allow private network
                        </label>
                      </div>
                    </div>
                  )}

                  {target.type === NotificationProviderType.slack && (
                    <Input
                      type="password"
                      value={target.slack.webhookUrl}
                      placeholder={
                        target.slack.webhookUrlConfigured
                          ? `Webhook URL configured (${target.slack.webhookUrlPreview || 'saved'})`
                          : 'Slack webhook URL'
                      }
                      onChange={(event) =>
                        updateTarget(index, (current) => ({
                          ...current,
                          slack: {
                            ...current.slack,
                            webhookUrl: event.target.value,
                          },
                        }))
                      }
                    />
                  )}

                  {target.type === NotificationProviderType.telegram && (
                    <div className="grid gap-3 md:grid-cols-2">
                      <Input
                        type="password"
                        value={target.telegram.botToken}
                        placeholder={
                          target.telegram.botTokenConfigured
                            ? `Bot token configured (${target.telegram.botTokenPreview || 'saved'})`
                            : 'Bot token'
                        }
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            telegram: {
                              ...current.telegram,
                              botToken: event.target.value,
                            },
                          }))
                        }
                      />
                      <Input
                        value={target.telegram.chatId}
                        placeholder="Chat ID"
                        onChange={(event) =>
                          updateTarget(index, (current) => ({
                            ...current,
                            telegram: {
                              ...current.telegram,
                              chatId: event.target.value,
                            },
                          }))
                        }
                      />
                    </div>
                  )}
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <ConfirmDialog
        title="Delete Target"
        buttonText="Delete"
        visible={deleteIndex !== null}
        dismissModal={() => setDeleteIndex(null)}
        onSubmit={removeTarget}
      >
        Delete{' '}
        {deleteIndex !== null && draft.targets[deleteIndex]
          ? targetLabel(draft.targets[deleteIndex])
          : 'target'}
        ?
      </ConfirmDialog>
    </div>
  );
}

export default NotificationsTab;
