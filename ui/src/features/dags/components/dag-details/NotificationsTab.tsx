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
  NotificationChannelsUnavailableCard,
  NotificationOverviewCard,
} from './notifications/NotificationSections';
import {
  authHeaders,
  blankTarget,
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
};

function NotificationsTab({ fileName }: NotificationsTabProps) {
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
    channels,
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
    reusableChannelsLicensed,
  });
  const [isSaving, setIsSaving] = useState(false);
  const [testingTargetId, setTestingTargetId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [deleteTargetIndex, setDeleteTargetIndex] = useState<number | null>(
    null
  );
  const [deleteSubscriptionIndex, setDeleteSubscriptionIndex] = useState<
    number | null
  >(null);
  const testableDestinationCount =
    draft.targets.length +
    (reusableChannelsLicensed ? draft.subscriptions.length : 0);

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

  const updateSubscription = (
    index: number,
    updater: (subscription: DraftSubscription) => DraftSubscription
  ) => {
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
            eventType: testEventForTarget(draft, events),
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

  const removeTarget = () => {
    if (deleteTargetIndex === null) return;
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
        error={error}
        notice={notice}
        testResults={testResults}
        isSaving={isSaving}
        testingTargetId={testingTargetId}
        testableDestinationCount={testableDestinationCount}
        onEnabledChange={(enabled) =>
          setDraft((current) => ({ ...current, enabled }))
        }
        onEventsChange={(events) =>
          setDraft((current) => ({ ...current, events }))
        }
        onRefresh={fetchData}
        onTestAll={() => testNotifications()}
        onSave={saveSettings}
      />

      {reusableChannelsLicensed ? (
        <DAGSubscriptionsSection
          draft={draft}
          channels={channels}
          testingTargetId={testingTargetId}
          manageChannelsHref="/notification-channels"
          onAdd={addSubscription}
          onUpdate={updateSubscription}
          onDelete={setDeleteSubscriptionIndex}
          onTest={testNotifications}
        />
      ) : (
        <NotificationChannelsUnavailableCard />
      )}

      <DAGLocalTargetsSection
        draft={draft}
        testingTargetId={testingTargetId}
        onAdd={addLocalTarget}
        onUpdate={updateTarget}
        onDelete={setDeleteTargetIndex}
        onTest={testNotifications}
      />

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
