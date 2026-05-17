import { useCallback, useEffect, useState } from 'react';

import { components } from '../../../../../api/v1/schema';
import {
  authHeaders,
  defaultDraft,
  DraftChannel,
  DraftSettings,
  draftChannelFromAPI,
  draftFromAPI,
  NotificationSettings,
  readError,
  TestResult,
} from './notificationDrafts';

type UseNotificationSettingsArgs = {
  apiURL: string;
  fileName: string;
  query: string;
  reusableChannelsLicensed: boolean;
};

export function useNotificationSettings({
  apiURL,
  fileName,
  query,
  reusableChannelsLicensed,
}: UseNotificationSettingsArgs) {
  const [draft, setDraft] = useState<DraftSettings>(defaultDraft);
  const [channels, setChannels] = useState<DraftChannel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<TestResult[]>([]);

  const fetchData = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const settingsRequest = fetch(
        `${apiURL}/dags/${encodeURIComponent(fileName)}/notifications${query}`,
        { headers: authHeaders() }
      );
      const channelRequest = reusableChannelsLicensed
        ? fetch(`${apiURL}/notification-channels${query}`, {
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
  }, [apiURL, fileName, query, reusableChannelsLicensed]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  return {
    draft,
    setDraft,
    channels,
    setChannels,
    isLoading,
    error,
    setError,
    testResults,
    setTestResults,
    fetchData,
  };
}
