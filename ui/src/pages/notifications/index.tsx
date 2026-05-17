import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  Loader2,
  Mail,
  Save,
} from 'lucide-react';
import { useCallback, useContext, useEffect, useMemo, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { Input } from '@/components/ui/input';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useLicense } from '@/hooks/useLicense';
import {
  ReusableChannelsUnavailableCard,
  WorkspaceChannelsSection,
} from '@/features/dags/components/dag-details/notifications/NotificationSections';
import {
  authHeaders,
  blankChannel,
  channelInput,
  deliveryLabel,
  DraftChannel,
  draftChannelFromAPI,
  NotificationChannel,
  readError,
} from '@/features/dags/components/dag-details/notifications/notificationDrafts';
import { components, NotificationProviderType } from '@/api/v1/schema';

type NotificationWorkspaceSettings =
  components['schemas']['NotificationWorkspaceSettings'];

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

export default function NotificationsPage() {
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
        return;
      }
      const response = await fetch(
        `${config.apiURL}/notification-channels${query}`,
        { headers: authHeaders() }
      );
      if (!response.ok) {
        throw new Error(await readError(response, 'Failed to load channels'));
      }
      const data = (await response.json()) as {
        channels: NotificationChannel[];
      };
      setChannels((data.channels || []).map(draftChannelFromAPI));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load settings');
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
            <Badge variant="default">Workspace</Badge>
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
        <WorkspaceChannelsSection
          channels={channels}
          savingChannelIndex={savingChannelIndex}
          onAdd={addChannel}
          onUpdate={updateChannel}
          onSave={saveChannel}
          onDelete={setDeleteChannelIndex}
        />
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
