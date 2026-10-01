// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Loader2, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import {
  components,
  WebhookAuthMode as WebhookAuthModeValue,
} from '../../../../api/v1/schema';
import { Badge } from '@/components/ui/badge';
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
import { useClient } from '../../../../hooks/api';
import dayjs from '../../../../lib/dayjs';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type WebhookDetails = components['schemas']['WebhookDetails'];
type WebhookProfileToken = components['schemas']['WebhookProfileToken'];

const MAX_TOKEN_NAME_LENGTH = 100;

interface WebhookProfileTokensCardProps {
  fileName: string;
  isAdmin: boolean;
  remoteNode: string;
  webhook: WebhookDetails;
  activeProfileNames: string[];
  onWebhookChange: (webhook: WebhookDetails) => void;
  onTokenCreated: (token: string) => void;
}

function WebhookProfileTokensCard({
  fileName,
  isAdmin,
  remoteNode,
  webhook,
  activeProfileNames,
  onWebhookChange,
  onTokenCreated,
}: WebhookProfileTokensCardProps) {
  const client = useClient();
  const [name, setName] = useState('');
  const [profile, setProfile] = useState('');
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pendingRevoke, setPendingRevoke] =
    useState<WebhookProfileToken | null>(null);

  // Remote nodes running an older version omit profileTokens.
  const tokens = webhook.profileTokens ?? [];
  const isHMACOnly = webhook.authMode === WebhookAuthModeValue.hmac_only;

  const handleCreate = async () => {
    try {
      setIsSaving(true);
      setError(null);
      const { data, error } = await client.POST(
        '/dags/{fileName}/webhook/profile-tokens',
        {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
          body: { name: name.trim(), profile },
        }
      );
      if (error || !data) {
        throw new Error(error?.message || 'Failed to create profile token');
      }
      setName('');
      setProfile('');
      onWebhookChange(data.webhook);
      onTokenCreated(data.token);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Failed to create profile token'
      );
    } finally {
      setIsSaving(false);
    }
  };

  const handleRevoke = async () => {
    if (!pendingRevoke) return;
    const tokenId = pendingRevoke.id;
    setPendingRevoke(null);
    try {
      setIsSaving(true);
      setError(null);
      const { data, error } = await client.DELETE(
        '/dags/{fileName}/webhook/profile-tokens/{tokenId}',
        {
          params: {
            path: { fileName, tokenId },
            query: { remoteNode },
          },
        }
      );
      if (error || !data) {
        throw new Error(error?.message || 'Failed to revoke profile token');
      }
      onWebhookChange(data);
    } catch (error) {
      setError(
        error instanceof Error
          ? error.message
          : 'Failed to revoke profile token'
      );
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <Card className="gap-0 py-0">
      <CardHeader className="pb-3 px-4 pt-3">
        <CardTitle className="text-sm">
          <I18nText text={'Profile tokens'} />
        </CardTitle>
        <CardDescription className="text-xs">
          <I18nTemplate
            text="Give each caller its own token. Requests with a profile token always run with that token's profile, and an {header} header naming another profile is rejected."
            values={{
              header: (
                <code className="bg-accent px-1 rounded-md border">
                  X-Dagu-Profile
                </code>
              ),
            }}
          />
        </CardDescription>
      </CardHeader>
      <CardContent className="px-4 pb-3 pt-2 space-y-3">
        {error && <div className="text-xs text-destructive">{error}</div>}

        {isHMACOnly && (
          <div className="rounded-md border bg-warning/10 px-3 py-2 text-xs text-muted-foreground">
            <I18nText
              text={
                'Profile tokens are ignored while authentication is HMAC only.'
              }
            />
          </div>
        )}

        {tokens.length === 0 ? (
          <div className="text-xs text-muted-foreground">
            <I18nText text={'No profile tokens.'} />
          </div>
        ) : (
          <ul className="divide-y rounded-md border">
            {tokens.map((token) => (
              <li
                key={token.id}
                className="flex items-center justify-between gap-2 px-3 py-2"
              >
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="font-medium break-all">{token.name}</span>
                    <Badge variant="secondary">{token.profile}</Badge>
                  </div>
                  <div className="text-xs font-mono">
                    <span>{token.tokenPrefix}</span>
                    <span className="text-muted-foreground">
                      {'*'.repeat(8)}
                    </span>
                  </div>
                  <div className="flex flex-wrap gap-x-4 text-xs text-muted-foreground">
                    <span>
                      <I18nText text={'Created:'} />{' '}
                      {dayjs(token.createdAt).format('MMM D, YYYY HH:mm')}
                    </span>
                    {token.lastUsedAt && (
                      <span>
                        <I18nText text={'Last used:'} />{' '}
                        {dayjs(token.lastUsedAt).format('MMM D, YYYY HH:mm')}
                      </span>
                    )}
                  </div>
                </div>
                {isAdmin && (
                  <I18nProps>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label="Revoke"
                      className="text-destructive hover:text-destructive"
                      disabled={isSaving}
                      onClick={() => setPendingRevoke(token)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </I18nProps>
                )}
              </li>
            ))}
          </ul>
        )}

        {isAdmin && !isHMACOnly && (
          <div className="flex flex-wrap items-center gap-2">
            <I18nProps>
              <Input
                aria-label="Token name"
                placeholder="Token name"
                className="h-8 w-44 text-sm"
                maxLength={MAX_TOKEN_NAME_LENGTH}
                value={name}
                disabled={isSaving}
                onChange={(event) => setName(event.target.value)}
              />
            </I18nProps>
            <Select
              value={profile}
              onValueChange={setProfile}
              disabled={isSaving}
            >
              <I18nProps>
                <SelectTrigger
                  aria-label="Runtime profile"
                  className="h-8 w-44 text-sm"
                >
                  <I18nProps>
                    <SelectValue placeholder="Runtime profile" />
                  </I18nProps>
                </SelectTrigger>
              </I18nProps>
              <SelectContent>
                {activeProfileNames.map((profileName) => (
                  <SelectItem key={profileName} value={profileName}>
                    {profileName}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              size="sm"
              disabled={isSaving || !name.trim() || !profile}
              onClick={handleCreate}
            >
              {isSaving ? (
                <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
              ) : (
                <Plus className="h-3.5 w-3.5 mr-1" />
              )}
              <I18nText text={'Create token'} />
            </Button>
          </div>
        )}
      </CardContent>

      <I18nProps>
        <ConfirmModal
          title="Revoke Profile Token"
          buttonText="Revoke"
          visible={pendingRevoke !== null}
          dismissModal={() => setPendingRevoke(null)}
          onSubmit={handleRevoke}
        >
          <p>
            <I18nText
              text={
                'Applications using this token will immediately lose access.'
              }
            />
          </p>
        </ConfirmModal>
      </I18nProps>
    </Card>
  );
}

export default WebhookProfileTokensCard;
