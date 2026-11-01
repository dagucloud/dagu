// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Check, Copy, Loader2, RefreshCw } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  components,
  WebhookPublicDeliveryOutcome,
  WebhookPublicSender,
  WebhookPublicState,
} from '../../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import dayjs from '@/lib/dayjs';
import { formatBytes } from '@/lib/formatBytes';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

type WebhookDetails = components['schemas']['WebhookDetails'];
type WebhookPublicDelivery = components['schemas']['WebhookPublicDelivery'];

// ANYONE stands for a URL that takes requests without a sender's signature.
const ANYONE = 'anyone';
// REFRESH_INTERVAL_MS matches how often the scheduler rereads the webhooks.
const REFRESH_INTERVAL_MS = 10_000;

const senderLabels: Record<WebhookPublicSender, string> = {
  [WebhookPublicSender.github]: 'GitHub',
  [WebhookPublicSender.stripe]: 'Stripe',
  [WebhookPublicSender.slack]: 'Slack',
  [WebhookPublicSender.standard]: 'Standard Webhooks',
};

const outcomeLabels: Record<WebhookPublicDeliveryOutcome, string> = {
  [WebhookPublicDeliveryOutcome.started]: 'Started',
  [WebhookPublicDeliveryOutcome.waiting]: 'Waiting',
  [WebhookPublicDeliveryOutcome.rejected]: 'Rejected',
  [WebhookPublicDeliveryOutcome.skipped]: 'Skipped',
  [WebhookPublicDeliveryOutcome.ignored]: 'Ignored',
  [WebhookPublicDeliveryOutcome.failed]: 'Failed',
};

interface WebhookPublicURLCardProps {
  fileName: string;
  dagName: string;
  isAdmin: boolean;
  remoteNode: string;
  webhook: WebhookDetails;
  onWebhookChange: (webhook: WebhookDetails) => void;
  onSecretCreated: (secret: string) => void;
}

// formatSize shows whole bytes below a kilobyte.
function formatSize(bytes: number): string {
  return bytes < 1024 ? `${bytes} B` : formatBytes(bytes);
}

function parseEvents(text: string): string[] {
  return text
    .split(',')
    .map((event) => event.trim())
    .filter(Boolean);
}

function WebhookPublicURLCard({
  fileName,
  isAdmin,
  remoteNode,
  webhook,
  onWebhookChange,
  onSecretCreated,
}: WebhookPublicURLCardProps) {
  const { ts } = useI18n();
  const client = useClient();
  const settings = webhook.public;
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pendingAction, setPendingAction] = useState<
    'disable' | 'regenerate' | null
  >(null);
  const { copied, copy: handleCopy } = useCopyFeedback(1500);
  const [sender, setSender] = useState<string>(ANYONE);
  const [secret, setSecret] = useState('');
  const [events, setEvents] = useState('');

  useEffect(() => {
    setSender(settings?.sender ?? ANYONE);
    setSecret('');
    setEvents((settings?.events ?? []).join(', '));
  }, [settings?.sender, settings?.events]);

  const publicQuery = useMemo(
    () =>
      whenEnabled(!!settings, {
        params: {
          path: { fileName },
          query: { remoteNode },
        },
      }),
    [settings, fileName, remoteNode]
  );
  const {
    data: details,
    error: detailsError,
    mutate,
  } = useQuery('/dags/{fileName}/webhook/public', publicQuery, {
    refreshInterval: REFRESH_INTERVAL_MS,
  });

  const params = { path: { fileName }, query: { remoteNode } };

  const run = async (action: () => Promise<void>, failure: string) => {
    try {
      setIsSaving(true);
      setError(null);
      await action();
      await mutate();
    } catch (err) {
      setError(err instanceof Error ? err.message : failure);
    } finally {
      setIsSaving(false);
    }
  };

  const handleEnable = () =>
    run(async () => {
      const { data, error } = await client.POST(
        '/dags/{fileName}/webhook/public',
        { params }
      );
      if (error || !data) {
        throw new Error(error?.message || 'Failed to turn on the public URL');
      }
      onWebhookChange(data);
    }, 'Failed to turn on the public URL');

  const handleDisable = () =>
    run(async () => {
      const { data, error } = await client.DELETE(
        '/dags/{fileName}/webhook/public',
        { params }
      );
      if (error || !data) {
        throw new Error(error?.message || 'Failed to turn off the public URL');
      }
      onWebhookChange(data);
    }, 'Failed to turn off the public URL');

  const handleRegenerate = () =>
    run(async () => {
      const { data, error } = await client.POST(
        '/dags/{fileName}/webhook/public/regenerate',
        { params }
      );
      if (error || !data) {
        throw new Error(error?.message || 'Failed to replace the public URL');
      }
      onWebhookChange(data);
    }, 'Failed to replace the public URL');

  const handleSaveSender = (generateSecret: boolean) =>
    run(async () => {
      const chosen =
        sender === ANYONE ? undefined : (sender as WebhookPublicSender);
      const { data, error } = await client.PUT(
        '/dags/{fileName}/webhook/public/sender',
        {
          params,
          body: {
            sender: chosen,
            secret: chosen && !generateSecret ? secret.trim() : undefined,
            generateSecret: generateSecret || undefined,
            events: parseEvents(events),
          },
        }
      );
      if (error || !data) {
        throw new Error(error?.message || 'Failed to save the sender');
      }
      setSecret('');
      onWebhookChange(data.webhook);
      if (data.secret) {
        onSecretCreated(data.secret);
      }
    }, 'Failed to save the sender');

  const handleDismissMissed = () =>
    run(async () => {
      const { error } = await client.DELETE(
        '/dags/{fileName}/webhook/public/missed',
        { params }
      );
      if (error) {
        throw new Error(error.message || 'Failed to dismiss');
      }
    }, 'Failed to dismiss');

  const keepsSecret =
    !!settings?.secretConfigured && settings.sender === sender;
  const secretPlaceholder = keepsSecret
    ? 'Leave empty to keep the current secret'
    : sender === WebhookPublicSender.github
      ? 'Leave empty to have Dagu make one'
      : "Paste the sender's signing secret";
  const loadFailed = !!detailsError && !details;
  const missed = details?.missed;
  const missedCount = (missed?.expired ?? 0) + (missed?.refused ?? 0);

  return (
    <Card className="gap-0 py-0">
      <CardHeader className="pb-3 px-4 pt-3">
        <div className="flex items-center justify-between">
          <CardTitle className="text-sm">
            <I18nText text={'Public URL'} />
          </CardTitle>
          <I18nProps>
            <Switch
              aria-label="Public URL"
              checked={!!settings}
              disabled={!isAdmin || isSaving}
              onCheckedChange={(checked) =>
                checked ? void handleEnable() : setPendingAction('disable')
              }
            />
          </I18nProps>
        </div>
        <CardDescription className="text-xs">
          <I18nText
            text={
              'Receive webhooks from services on the internet, such as GitHub or Stripe, through Dagu Cloud. Deliveries wait there for up to 7 days while this server is offline.'
            }
          />
        </CardDescription>
      </CardHeader>
      <CardContent className="px-4 pb-3 pt-0 space-y-3">
        {error && <div className="text-xs text-destructive">{error}</div>}

        {!settings && !isAdmin && (
          <div className="text-xs text-muted-foreground">
            <I18nText text={'Only administrators can turn on a public URL.'} />
          </div>
        )}

        {settings && (
          <>
            {loadFailed ? (
              <div className="flex flex-wrap items-center gap-2 text-xs text-destructive">
                <I18nText text={'The public URL could not be loaded.'} />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void mutate()}
                >
                  <I18nText text={'Retry'} />
                </Button>
              </div>
            ) : (
              <PublicURLState details={details} />
            )}

            {!loadFailed && (
              <div>
                <div className="flex items-center justify-between mb-1">
                  <span className="text-xs text-muted-foreground">
                    <I18nText text={'URL'} />
                  </span>
                  {details?.url && (
                    <I18nProps>
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={copied ? 'Copied' : 'Copy'}
                        className="h-6 px-2 text-muted-foreground hover:text-foreground"
                        onClick={() => handleCopy(details.url!)}
                      >
                        {copied ? (
                          <Check className="h-3 w-3" />
                        ) : (
                          <Copy className="h-3 w-3" />
                        )}
                      </Button>
                    </I18nProps>
                  )}
                </div>
                {details?.url ? (
                  <div className="px-3 py-2 bg-accent rounded-md text-xs font-mono border overflow-x-auto">
                    <span className="text-muted-foreground">POST</span>{' '}
                    <span className="break-all">{details.url}</span>
                  </div>
                ) : (
                  <div className="text-xs text-muted-foreground">
                    <I18nText
                      text={
                        'The URL appears once this server connects to Dagu Cloud.'
                      }
                    />
                  </div>
                )}
              </div>
            )}

            <div className="space-y-2">
              <span className="text-xs text-muted-foreground">
                <I18nText text={'Sender'} />
              </span>
              {isAdmin ? (
                <>
                  <div className="grid gap-2 md:grid-cols-2">
                    <Select
                      value={sender}
                      onValueChange={setSender}
                      disabled={isSaving}
                    >
                      <I18nProps>
                        <SelectTrigger aria-label="Sender" className="h-8">
                          <SelectValue />
                        </SelectTrigger>
                      </I18nProps>
                      <SelectContent>
                        <SelectItem value={ANYONE}>
                          <I18nText text={'Anyone with the URL'} />
                        </SelectItem>
                        {Object.values(WebhookPublicSender).map((value) => (
                          <SelectItem key={value} value={value}>
                            {senderLabels[value]}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {sender !== ANYONE && (
                      <I18nProps>
                        <Input
                          type="password"
                          aria-label="Signing secret"
                          placeholder={secretPlaceholder}
                          className="h-8 text-sm"
                          autoComplete="off"
                          value={secret}
                          disabled={isSaving}
                          onChange={(event) => setSecret(event.target.value)}
                        />
                      </I18nProps>
                    )}
                  </div>
                  <span className="block text-xs text-muted-foreground">
                    <I18nText text={'Events'} />
                  </span>
                  <I18nProps>
                    <Input
                      aria-label="Events"
                      placeholder="All events"
                      className="h-8 text-sm"
                      value={events}
                      disabled={isSaving}
                      onChange={(event) => setEvents(event.target.value)}
                    />
                  </I18nProps>
                  <div className="text-xs text-muted-foreground">
                    <I18nTemplate
                      text="Separate events with commas, such as {example}. A name also takes the events under it."
                      values={{
                        example: (
                          <code className="bg-accent px-1 rounded-md border">
                            push, issues.opened
                          </code>
                        ),
                      }}
                    />
                  </div>
                  {sender === WebhookPublicSender.github && (
                    <div className="text-xs text-muted-foreground">
                      <I18nTemplate
                        text="In GitHub's webhook settings, set Content type to {type}."
                        values={{
                          type: (
                            <code className="bg-accent px-1 rounded-md border">
                              application/json
                            </code>
                          ),
                        }}
                      />
                    </div>
                  )}
                  <div className="flex flex-wrap gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={isSaving}
                      onClick={() => void handleSaveSender(false)}
                    >
                      {isSaving && (
                        <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
                      )}
                      <I18nText text={'Save sender'} />
                    </Button>
                    {sender === WebhookPublicSender.github && keepsSecret && (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={isSaving}
                        onClick={() => void handleSaveSender(true)}
                      >
                        <I18nText text={'New secret'} />
                      </Button>
                    )}
                  </div>
                </>
              ) : (
                <div className="text-xs">
                  {settings.sender ? (
                    senderLabels[settings.sender]
                  ) : (
                    <I18nText text={'Anyone with the URL'} />
                  )}
                  {' · '}
                  {settings.events.length > 0 ? (
                    settings.events.join(', ')
                  ) : (
                    <I18nText text={'All events'} />
                  )}
                </div>
              )}
            </div>

            {missedCount > 0 && missed && (
              <div className="rounded-md border bg-warning/10 px-3 py-2 text-xs space-y-2">
                <div>
                  <I18nText
                    text={
                      'Dagu Cloud dropped deliveries for this URL: {expired} waited more than 7 days, and {refused} arrived while 1,000 were waiting.'
                    }
                    values={{
                      expired: missed.expired,
                      refused: missed.refused,
                    }}
                  />
                </div>
                {isAdmin && (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={isSaving}
                    onClick={() => void handleDismissMissed()}
                  >
                    <I18nText text={'Dismiss'} />
                  </Button>
                )}
              </div>
            )}

            {!loadFailed && (
              <Deliveries
                fileName={fileName}
                deliveries={details?.deliveries ?? []}
              />
            )}

            {isAdmin && (
              <Button
                variant="outline"
                size="sm"
                disabled={isSaving}
                onClick={() => setPendingAction('regenerate')}
              >
                <RefreshCw className="h-3.5 w-3.5 mr-1" />
                <I18nText text={'New URL'} />
              </Button>
            )}
          </>
        )}
      </CardContent>

      <I18nProps>
        <ConfirmModal
          title={
            pendingAction === 'disable'
              ? 'Turn off public URL'
              : 'Replace public URL'
          }
          buttonText={pendingAction === 'disable' ? 'Turn off' : 'Replace'}
          visible={pendingAction !== null}
          dismissModal={() => setPendingAction(null)}
          onSubmit={() => {
            const action = pendingAction;
            setPendingAction(null);
            if (action === 'disable') {
              void handleDisable();
            } else if (action === 'regenerate') {
              void handleRegenerate();
            }
          }}
        >
          <p>
            {ts(
              pendingAction === 'disable'
                ? 'Dagu Cloud drops this URL and any deliveries waiting for it.'
                : 'The current URL stops working. Requests Dagu Cloud already took through it are still delivered. Give every sender the new URL.'
            )}
          </p>
        </ConfirmModal>
      </I18nProps>
    </Card>
  );
}

function PublicURLState({
  details,
}: {
  details?: components['schemas']['WebhookPublicDetails'];
}) {
  const { ts } = useI18n();
  switch (details?.state) {
    case WebhookPublicState.active:
      return null;
    case WebhookPublicState.overLimit:
      return (
        <div className="rounded-md border bg-warning/10 px-3 py-2 text-xs">
          <I18nText
            text={
              'Your plan allows {count} public URLs, and this one is not among them. Turn another off, or change the plan.'
            }
            values={{ count: details.maxHooks ?? 0 }}
          />
        </div>
      );
    case WebhookPublicState.error:
      return (
        <div className="text-xs text-destructive">
          {details.error ? ts(details.error) : null}
        </div>
      );
    default:
      return (
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="h-3 w-3 animate-spin" />
          <I18nText text={'Connecting to Dagu Cloud…'} />
        </div>
      );
  }
}

function Deliveries({
  fileName,
  deliveries,
}: {
  fileName: string;
  deliveries: WebhookPublicDelivery[];
}) {
  const remoteNode = useRemoteNode();
  return (
    <div className="space-y-1">
      <span className="text-xs text-muted-foreground">
        <I18nText text={'Recent deliveries'} />
      </span>
      {deliveries.length === 0 ? (
        <div className="text-xs text-muted-foreground">
          <I18nText text={'No deliveries yet.'} />
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="text-xs">
                <I18nText text={'Received'} />
              </TableHead>
              <TableHead className="hidden text-xs sm:table-cell">
                <I18nText text={'Event'} />
              </TableHead>
              <TableHead className="hidden text-xs sm:table-cell">
                <I18nText text={'Size'} />
              </TableHead>
              <TableHead className="text-xs">
                <I18nText text={'Result'} />
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {deliveries.map((delivery) => (
              <TableRow key={delivery.id}>
                <TableCell className="text-xs whitespace-nowrap">
                  {dayjs(delivery.receivedAt).format('MMM D, HH:mm:ss')}
                  {delivery.event && (
                    <div className="font-mono text-muted-foreground sm:hidden">
                      {delivery.event}
                    </div>
                  )}
                </TableCell>
                <TableCell className="hidden text-xs font-mono whitespace-nowrap sm:table-cell">
                  {delivery.event || '—'}
                </TableCell>
                <TableCell className="hidden text-xs whitespace-nowrap sm:table-cell">
                  {formatSize(delivery.size)}
                </TableCell>
                <TableCell className="text-xs sm:min-w-48">
                  <div>
                    <I18nText text={outcomeLabels[delivery.outcome]} />
                    {delivery.dagRunId && fileName && (
                      <>
                        {' · '}
                        <Link
                          className="font-mono text-primary hover:underline"
                          to={buildDAGPageURL({
                            fileName,
                            remoteNode,
                            run: delivery.dagRunId,
                          })}
                        >
                          {delivery.dagRunId.slice(0, 8)}
                        </Link>
                      </>
                    )}
                  </div>
                  {delivery.error && (
                    <div className="text-muted-foreground break-words">
                      {delivery.error}
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

export default WebhookPublicURLCard;
