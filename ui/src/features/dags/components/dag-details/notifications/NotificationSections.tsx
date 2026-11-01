import {
  AlertTriangle,
  ArrowRight,
  MoreHorizontal,
  Search,
  Bell,
  CheckCircle2,
  FlaskConical,
  Link2,
  Loader2,
  Plus,
  Settings,
  Trash2,
  XCircle,
} from 'lucide-react';
import { useEffect, useId, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';

import ConfirmDialog from '@/components/ui/confirm-dialog';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from '@/components/ui/dropdown-menu';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
  CHANNEL_PROVIDER_OPTIONS,
  DEFAULT_COMMAND_PLACEHOLDER,
  DEFAULT_MESSAGE_TEMPLATE,
  DEFAULT_SUBJECT_TEMPLATE,
  DeliveryDraft,
  deliveryLabel,
  DraftChannel,
  DraftSettings,
  DraftSubscription,
  DraftTarget,
  EVENT_OPTIONS,
  isSlackIncomingWebhookURL,
  providerIcon,
  providerLabel,
  PROVIDER_OPTIONS,
  replaceDeliveryProvider,
  TestResult,
  WEBHOOK_BODY_TEMPLATE_PLACEHOLDER,
} from './notificationDrafts';
import type { EffectiveNotificationRoute } from './useNotificationSettings';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import {
  useCanManageCommandChannels,
  useCommandChannelsEnabled,
} from '@/contexts/AuthContext';

type ProviderFieldsProps = {
  draft: DeliveryDraft;
  onChange: (next: DeliveryDraft) => void;
};

/**
 * Provider fields adapt to the width of their own box, as the same fields sit
 * in the narrow Workflow panel and in a dialog.
 */
function ProviderFields(props: ProviderFieldsProps) {
  return (
    <div className="@container">
      <ProviderFieldsBody {...props} />
    </div>
  );
}

function ProviderFieldsBody({ draft, onChange }: ProviderFieldsProps) {
  const { ts } = useI18n();
  const fieldId = useId();
  const update = (patch: Partial<DeliveryDraft>) =>
    onChange({ ...draft, ...patch });

  if (draft.type === NotificationProviderType.email) {
    return (
      <div className="grid gap-3 @xl:grid-cols-2">
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'To'} />
          </span>
          <I18nProps>
            <Input
              value={draft.email.to}
              placeholder="To"
              onChange={(event) =>
                update({ email: { ...draft.email, to: event.target.value } })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'From'} />
          </span>
          <I18nProps>
            <Input
              value={draft.email.from}
              placeholder="From"
              onChange={(event) =>
                update({ email: { ...draft.email, from: event.target.value } })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Cc'} />
          </span>
          <I18nProps>
            <Input
              value={draft.email.cc}
              placeholder="Cc"
              onChange={(event) =>
                update({ email: { ...draft.email, cc: event.target.value } })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Bcc'} />
          </span>
          <I18nProps>
            <Input
              value={draft.email.bcc}
              placeholder="Bcc"
              onChange={(event) =>
                update({ email: { ...draft.email, bcc: event.target.value } })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Subject prefix'} />
          </span>
          <I18nProps>
            <Input
              value={draft.email.subjectPrefix}
              placeholder="Subject prefix"
              onChange={(event) =>
                update({
                  email: { ...draft.email, subjectPrefix: event.target.value },
                })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2 @xl:col-span-2">
          <span className="text-sm font-medium">
            <I18nText text={'Email subject template'} />
          </span>
          <I18nProps>
            <Textarea
              className="@xl:col-span-2"
              aria-label="Email subject template"
              value={draft.email.subjectTemplate}
              placeholder={DEFAULT_SUBJECT_TEMPLATE}
              onChange={(event) =>
                update({
                  email: {
                    ...draft.email,
                    subjectTemplate: event.target.value,
                  },
                })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2 @xl:col-span-2">
          <span className="text-sm font-medium">
            <I18nText text={'Email body template'} />
          </span>
          <I18nProps>
            <Textarea
              className="min-h-24 py-1 @xl:col-span-2"
              aria-label="Email body template"
              value={draft.email.bodyTemplate}
              placeholder={DEFAULT_MESSAGE_TEMPLATE}
              onChange={(event) =>
                update({
                  email: { ...draft.email, bodyTemplate: event.target.value },
                })
              }
            />
          </I18nProps>
        </label>
        <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
          <Checkbox
            checked={draft.email.attachLogs}
            onCheckedChange={(value) =>
              update({
                email: { ...draft.email, attachLogs: !!value },
              })
            }
          />
          <I18nText text={'Attach logs'} />
        </label>
      </div>
    );
  }

  if (draft.type === NotificationProviderType.webhook) {
    const hasSlackURL =
      isSlackIncomingWebhookURL(draft.webhook.url) ||
      isSlackIncomingWebhookURL(draft.webhook.urlPreview || '');
    return (
      <div className="space-y-3">
        <div className="space-y-2">
          <Label htmlFor={`${fieldId}-webhook-url`}>
            <I18nText text={'Webhook endpoint URL'} />
          </Label>
          <Input
            id={`${fieldId}-webhook-url`}
            value={draft.webhook.url}
            placeholder={
              draft.webhook.urlConfigured
                ? ts('URL configured ({preview})', {
                    preview: draft.webhook.urlPreview || ts('saved'),
                  })
                : 'https://example.com/webhook'
            }
            onChange={(event) =>
              update({
                webhook: { ...draft.webhook, url: event.target.value },
              })
            }
          />
        </div>
        {hasSlackURL && (
          <div className="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
            <I18nText
              text={
                'This is a Slack Incoming Webhook URL. Select Slack as the provider.'
              }
            />
          </div>
        )}
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
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Headers'} />
          </span>
          <I18nProps>
            <Textarea
              value={draft.webhook.headers}
              placeholder="Header-Name: value"
              onChange={(event) =>
                update({
                  webhook: { ...draft.webhook, headers: event.target.value },
                })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'HMAC secret'} />
          </span>
          <I18nProps>
            <Input
              type="password"
              value={draft.webhook.hmacSecret}
              placeholder={
                draft.webhook.hmacSecretConfigured
                  ? ts('HMAC secret configured')
                  : ts('HMAC secret')
              }
              onChange={(event) =>
                update({
                  webhook: { ...draft.webhook, hmacSecret: event.target.value },
                })
              }
            />
          </I18nProps>
        </label>
        <div className="space-y-2">
          <Label htmlFor={`${fieldId}-webhook-message-template`}>
            <I18nText text={'Webhook message template'} />
          </Label>
          <Textarea
            id={`${fieldId}-webhook-message-template`}
            className="min-h-24 py-1"
            value={draft.webhook.messageTemplate}
            placeholder={DEFAULT_MESSAGE_TEMPLATE}
            onChange={(event) =>
              update({
                webhook: {
                  ...draft.webhook,
                  messageTemplate: event.target.value,
                },
              })
            }
          />
          <p className="text-xs text-muted-foreground">
            <I18nTemplate
              text="Supports tokens such as {dag}, {status}, {error}, and {link}."
              values={{
                dag: <code>{'{{dag.name}}'}</code>,
                status: <code>{'{{run.status}}'}</code>,
                error: <code>{'{{run.error}}'}</code>,
                link: <code>{'{{run.link}}'}</code>,
              }}
            />
          </p>
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${fieldId}-webhook-body-template`}>
            <I18nText text={'Webhook JSON body template'} />
          </Label>
          <Textarea
            id={`${fieldId}-webhook-body-template`}
            className="min-h-24 py-1 font-mono"
            value={draft.webhook.bodyTemplate}
            placeholder={WEBHOOK_BODY_TEMPLATE_PLACEHOLDER}
            onChange={(event) =>
              update({
                webhook: {
                  ...draft.webhook,
                  bodyTemplate: event.target.value,
                },
              })
            }
          />
          <p className="text-xs text-muted-foreground">
            <I18nText
              text={
                'Optional. Replaces the default webhook payload and must render as valid JSON. Supports the message-template tokens plus'
              }
            />{' '}
            <code>{'{{message}}'}</code>
            <I18nText
              text={'. Leave blank to keep the default Dagu payload.'}
            />
          </p>
          <div className="rounded-md border border-border bg-muted/30 px-3 py-2 text-xs">
            <div className="mb-1 text-muted-foreground">
              <I18nText text={'Example'} />
            </div>
            <code>{'{"text": "{{message}}"}'}</code>
          </div>
        </div>
        <div className="grid gap-2 @xl:grid-cols-2">
          <label className="flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm">
            <Checkbox
              checked={draft.webhook.clearHeaders}
              onCheckedChange={(value) =>
                update({
                  webhook: { ...draft.webhook, clearHeaders: !!value },
                })
              }
            />
            <I18nText text={'Clear headers'} />
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
            <I18nText text={'Clear HMAC'} />
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
            <I18nText text={'Allow HTTP'} />
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
            <I18nText text={'Allow private network'} />
          </label>
        </div>
      </div>
    );
  }

  if (draft.type === NotificationProviderType.slack) {
    return (
      <div className="space-y-3">
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Slack webhook URL'} />
          </span>
          <I18nProps>
            <Input
              type="password"
              value={draft.slack.webhookUrl}
              placeholder={
                draft.slack.webhookUrlConfigured
                  ? ts('Webhook URL configured ({preview})', {
                      preview: draft.slack.webhookUrlPreview || ts('saved'),
                    })
                  : ts('Slack webhook URL')
              }
              onChange={(event) =>
                update({
                  slack: { ...draft.slack, webhookUrl: event.target.value },
                })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Slack message template'} />
          </span>
          <I18nProps>
            <Textarea
              className="min-h-24 py-1"
              aria-label="Slack message template"
              value={draft.slack.messageTemplate}
              placeholder={DEFAULT_MESSAGE_TEMPLATE}
              onChange={(event) =>
                update({
                  slack: {
                    ...draft.slack,
                    messageTemplate: event.target.value,
                  },
                })
              }
            />
          </I18nProps>
        </label>
      </div>
    );
  }

  if (draft.type === NotificationProviderType.command) {
    return (
      <div className="space-y-2">
        <Label htmlFor={`${fieldId}-command`}>
          <I18nText text={'Command'} />
        </Label>
        <Textarea
          id={`${fieldId}-command`}
          className="min-h-24 py-1 font-mono text-xs"
          required
          spellCheck={false}
          value={draft.command.command}
          placeholder={DEFAULT_COMMAND_PLACEHOLDER}
          onChange={(event) =>
            update({
              command: { command: event.target.value },
            })
          }
        />
        <p className="text-xs text-muted-foreground">
          <I18nText
            text={
              'The Dagu server runs this command. Unix uses sh -c and Windows uses PowerShell. The server stops it after 30 seconds, and a non-zero exit fails delivery.'
            }
          />
        </p>
        <p className="text-xs text-muted-foreground">
          <I18nTemplate
            text="The command runs as written. Run values are environment variables, such as {dagEnv}, {statusEnv}, {errorEnv}, DAG_RUN_ID, and RUN_URL. Quote them, as in {quotedError}. {logEnv} is a temporary file with up to 1 MiB of the run log, including lifecycle-handler output, and is removed after the command exits. On Windows, PowerShell reads these as {windowsEnv} and so on."
            values={{
              dagEnv: <code>$DAG_NAME</code>,
              statusEnv: <code>$STATUS</code>,
              errorEnv: <code>$ERROR</code>,
              quotedError: <code>"$ERROR"</code>,
              logEnv: <code>$EXECUTION_LOG</code>,
              windowsEnv: <code>$env:ERROR</code>,
            }}
          />
        </p>
      </div>
    );
  }

  if (draft.type === NotificationProviderType.teams) {
    return (
      <div className="space-y-3">
        <div className="rounded-md border border-border bg-muted/30 px-3 py-2 text-sm text-muted-foreground">
          <I18nText
            text={
              'Create an incoming-webhook Workflow in Teams, select its destination, save it, and copy the generated URL. Dagu sends the compatible MessageCard format automatically.'
            }
          />{' '}
          <a
            className="font-medium text-primary underline-offset-4 hover:underline"
            href="https://support.microsoft.com/en-US/Workflows/send-messages-in-teams-using-incoming-webhooks"
            target="_blank"
            rel="noreferrer"
          >
            <I18nText text={'Open Microsoft setup guide'} />
          </a>
          .
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${fieldId}-teams-webhook-url`}>
            <I18nText text={'Teams webhook URL'} />
          </Label>
          <Input
            id={`${fieldId}-teams-webhook-url`}
            type="password"
            value={draft.teams.webhookUrl}
            placeholder={
              draft.teams.webhookUrlConfigured
                ? ts('Webhook URL configured ({preview})', {
                    preview: draft.teams.webhookUrlPreview || ts('saved'),
                  })
                : 'https://...'
            }
            onChange={(event) =>
              update({
                teams: { ...draft.teams, webhookUrl: event.target.value },
              })
            }
          />
          <p className="text-xs text-muted-foreground">
            <I18nText
              text={
                'Only HTTPS URLs are accepted. A saved URL remains configured unless it is replaced.'
              }
            />
          </p>
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${fieldId}-teams-message-template`}>
            <I18nText text={'Teams message template'} />
          </Label>
          <Textarea
            id={`${fieldId}-teams-message-template`}
            className="min-h-24 py-1"
            value={draft.teams.messageTemplate}
            placeholder={DEFAULT_MESSAGE_TEMPLATE}
            onChange={(event) =>
              update({
                teams: {
                  ...draft.teams,
                  messageTemplate: event.target.value,
                },
              })
            }
          />
          <p className="text-xs text-muted-foreground">
            <I18nTemplate
              text="Supports tokens such as {dag}, {status}, {error}, and {link}."
              values={{
                dag: <code>{'{{dag.name}}'}</code>,
                status: <code>{'{{run.status}}'}</code>,
                error: <code>{'{{run.error}}'}</code>,
                link: <code>{'{{run.link}}'}</code>,
              }}
            />
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="grid gap-3 @xl:grid-cols-2">
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Bot token'} />
          </span>
          <I18nProps>
            <Input
              type="password"
              value={draft.telegram.botToken}
              placeholder={
                draft.telegram.botTokenConfigured
                  ? ts('Bot token configured ({preview})', {
                      preview: draft.telegram.botTokenPreview || ts('saved'),
                    })
                  : ts('Bot token')
              }
              onChange={(event) =>
                update({
                  telegram: { ...draft.telegram, botToken: event.target.value },
                })
              }
            />
          </I18nProps>
        </label>
        <label className="block space-y-2">
          <span className="text-sm font-medium">
            <I18nText text={'Chat ID'} />
          </span>
          <I18nProps>
            <Input
              value={draft.telegram.chatId}
              placeholder="Chat ID"
              onChange={(event) =>
                update({
                  telegram: { ...draft.telegram, chatId: event.target.value },
                })
              }
            />
          </I18nProps>
        </label>
      </div>
      <label className="block space-y-2">
        <span className="text-sm font-medium">
          <I18nText text={'Telegram topic ID'} />
        </span>
        <I18nProps>
          <Input
            aria-label="Telegram topic ID"
            value={draft.telegram.topicId}
            placeholder="Topic ID (optional, for forum groups)"
            onChange={(event) =>
              update({
                telegram: { ...draft.telegram, topicId: event.target.value },
              })
            }
          />
        </I18nProps>
      </label>
      <label className="block space-y-2">
        <span className="text-sm font-medium">
          <I18nText text={'Telegram message template'} />
        </span>
        <I18nProps>
          <Textarea
            className="min-h-24 py-1"
            aria-label="Telegram message template"
            value={draft.telegram.messageTemplate}
            placeholder={DEFAULT_MESSAGE_TEMPLATE}
            onChange={(event) =>
              update({
                telegram: {
                  ...draft.telegram,
                  messageTemplate: event.target.value,
                },
              })
            }
          />
        </I18nProps>
      </label>
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
            <I18nText text={event.label} />
          </label>
        );
      })}
      {events.length > 0 && (
        <Button variant="ghost" size="sm" onClick={() => onChange([])}>
          <I18nText text={'Use workflow events'} />
        </Button>
      )}
    </div>
  );
}

function EventSummary({
  events,
  fallback,
}: {
  events: NotificationEventType[];
  fallback: string;
}) {
  const labels = EVENT_OPTIONS.filter((event) =>
    events.includes(event.value)
  ).map((event) => event.label);

  if (labels.length === 0) {
    return <I18nText text={fallback} />;
  }

  return labels.map((label, index) => (
    <span key={label}>
      {index > 0 ? ', ' : ''}
      <I18nText text={label} />
    </span>
  ));
}

type NotificationOverviewCardProps = {
  draft: DraftSettings;
  isDAGConfigured: boolean;
  hasDAGDestinations: boolean;
  hasUnsavedChanges: boolean;
  inheritedSourceLabel: string;
  error: string | null;
  notice: string | null;
  testResults: TestResult[];
  onEnabledChange: (enabled: boolean) => void;
  onEventsChange: (events: NotificationEventType[]) => void;
};

export function NotificationOverviewCard({
  draft,
  isDAGConfigured,
  hasDAGDestinations,
  hasUnsavedChanges,
  inheritedSourceLabel,
  error,
  notice,
  testResults,
  onEnabledChange,
  onEventsChange,
}: NotificationOverviewCardProps) {
  return (
    <Card>
      <CardHeader className="grid-cols-[minmax(0,1fr)_auto]">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Bell className="h-4 w-4 shrink-0 text-muted-foreground" />
          <CardTitle className="text-sm">
            <I18nText text={'Notification source'} />
          </CardTitle>
          <Badge variant={isDAGConfigured ? 'success' : 'default'}>
            {isDAGConfigured ? (
              <I18nText text={'Workflow override'} />
            ) : (
              <I18nText text={'Inherited'} />
            )}
          </Badge>
          {isDAGConfigured && (
            <Badge variant={draft.enabled ? 'success' : 'default'}>
              {draft.enabled ? (
                <I18nText text={'Override on'} />
              ) : (
                <I18nText text={'Override off'} />
              )}
            </Badge>
          )}
          {hasUnsavedChanges && (
            <Badge variant="warning">
              <I18nText text={'Unsaved changes'} />
            </Badge>
          )}
        </div>
        <div className="flex items-center justify-end">
          {isDAGConfigured && (
            <I18nProps>
              <Switch
                checked={draft.enabled}
                onCheckedChange={onEnabledChange}
                aria-label="Toggle notifications"
              />
            </I18nProps>
          )}
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

        {isDAGConfigured ? (
          <div className="space-y-2">
            <div className="text-sm font-medium text-foreground">
              <I18nText text={'Send notifications when this workflow is'} />
            </div>
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
                            : draft.events.filter(
                                (item) => item !== event.value
                              )
                        )
                      }
                    />
                    {event.label}
                  </label>
                );
              })}
            </div>
            <div className="text-xs text-muted-foreground">
              <I18nText
                text={
                  'This workflow override replaces workspace and Global rules for future runs. Send test verifies delivery now.'
                }
              />
            </div>
            {!hasDAGDestinations && (
              <div className="rounded-md border border-warning/20 bg-warning/10 px-3 py-2 text-sm text-foreground">
                <I18nText
                  text={
                    'This workflow override has no destinations. Inherited rules are not used while the override exists.'
                  }
                />
              </div>
            )}
            {hasUnsavedChanges && (
              <div className="text-xs text-muted-foreground">
                <I18nText
                  text={
                    'Save changes before leaving this page or sending a test.'
                  }
                />
              </div>
            )}
          </div>
        ) : (
          <div>
            <div className="text-sm font-medium text-foreground">
              <I18nText
                text="This workflow inherits {source}."
                values={{ source: inheritedSourceLabel }}
              />
            </div>
            <div className="mt-1 text-sm text-muted-foreground">
              <I18nText
                text={
                  'Create a workflow override only when this workflow needs different events or destinations. The effective order is workflow, then workspace, then Global.'
                }
              />
            </div>
          </div>
        )}

        {testResults.length > 0 && (
          <div className="grid gap-2 @xl:grid-cols-2">
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
                <div className="min-w-0 flex-1">
                  <div className="truncate">
                    {result.targetName || result.provider}
                  </div>
                  {result.error && (
                    <div className="truncate text-xs text-destructive">
                      {result.error}
                    </div>
                  )}
                </div>
                <Badge variant={result.delivered ? 'success' : 'error'}>
                  {result.delivered ? (
                    <I18nText text={'Delivered'} />
                  ) : (
                    <I18nText text={'Failed'} />
                  )}
                </Badge>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

type InheritedNotificationRoutesCardProps = {
  sourceLabel: string;
  routes: EffectiveNotificationRoute[];
  manageRulesHref: string;
};

export function InheritedNotificationRoutesCard({
  sourceLabel,
  routes,
  manageRulesHref,
}: InheritedNotificationRoutesCardProps) {
  const enabledRoutes = routes.filter(
    (route) => route.enabled && route.channelEnabled
  );

  return (
    <Card>
      <CardHeader className="grid-cols-[minmax(0,1fr)_auto]">
        <div className="flex min-w-0 items-center gap-2">
          <Link2 className="h-4 w-4 shrink-0 text-muted-foreground" />
          <CardTitle className="truncate text-sm">
            <I18nText text={'Effective Inherited Routes'} />
          </CardTitle>
          <Badge variant={enabledRoutes.length > 0 ? 'success' : 'default'}>
            {sourceLabel}
          </Badge>
        </div>
        <Button asChild variant="outline" size="sm">
          <Link to={manageRulesHref}>
            <Settings className="h-4 w-4" />
            <I18nText text={'Manage rules'} />
          </Link>
        </Button>
      </CardHeader>
      <CardContent>
        {routes.length === 0 ? (
          <div className="text-sm text-muted-foreground">
            <I18nText
              text={'No inherited route is configured for this workflow.'}
            />
          </div>
        ) : (
          <div className="divide-y divide-border rounded-md border border-border">
            {routes.map((route) => {
              const Icon = providerIcon(route.provider);
              const active = route.enabled && route.channelEnabled;
              return (
                <div
                  key={route.id}
                  className="flex flex-col gap-2 px-3 py-3 text-sm @xl:flex-row @xl:items-center @xl:justify-between"
                >
                  <div className="flex min-w-0 items-center gap-2">
                    <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                    <span className="truncate font-medium">
                      {route.channelName}
                    </span>
                    <Badge variant={active ? 'success' : 'default'}>
                      {active ? (
                        <I18nText text={'Active'} />
                      ) : (
                        <I18nText text={'Inactive'} />
                      )}
                    </Badge>
                  </div>
                  <div className="text-xs text-muted-foreground">
                    <EventSummary events={route.events} fallback="No events" />
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

/**
 * Explains why the current user cannot add, toggle, test, or edit local command
 * channels on the selected node, or returns undefined when they can.
 */
export function useCommandChannelLockReason(): string | undefined {
  const remoteNode = useRemoteNode();
  const enabled = useCommandChannelsEnabled();
  const canManage = useCanManageCommandChannels();
  // A remote node checks its own role and permissions, which this server's
  // configuration does not describe, so its API decides instead.
  if (canManage || remoteNode !== 'local') {
    return undefined;
  }
  return enabled
    ? 'Only admins can change or test local command channels.'
    : 'This server does not run local commands. Enable permissions.write_dags and permissions.run_dags to deliver to this channel.';
}

type NotificationChannelsSectionProps = {
  channels: DraftChannel[];
  onSave: (channel: DraftChannel) => Promise<void>;
  onDelete: (channel: DraftChannel) => Promise<void>;
  onTest: (channelId: string) => Promise<TestResult | undefined>;
};

function channelDestination(channel: DraftChannel): string {
  switch (channel.type) {
    case NotificationProviderType.email:
      return channel.email.to;
    case NotificationProviderType.slack:
    case NotificationProviderType.teams:
      return 'Incoming webhook';
    case NotificationProviderType.telegram:
      return 'Bot destination';
    case NotificationProviderType.webhook:
      try {
        return new URL(channel.webhook.urlPreview || '').hostname;
      } catch {
        return 'Webhook destination';
      }
    case NotificationProviderType.command:
      return channel.command.command.split('\n', 1)[0] ?? '';
  }
}

function ChannelListRow({
  channel,
  lockedReason,
  onEdit,
  onDelete,
  onSave,
  onTest,
}: {
  channel: DraftChannel;
  // lockedReason explains why the channel cannot be toggled, tested, or edited.
  lockedReason?: string;
  onEdit: () => void;
  onDelete: () => void;
  onSave: NotificationChannelsSectionProps['onSave'];
  onTest: NotificationChannelsSectionProps['onTest'];
}) {
  const { ts } = useI18n();
  const Icon = providerIcon(channel.type);
  const label = deliveryLabel(channel);
  const [pending, setPending] = useState<'toggle' | 'test' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [delivered, setDelivered] = useState(false);
  useEffect(() => {
    setDelivered(false);
    setError(null);
  }, [channel]);

  const toggle = async (enabled: boolean) => {
    setPending('toggle');
    setError(null);
    try {
      await onSave({ ...channel, enabled });
    } catch (error) {
      setError(
        error instanceof Error ? error.message : ts('Failed to save channel')
      );
    } finally {
      setPending(null);
    }
  };
  const test = async () => {
    setPending('test');
    setError(null);
    setDelivered(false);
    try {
      const result = await onTest(channel.id!);
      if (!result?.delivered) {
        throw new Error(result?.error || ts('Delivery failed'));
      }
      setDelivered(true);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : ts('Failed to send test notification')
      );
    } finally {
      setPending(null);
    }
  };

  return (
    <li
      aria-label={label}
      className="flex flex-col gap-4 p-4 lg:flex-row lg:items-center lg:justify-between"
    >
      <div className="flex min-w-0 items-start gap-4">
        <span className="flex size-12 shrink-0 items-center justify-center rounded-lg bg-muted text-primary">
          <Icon className="size-6" />
        </span>
        <div className="min-w-0 space-y-1">
          <h3 className="break-words text-sm font-medium">{label}</h3>
          <p className="break-words text-sm text-muted-foreground">
            <I18nText text={providerLabel(channel.type)} /> ·{' '}
            <I18nText text={channelDestination(channel)} />
          </p>
          {delivered && (
            <p
              role="status"
              className="flex items-center gap-2 text-sm text-success"
            >
              <CheckCircle2 className="size-4" />
              <I18nText text={'Test delivered'} />
            </p>
          )}
          {error && (
            <p role="alert" className="break-words text-sm text-destructive">
              {error}
            </p>
          )}
          {lockedReason && (
            <p className="break-words text-sm text-warning">
              <I18nText text={lockedReason} />
            </p>
          )}
        </div>
      </div>
      <div className="flex shrink-0 flex-wrap items-center gap-3 sm:gap-4">
        <label className="flex min-h-9 items-center gap-3 text-sm text-muted-foreground">
          <I18nText text={channel.enabled ? 'Enabled' : 'Disabled'} />
          <Switch
            checked={channel.enabled}
            disabled={pending !== null || lockedReason !== undefined}
            onCheckedChange={toggle}
            aria-label={ts('Toggle {title}', { title: label })}
          />
          {pending === 'toggle' && <Loader2 className="size-4 animate-spin" />}
        </label>
        <Button
          variant="outline"
          className="px-5"
          disabled={pending !== null || lockedReason !== undefined}
          onClick={test}
        >
          {pending === 'test' && <Loader2 className="size-4 animate-spin" />}
          <I18nText text={pending === 'test' ? 'Sending...' : 'Test'} />
        </Button>
        <Button
          variant="outline"
          className="px-5"
          disabled={pending !== null || lockedReason !== undefined}
          onClick={onEdit}
        >
          <I18nText text={'Edit'} />
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              disabled={pending !== null}
              aria-label={ts('Channel actions for {channel}', {
                channel: label,
              })}
            >
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onSelect={onDelete}
              className="gap-2 text-destructive"
            >
              <Trash2 className="size-4" />
              <I18nText text={'Delete channel'} />
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </li>
  );
}

export function NotificationChannelsSection({
  channels,
  onSave,
  onDelete,
  onTest,
}: NotificationChannelsSectionProps) {
  const { ts } = useI18n();
  const fieldId = useId();
  const [search, setSearch] = useState('');
  const [editor, setEditor] = useState<DraftChannel | null>(null);
  const [deleting, setDeleting] = useState<DraftChannel | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const commandLockReason = useCommandChannelLockReason();
  const providerOptions = CHANNEL_PROVIDER_OPTIONS.filter(
    (provider) =>
      provider.value !== NotificationProviderType.command ||
      commandLockReason === undefined
  );
  const editorIncomplete =
    !editor?.name.trim() ||
    (editor.type === NotificationProviderType.command &&
      !editor.command.command.trim());
  const query = search.trim().toLocaleLowerCase();
  const filteredChannels = channels.filter((channel) =>
    [
      deliveryLabel(channel),
      ts(providerLabel(channel.type)),
      ts(channelDestination(channel)),
    ].some((value) => value.toLocaleLowerCase().includes(query))
  );
  const edit = (channel: DraftChannel) => {
    setError(null);
    setEditor(channel);
  };
  const save = async () => {
    if (!editor) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await onSave(editor);
      setEditor(null);
      setSearch('');
    } catch (error) {
      setError(
        error instanceof Error ? error.message : ts('Failed to save channel')
      );
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!deleting) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await onDelete(deleting);
      setDeleting(null);
    } catch (error) {
      setError(
        error instanceof Error ? error.message : ts('Failed to delete channel')
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="space-y-4" aria-label={ts('Notification Channels')}>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-2">
          <div className="flex items-center gap-3">
            <h2 className="text-base font-semibold">
              <I18nText text={'Channels'} />
            </h2>
            <span className="text-sm text-muted-foreground">
              {ts(
                channels.length === 1 ? '{count} channel' : '{count} channels',
                { count: channels.length }
              )}
            </span>
          </div>
          <p className="text-sm text-muted-foreground">
            <I18nText
              text={
                'Create reusable destinations, then choose events in Rules.'
              }
            />
          </p>
        </div>
        <div className="flex items-center gap-4 sm:flex-col sm:items-end">
          <Button
            variant="primary"
            onClick={() => edit(blankChannel(NotificationProviderType.slack))}
          >
            <Plus className="size-4" />
            <I18nText text={'Add channel'} />
          </Button>
          <Link
            to="/notification-rules"
            className="inline-flex items-center gap-2 text-sm text-primary hover:underline"
          >
            <I18nText text={'View rules'} />
            <ArrowRight className="size-4" />
          </Link>
        </div>
      </div>
      <div className="relative max-w-sm">
        <Search className="pointer-events-none absolute left-3 top-3.5 size-4 text-muted-foreground" />
        <Input
          type="search"
          aria-label={ts('Search channels')}
          placeholder={ts('Search channels')}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          className="pl-10"
        />
      </div>
      {filteredChannels.length ? (
        <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border bg-card">
          {filteredChannels.map((channel) => (
            <ChannelListRow
              key={channel.id}
              channel={channel}
              lockedReason={
                channel.type === NotificationProviderType.command
                  ? commandLockReason
                  : undefined
              }
              onSave={onSave}
              onTest={onTest}
              onEdit={() => edit(channel)}
              onDelete={() => {
                setError(null);
                setDeleting(channel);
              }}
            />
          ))}
        </ul>
      ) : (
        <div className="rounded-lg border border-dashed border-border-strong px-6 py-10 text-center">
          <Bell className="mx-auto mb-3 size-7 text-muted-foreground" />
          <p className="text-sm font-medium">
            <I18nText
              text={
                channels.length
                  ? 'No matching channels'
                  : 'No channels configured.'
              }
            />
          </p>
          <p className="mt-2 text-sm text-muted-foreground">
            <I18nText
              text={
                channels.length
                  ? 'Try a different name or provider.'
                  : 'Add a channel to create your first notification destination.'
              }
            />
          </p>
        </div>
      )}
      <Dialog
        open={editor !== null}
        onOpenChange={(open) => {
          if (!open && !saving) {
            setEditor(null);
          }
        }}
      >
        <DialogContent
          className="flex max-h-[90dvh] w-[calc(100%-2rem)] max-w-2xl flex-col gap-0 overflow-hidden p-0"
          onInteractOutside={(event) => event.preventDefault()}
        >
          <DialogHeader className="border-b border-border px-6 py-5 pr-12">
            <DialogTitle>
              <I18nText text={editor?.id ? 'Edit channel' : 'Add channel'} />
            </DialogTitle>
            <DialogDescription>
              <I18nText
                text={'Save a destination, then use it in notification rules.'}
              />
            </DialogDescription>
          </DialogHeader>
          {editor && (
            <form
              className="flex min-h-0 flex-col"
              onSubmit={(event) => {
                event.preventDefault();
                void save();
              }}
            >
              <fieldset
                disabled={saving}
                className="min-h-0 space-y-5 overflow-y-auto p-6"
              >
                {error && (
                  <p role="alert" className="text-sm text-destructive">
                    {error}
                  </p>
                )}
                <div className="grid gap-4 sm:grid-cols-[1fr_180px]">
                  <div className="space-y-2">
                    <Label htmlFor={`${fieldId}-name`}>
                      <I18nText text={'Channel name'} />
                    </Label>
                    <Input
                      id={`${fieldId}-name`}
                      required
                      value={editor.name}
                      onChange={(event) =>
                        setEditor({ ...editor, name: event.target.value })
                      }
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor={`${fieldId}-provider`}>
                      <I18nText text={'Provider'} />
                    </Label>
                    <Select
                      value={editor.type}
                      disabled={saving}
                      onValueChange={(value) =>
                        setEditor({
                          ...replaceDeliveryProvider(
                            editor,
                            value as NotificationProviderType
                          ),
                          id: editor.id,
                        })
                      }
                    >
                      <SelectTrigger id={`${fieldId}-provider`}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {providerOptions.map((provider) => (
                          <SelectItem
                            key={provider.value}
                            value={provider.value}
                          >
                            <I18nText text={provider.label} />
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </div>
                <ProviderFields
                  draft={editor}
                  onChange={(next) => setEditor({ ...next, id: editor.id })}
                />
                <label className="flex min-h-9 items-center gap-3 text-sm">
                  <Switch
                    checked={editor.enabled}
                    disabled={saving}
                    onCheckedChange={(enabled) =>
                      setEditor({ ...editor, enabled })
                    }
                  />
                  <I18nText text={'Enabled'} />
                </label>
              </fieldset>
              <DialogFooter className="gap-2 border-t border-border px-6 py-4">
                <Button
                  type="button"
                  variant="outline"
                  disabled={saving}
                  onClick={() => setEditor(null)}
                >
                  <I18nText text={'Cancel'} />
                </Button>
                <Button
                  type="submit"
                  variant="primary"
                  disabled={saving || editorIncomplete}
                >
                  {saving && <Loader2 className="size-4 animate-spin" />}
                  <I18nText
                    text={editor.id ? 'Save changes' : 'Create channel'}
                  />
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        title={ts('Delete channel')}
        buttonText={ts('Delete')}
        visible={deleting !== null}
        dismissModal={() => {
          if (!saving) {
            setDeleting(null);
          }
        }}
        onSubmit={remove}
        submitDisabled={saving}
      >
        <p className="break-words text-sm">
          {ts('Delete {channel}?', {
            channel: deleting ? deliveryLabel(deleting) : '',
          })}
        </p>
        {error && (
          <p role="alert" className="mt-3 text-sm text-destructive">
            {error}
          </p>
        )}
      </ConfirmDialog>
    </section>
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
  const [expandedEventRows, setExpandedEventRows] = useState<Set<string>>(
    () => new Set()
  );
  const commandLockReason = useCommandChannelLockReason();
  const channelsById = useMemo(() => {
    const map = new Map<string, DraftChannel>();
    channels.forEach((channel) => {
      if (channel.id) {
        map.set(channel.id, channel);
      }
    });
    return map;
  }, [channels]);
  const toggleEventRow = (key: string) => {
    setExpandedEventRows((current) => {
      const next = new Set(current);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  return (
    <>
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">
          <I18nText text={'Send to'} />
        </h3>
        <div className="flex items-center gap-2">
          {manageChannelsHref && (
            <Button asChild variant="ghost" size="sm">
              <Link to={manageChannelsHref}>
                <Settings className="h-4 w-4" />
                <I18nText text={'Manage channels'} />
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
            <I18nText text={'Add channel'} />
          </Button>
        </div>
      </div>

      {draft.subscriptions.length === 0 ? (
        <Card>
          <CardContent className="space-y-2 py-8 text-sm text-muted-foreground">
            <div>
              <I18nText text={'No workflow override channels selected.'} />
            </div>
            <div>
              <I18nText
                text={
                  'Inherited rules are not used while this workflow override exists. Add a channel or reset to inherit.'
                }
              />
            </div>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {draft.subscriptions.map((subscription, index) => {
            const channel = channelsById.get(subscription.channelId);
            const Icon = providerIcon(channel?.type);
            const lockReason =
              channel?.type === NotificationProviderType.command
                ? commandLockReason
                : undefined;
            const usedChannelIds = new Set(
              draft.subscriptions
                .filter((_, subIndex) => subIndex !== index)
                .map((item) => item.channelId)
            );
            const rowKey =
              subscription.id ||
              subscription.channelId ||
              `subscription-${index}`;
            const eventsExpanded = expandedEventRows.has(rowKey);
            const hasCustomEvents = subscription.events.length > 0;
            return (
              <Card
                key={subscription.id || `${subscription.channelId}-${index}`}
              >
                <CardContent className="space-y-3 p-4">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-2">
                      <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                      <span className="truncate text-sm font-medium">
                        {channel?.name || subscription.channelId}
                      </span>
                      <Badge
                        variant={
                          subscription.enabled && channel?.enabled
                            ? 'success'
                            : 'default'
                        }
                      >
                        {subscription.enabled && channel?.enabled ? (
                          <I18nText text={'Enabled'} />
                        ) : (
                          <I18nText text={'Disabled'} />
                        )}
                      </Badge>
                      {!subscription.id && (
                        <Badge variant="warning">
                          <I18nText text={'Unsaved'} />
                        </Badge>
                      )}
                      {!channel && (
                        <Badge variant="error">
                          <I18nText text={'Missing'} />
                        </Badge>
                      )}
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
                        disabled={
                          !subscription.id ||
                          testingTargetId !== null ||
                          lockReason !== undefined
                        }
                      >
                        {testingTargetId === subscription.id ? (
                          <Loader2 className="h-4 w-4 animate-spin" />
                        ) : (
                          <FlaskConical className="h-4 w-4" />
                        )}
                        <I18nText text={'Send test'} />
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
                  </div>

                  {lockReason && (
                    <p className="text-sm text-warning">
                      <I18nText text={lockReason} />
                    </p>
                  )}
                  <div className="grid gap-3 @xl:grid-cols-[minmax(220px,320px)_minmax(0,1fr)]">
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
                              disabled={
                                !!item.id && usedChannelIds.has(item.id)
                              }
                            >
                              {item.name || providerLabel(item.type)}
                            </SelectItem>
                          ))}
                      </SelectContent>
                    </Select>

                    <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border px-3 py-2 text-sm">
                      <span className="text-muted-foreground">
                        <I18nText text={'Events:'} />{' '}
                        <EventSummary
                          events={subscription.events}
                          fallback={
                            subscription.events.length === 0
                              ? 'Same as workflow events'
                              : 'Custom events'
                          }
                        />
                      </span>
                      <div className="flex flex-wrap items-center gap-2">
                        {hasCustomEvents && (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() =>
                              onUpdate(index, (current) => ({
                                ...current,
                                events: [],
                              }))
                            }
                          >
                            <I18nText text={'Use workflow events'} />
                          </Button>
                        )}
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => toggleEventRow(rowKey)}
                        >
                          {eventsExpanded ? (
                            <I18nText text={'Hide events'} />
                          ) : (
                            <I18nText text={'Customize events'} />
                          )}
                        </Button>
                      </div>
                    </div>
                  </div>

                  {eventsExpanded && (
                    <EventFilterEditor
                      events={subscription.events}
                      onChange={(events) =>
                        onUpdate(index, (current) => ({
                          ...current,
                          events,
                        }))
                      }
                    />
                  )}
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}
    </>
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
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="ghost" size="sm" onClick={onAdd}>
          <Link2 className="h-4 w-4" />
          <I18nText text={'Add custom destination'} />
        </Button>
      </div>
    );
  }

  return (
    <>
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-foreground">
          <I18nText text={'Custom Destinations'} />
        </h3>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={onAdd}>
            <Plus className="h-4 w-4" />
            <I18nText text={'Add custom'} />
          </Button>
        </div>
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
                    {target.enabled ? (
                      <I18nText text={'Enabled'} />
                    ) : (
                      <I18nText text={'Disabled'} />
                    )}
                  </Badge>
                  {!target.id && (
                    <Badge variant="warning">
                      <I18nText text={'Unsaved'} />
                    </Badge>
                  )}
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
                    <I18nText text={'Send test'} />
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
                <div className="grid gap-3 @xl:grid-cols-[minmax(0,1fr)_180px]">
                  <I18nProps>
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
                  </I18nProps>
                  <Select
                    value={target.type}
                    onValueChange={(value) =>
                      onUpdate(index, (current) => {
                        const nextType = value as NotificationProviderType;
                        const next = replaceDeliveryProvider(current, nextType);
                        return {
                          ...next,
                          id: current.id,
                          events: current.events,
                        };
                      })
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
