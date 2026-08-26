import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

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
  const [loadError, setLoadError] = useState<string | null>(null);
  const [testResults, setTestResults] = useState<TestResult[]>([]);
  const requestSequence = useRef(0);

  const fetchData = useCallback(async () => {
    const requestID = ++requestSequence.current;
    setIsLoading(true);
    setError(null);
    setLoadError(null);
    try {
      const settingsRequest = fetch(
        `${apiURL}/dags/${encodeURIComponent(fileName)}/notifications${query}`,
        { headers: authHeaders() }
      );
      const channelRequest = fetch(`${apiURL}/notification-channels${query}`, {
        headers: authHeaders(),
      });
      const globalRoutesRequest = fetch(
        `${apiURL}/notification-routes/global${query}`,
        {
          headers: authHeaders(),
        }
      );
      const workspaceRoutesRequest = workspaceName
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

      if (requestID !== requestSequence.current) {
        return;
      }

      if (settingsResponse.status === 404) {
        setDraft(defaultDraft());
        setHasDAGSettings(false);
      } else if (!settingsResponse.ok) {
        throw new Error(
          await readError(settingsResponse, 'Failed to load notifications')
        );
      } else {
        const data = (await settingsResponse.json()) as NotificationSettings;
        if (requestID !== requestSequence.current) {
          return;
        }
        setDraft(draftFromAPI(data));
        setHasDAGSettings(true);
      }

      if (!channelsResponse.ok) {
        throw new Error(
          await readError(channelsResponse, 'Failed to load channels')
        );
      }
      const channelData =
        (await channelsResponse.json()) as components['schemas']['NotificationChannelListResponse'];
      if (requestID !== requestSequence.current) {
        return;
      }
      setChannels(channelData.channels.map(draftChannelFromAPI));

      if (!globalRoutesResponse.ok) {
        throw new Error(
          await readError(
            globalRoutesResponse,
            'Failed to load inherited notification rules'
          )
        );
      }
      const nextGlobalRoutes =
        (await globalRoutesResponse.json()) as NotificationRouteSet;
      if (requestID !== requestSequence.current) {
        return;
      }
      setGlobalRoutes(nextGlobalRoutes);

      if (workspaceRoutesResponse) {
        if (!workspaceRoutesResponse.ok) {
          throw new Error(
            await readError(
              workspaceRoutesResponse,
              'Failed to load workspace notification rules'
            )
          );
        }
        const nextWorkspaceRoutes =
          (await workspaceRoutesResponse.json()) as NotificationRouteSet;
        if (requestID !== requestSequence.current) {
          return;
        }
        setWorkspaceRoutes(nextWorkspaceRoutes);
      } else {
        setWorkspaceRoutes(null);
      }
      setTestResults([]);
    } catch (err) {
      if (requestID !== requestSequence.current) {
        return;
      }
      const message =
        err instanceof Error ? err.message : 'Failed to load notifications';
      setError(message);
      setLoadError(message);
    } finally {
      if (requestID === requestSequence.current) {
        setIsLoading(false);
      }
    }
  }, [apiURL, fileName, query, workspaceName]);

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
    loadError,
    setError,
    testResults,
    setTestResults,
    fetchData,
  };
}
