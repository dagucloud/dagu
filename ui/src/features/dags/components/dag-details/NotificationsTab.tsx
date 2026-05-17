import { Loader2 } from 'lucide-react';
import { useContext, useMemo, useState } from 'react';

import { Card, CardContent } from '@/components/ui/card';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import {
  components,
  NotificationEventType,
  NotificationProviderType,
} from '../../../../api/v1/schema';
import { AppBarContext } from '../../../../contexts/AppBarContext';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useLicense } from '../../../../hooks/useLicense';
import {
  DAGLocalTargetsSection,
  DAGSubscriptionsSection,
  InheritedNotificationRoutesCard,
  NotificationChannelsUnavailableCard,
  NotificationOverviewCard,
} from './notifications/NotificationSections';
import {
  authHeaders,
  blankTarget,
  DEFAULT_NOTIFICATION_EVENTS,
  defaultDraft,
  deliveryLabel,
  DraftSubscription,
  DraftTarget,
  draftFromAPI,
  NotificationSettings,
  readError,
  subscriptionInput,
  targetInput,
  testEventForTarget,
} from './notifications/notificationDrafts';
import { useNotificationSettings } from './notifications/useNotificationSettings';

type NotificationsTabProps = {
  fileName: string;
  workspaceName?: string;
};

function NotificationsTab({ fileName, workspaceName }: NotificationsTabProps) {
  const config = useConfig();
  const license = useLicense();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const reusableChannelsLicensed =
    !license.community && (license.valid || license.gracePeriod);
  const query = useMemo(
    () => `?remoteNode=${encodeURIComponent(remoteNode)}`,
    [remoteNode]
  );
  const {
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
  } = useNotificationSettings({
    apiURL: config.apiURL,
    fileName,
    query,
    workspaceName,
    reusableChannelsLicensed,
  });
  const [isSaving, setIsSaving] = useState(false);
  const [isResetting, setIsResetting] = useState(false);
  const [hasUnsavedChanges, setHasUnsavedChanges] = useState(false);
  const [testingTargetId, setTestingTargetId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [resetVisible, setResetVisible] = useState(false);
  const [deleteTargetIndex, setDeleteTargetIndex] = useState<number | null>(
    null
  );
  const [deleteSubscriptionIndex, setDeleteSubscriptionIndex] = useState<
    number | null
  >(null);
  const inheritedDestinationCount = effectiveRoutes.filter(
    (route) => route.enabled && route.channelEnabled
  ).length;
  const testableDestinationCount = hasDAGSettings
    ? draft.targets.length +
      (reusableChannelsLicensed ? draft.subscriptions.length : 0)
    : inheritedDestinationCount;
  const hasDAGDestinations =
    draft.targets.length > 0 ||
    (reusableChannelsLicensed && draft.subscriptions.length > 0);

  const refreshSettings = async () => {
    await fetchData();
    setHasUnsavedChanges(false);
  };

  const updateTarget = (
    index: number,
    updater: (target: DraftTarget) => DraftTarget
  ) => {
    setHasUnsavedChanges(true);
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
    setHasUnsavedChanges(true);
    setDraft((current) => ({
      ...current,
      subscriptions: current.subscriptions.map((subscription, subIndex) =>
        subIndex === index ? updater(subscription) : subscription
      ),
    }));
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
    setHasUnsavedChanges(true);
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
    setHasUnsavedChanges(true);
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
      setHasDAGSettings(true);
      setHasUnsavedChanges(false);
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
            eventType:
              targetId || hasDAGSettings
                ? testEventForTarget(draft, events)
                : effectiveRoutes.find(
                    (route) => route.enabled && route.channelEnabled
                  )?.events[0] || DEFAULT_NOTIFICATION_EVENTS[0],
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
      const results = data.results || [];
      setTestResults(results);
      const failedResults = results.filter((result) => !result.delivered);
      if (failedResults.length > 0) {
        const failedLabels = failedResults
          .map(
            (result) =>
              `${result.targetName || result.provider}: ${result.error || 'Delivery failed'}`
          )
          .join('; ');
        setError(`Test failed: ${failedLabels}`);
        return;
      }
      setNotice(
        results.length > 0 ? 'Test delivered' : 'No destinations to test'
      );
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to send test notification'
      );
    } finally {
      setTestingTargetId(null);
    }
  };

  const configureDAGOverride = () => {
    setHasDAGSettings(true);
    setHasUnsavedChanges(true);
    setDraft((current) => ({
      ...defaultDraft(),
      targets: current.targets,
      subscriptions: current.subscriptions,
    }));
    setTestResults([]);
    setNotice(null);
  };

  const resetDAGSettings = async () => {
    setResetVisible(false);
    setIsResetting(true);
    setError(null);
    setNotice(null);
    try {
      const response = await fetch(
        `${config.apiURL}/dags/${encodeURIComponent(fileName)}/notifications${query}`,
        {
          method: 'DELETE',
          headers: authHeaders(),
        }
      );
      if (!response.ok && response.status !== 404) {
        throw new Error(
          await readError(response, 'Failed to reset notifications')
        );
      }
      setDraft(defaultDraft());
      setHasDAGSettings(false);
      setTestResults([]);
      setHasUnsavedChanges(false);
      setNotice('DAG now inherits notification rules');
      await fetchData();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to reset notifications'
      );
    } finally {
      setIsResetting(false);
    }
  };

  const removeTarget = () => {
    if (deleteTargetIndex === null) return;
    setHasUnsavedChanges(true);
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
    setHasUnsavedChanges(true);
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
      <NotificationOverviewCard
        draft={draft}
        isDAGConfigured={hasDAGSettings}
        hasDAGDestinations={hasDAGDestinations}
        hasUnsavedChanges={hasUnsavedChanges}
        inheritedSourceLabel={effectiveRouteSourceLabel}
        error={error}
        notice={notice}
        testResults={testResults}
        isSaving={isSaving}
        isResetting={isResetting}
        testingTargetId={testingTargetId}
        testableDestinationCount={testableDestinationCount}
        onEnabledChange={(enabled) => {
          setHasUnsavedChanges(true);
          setDraft((current) => ({ ...current, enabled }));
        }}
        onEventsChange={(events) => {
          setHasUnsavedChanges(true);
          setDraft((current) => ({ ...current, events }));
        }}
        onRefresh={refreshSettings}
        onTestAll={() => testNotifications()}
        onConfigureDAG={configureDAGOverride}
        onResetDAG={() => setResetVisible(true)}
        onSave={saveSettings}
      />

      {reusableChannelsLicensed && hasDAGSettings ? (
        <DAGSubscriptionsSection
          draft={draft}
          channels={channels}
          isSaving={isSaving}
          hasUnsavedChanges={hasUnsavedChanges}
          testingTargetId={testingTargetId}
          manageChannelsHref="/notification-channels"
          onAdd={addSubscription}
          onSave={saveSettings}
          onUpdate={updateSubscription}
          onDelete={setDeleteSubscriptionIndex}
          onTest={testNotifications}
        />
      ) : reusableChannelsLicensed ? (
        <InheritedNotificationRoutesCard
          sourceLabel={effectiveRouteSourceLabel}
          routes={effectiveRoutes}
          manageRulesHref="/notification-rules"
        />
      ) : (
        <NotificationChannelsUnavailableCard />
      )}

      {hasDAGSettings && (
        <DAGLocalTargetsSection
          draft={draft}
          isSaving={isSaving}
          hasUnsavedChanges={hasUnsavedChanges}
          testingTargetId={testingTargetId}
          onAdd={addLocalTarget}
          onSave={saveSettings}
          onUpdate={updateTarget}
          onDelete={setDeleteTargetIndex}
          onTest={testNotifications}
        />
      )}

      <ConfirmDialog
        title="Reset DAG Override"
        buttonText="Reset"
        visible={resetVisible}
        dismissModal={() => setResetVisible(false)}
        onSubmit={resetDAGSettings}
      >
        Remove this DAG override and inherit workspace or Global notification
        rules?
      </ConfirmDialog>

      <ConfirmDialog
        title="Delete Destination"
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

export default NotificationsTab;
