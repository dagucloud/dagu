import { useCallback, useEffect, useMemo, useState } from 'react';

import {
  components,
  NotificationEventType,
  NotificationProviderType,
} from '../../../../../api/v1/schema';
import {
  authHeaders,
  DEFAULT_NOTIFICATION_EVENTS,
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
  workspaceName?: string;
  reusableChannelsLicensed: boolean;
};

type NotificationRouteSet = components['schemas']['NotificationRouteSet'];

export type EffectiveNotificationRoute = {
  id: string;
  channelId: string;
  channelName: string;
  provider?: NotificationProviderType;
  enabled: boolean;
  channelEnabled: boolean;
  events: NotificationEventType[];
};

export function useNotificationSettings({
  apiURL,
  fileName,
  query,
  workspaceName,
  reusableChannelsLicensed,
}: UseNotificationSettingsArgs) {
  const [draft, setDraft] = useState<DraftSettings>(defaultDraft);
  const [hasDAGSettings, setHasDAGSettings] = useState(false);
  const [channels, setChannels] = useState<DraftChannel[]>([]);
  const [globalRoutes, setGlobalRoutes] = useState<NotificationRouteSet | null>(
    null
  );
  const [workspaceRoutes, setWorkspaceRoutes] =
    useState<NotificationRouteSet | null>(null);
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
      const globalRoutesRequest = reusableChannelsLicensed
        ? fetch(`${apiURL}/notification-routes/global${query}`, {
            headers: authHeaders(),
          })
        : Promise.resolve<Response | null>(null);
      const workspaceRoutesRequest =
        reusableChannelsLicensed && workspaceName
          ? fetch(
              `${apiURL}/notification-routes/workspaces/${encodeURIComponent(workspaceName)}${query}`,
              { headers: authHeaders() }
            )
          : Promise.resolve<Response | null>(null);
      const [
        settingsResponse,
        channelsResponse,
        globalRoutesResponse,
        workspaceRoutesResponse,
      ] = await Promise.all([
        settingsRequest,
        channelRequest,
        globalRoutesRequest,
        workspaceRoutesRequest,
      ]);

      if (settingsResponse.status === 404) {
        setDraft(defaultDraft());
        setHasDAGSettings(false);
      } else if (!settingsResponse.ok) {
        throw new Error(
          await readError(settingsResponse, 'Failed to load notifications')
        );
      } else {
        const data = (await settingsResponse.json()) as NotificationSettings;
        setDraft(draftFromAPI(data));
        setHasDAGSettings(true);
      }

      if (!channelsResponse) {
        setChannels([]);
        setGlobalRoutes(null);
        setWorkspaceRoutes(null);
      } else {
        if (!channelsResponse.ok) {
          throw new Error(
            await readError(channelsResponse, 'Failed to load channels')
          );
        }
        const channelData =
          (await channelsResponse.json()) as components['schemas']['NotificationChannelListResponse'];
        setChannels(channelData.channels.map(draftChannelFromAPI));

        if (!globalRoutesResponse?.ok) {
          throw new Error(
            await readError(
              globalRoutesResponse as Response,
              'Failed to load inherited notification rules'
            )
          );
        }
        setGlobalRoutes(
          (await globalRoutesResponse.json()) as NotificationRouteSet
        );

        if (workspaceRoutesResponse) {
          if (!workspaceRoutesResponse.ok) {
            throw new Error(
              await readError(
                workspaceRoutesResponse,
                'Failed to load workspace notification rules'
              )
            );
          }
          setWorkspaceRoutes(
            (await workspaceRoutesResponse.json()) as NotificationRouteSet
          );
        } else {
          setWorkspaceRoutes(null);
        }
      }
      setTestResults([]);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to load notifications'
      );
    } finally {
      setIsLoading(false);
    }
  }, [apiURL, fileName, query, reusableChannelsLicensed, workspaceName]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const effectiveRouteSet =
    workspaceRoutes && !workspaceRoutes.inheritGlobal
      ? workspaceRoutes
      : globalRoutes;
  const effectiveRouteSourceLabel =
    workspaceRoutes && !workspaceRoutes.inheritGlobal
      ? `${workspaceName} workspace rules`
      : 'Global rules';
  const effectiveRoutes = useMemo<EffectiveNotificationRoute[]>(() => {
    if (!effectiveRouteSet?.routes) {
      return [];
    }
    const channelsById = new Map(
      channels
        .filter((channel) => channel.id)
        .map((channel) => [channel.id as string, channel])
    );
    return effectiveRouteSet.routes.map((route) => {
      const channel = channelsById.get(route.channelId);
      return {
        id: route.id,
        channelId: route.channelId,
        channelName: channel?.name || route.channelId,
        provider: channel?.type,
        enabled: route.enabled,
        channelEnabled: !!channel?.enabled,
        events:
          route.events && route.events.length > 0
            ? route.events
            : DEFAULT_NOTIFICATION_EVENTS,
      };
    });
  }, [channels, effectiveRouteSet]);

  return {
    draft,
    setDraft,
    hasDAGSettings,
    setHasDAGSettings,
    channels,
    effectiveRoutes,
    effectiveRouteSourceLabel,
    isLoading,
    error,
    setError,
    testResults,
    setTestResults,
    fetchData,
  };
}
