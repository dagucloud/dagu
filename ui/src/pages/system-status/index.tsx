import { Activity, Calendar, RefreshCw, Server } from 'lucide-react';
import React from 'react';
import type { components } from '../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useConfig } from '../../contexts/ConfigContext';
import WorkersSummary from '../../features/dashboard/components/WorkersSummary';
import PathsCard from '../../features/system-status/components/PathsCard';
import ResourceChart from '../../features/system-status/components/ResourceChart';
import ServiceCard from '../../features/system-status/components/ServiceCard';
import TunnelStatusCard from '../../features/system-status/components/TunnelStatusCard';
import { useQuery } from '../../hooks/api';
import { cn } from '../../lib/utils';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

type SchedulerInstance = components['schemas']['SchedulerInstance'];
type CoordinatorInstance = components['schemas']['CoordinatorInstance'];
type TunnelStatusResponse = components['schemas']['TunnelStatusResponse'];

/**
 * Render the System Status view showing service health, resource usage charts, and refresh controls.
 *
 * Displays Scheduler and Coordinator service cards, four resource usage charts (CPU, Memory, Disk, Load Average),
 * and controls for toggling auto-refresh and triggering a manual refresh. Data is fetched for the currently
 * selected remote node and the "last updated" timestamp reflects the most recent automatic or manual refresh.
 *
 * @returns The rendered System Status UI containing service cards, resource charts, and refresh controls.
 */
function SystemStatus() {
  const { ts } = useI18n();
  const appBarContext = React.useContext(AppBarContext);
  const config = useConfig();
  const [isRefreshing, setIsRefreshing] = React.useState(false);
  const [autoRefresh, setAutoRefresh] = React.useState(true);
  const [lastUpdateTime, setLastUpdateTime] = React.useState<Date>(new Date());

  React.useEffect(() => {
    appBarContext.setTitle('System Status');
  }, [appBarContext]);

  // Fetch all data with remoteNode support and auto-refresh
  const {
    data: schedulerData,
    error: schedulerError,
    mutate: mutateScheduler,
  } = useQuery(
    '/services/scheduler',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
    },
    {
      refreshInterval: autoRefresh ? 5000 : 0,
    }
  );

  const {
    data: coordinatorData,
    error: coordinatorError,
    mutate: mutateCoordinator,
  } = useQuery(
    '/services/coordinator',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
    },
    {
      refreshInterval: autoRefresh ? 5000 : 0,
    }
  );

  const {
    data: resourceData,
    error: resourceError,
    mutate: mutateResource,
  } = useQuery(
    '/services/resources/history',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
    },
    {
      refreshInterval: autoRefresh ? 5000 : 0,
    }
  );

  const {
    data: workersData,
    error: workersError,
    mutate: mutateWorkers,
  } = useQuery(
    '/workers',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
    },
    {
      refreshInterval: autoRefresh ? 1000 : 0,
    }
  );

  const {
    data: tunnelData,
    error: tunnelError,
    mutate: mutateTunnel,
  } = useQuery(
    '/services/tunnel',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
    },
    {
      refreshInterval: autoRefresh ? 5000 : 0,
    }
  );

  const handleRefresh = async () => {
    setIsRefreshing(true);
    try {
      await Promise.all([
        mutateResource(),
        mutateScheduler(),
        mutateCoordinator(),
        mutateWorkers(),
        mutateTunnel(),
      ]);
      setLastUpdateTime(new Date());
    } finally {
      setIsRefreshing(false);
    }
  };

  // Update timestamp when data changes from auto-refresh
  React.useEffect(() => {
    if (resourceData) {
      setLastUpdateTime(new Date());
    }
  }, [resourceData]);

  return (
    <div className="flex flex-col gap-4 max-w-7xl">
      {/* Header */}
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold">
            <I18nText text={'System Status'} />
          </h1>
          <p className="text-sm text-muted-foreground">
            <I18nText
              text={'Monitor and manage Dagu services and system health'}
            />
          </p>
        </div>
        <div className="flex items-center gap-2">
          <PathsCard />
          <Button
            onClick={() => setAutoRefresh(!autoRefresh)}
            aria-label={ts('Auto-refresh {state}', {
              state: ts(autoRefresh ? 'enabled' : 'disabled'),
            })}
            title={ts('Toggle auto-refresh (currently {state})', {
              state: ts(autoRefresh ? 'ON' : 'OFF'),
            })}
          >
            <Activity
              className={cn('h-4 w-4', autoRefresh && 'text-success')}
            />
            <I18nText text={'Auto:'} />{' '}
            {autoRefresh ? <I18nText text={'ON'} /> : <I18nText text={'OFF'} />}
          </Button>
          <I18nProps>
            <Button
              size="icon"
              onClick={handleRefresh}
              disabled={isRefreshing}
              aria-label="Refresh system status"
              title="Refresh system status"
            >
              <RefreshCw
                className={cn('h-4 w-4', isRefreshing && 'animate-spin')}
              />
            </Button>
          </I18nProps>
        </div>
      </div>

      {/* Services */}
      <div className="flex flex-col gap-4">
        {/* Scheduler Service */}
        <I18nProps>
          <ServiceCard
            title="Scheduler Service"
            instances={
              schedulerData?.schedulers?.map((s: SchedulerInstance) => ({
                instanceId: s.instanceId,
                host: s.host,
                status: s.status,
                startedAt: s.startedAt,
              })) || []
            }
            icon={<Calendar className="h-4 w-4" />}
            isLoading={!schedulerData && !schedulerError}
            error={schedulerError ? String(schedulerError) : undefined}
          />
        </I18nProps>

        {/* Coordinator Service */}
        <I18nProps>
          <ServiceCard
            title="Coordinator Service"
            instances={
              coordinatorData?.coordinators?.map((c: CoordinatorInstance) => ({
                instanceId: c.instanceId,
                host: c.host,
                port: c.port,
                status: c.status,
                startedAt: c.startedAt,
              })) || []
            }
            icon={<Server className="h-4 w-4" />}
            isLoading={!coordinatorData && !coordinatorError}
            error={coordinatorError ? String(coordinatorError) : undefined}
          />
        </I18nProps>

        {/* Tunnel Service */}
        <TunnelStatusCard
          data={tunnelData as TunnelStatusResponse}
          isLoading={!tunnelData && !tunnelError}
          error={tunnelError ? String(tunnelError) : undefined}
        />
      </div>

      {/* Workers Status */}
      <h2 className="text-xl font-semibold mt-8 mb-4">
        <I18nText text={'Workers'} />
      </h2>
      <div className="card-obsidian" style={{ minHeight: '200px' }}>
        <WorkersSummary
          workers={workersData?.workers || []}
          isLoading={!workersData && !workersError}
          errors={workersData?.errors}
        />
      </div>

      {/* Resource Usage */}
      <h2 className="text-xl font-semibold mt-8 mb-4">
        <I18nText text={'Resource Usage'} />
      </h2>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <I18nProps>
          <ResourceChart
            title="CPU Usage"
            data={resourceData?.cpu}
            color="#73BF69"
            isLoading={!resourceData && !resourceError}
            error={resourceError ? String(resourceError) : undefined}
          />
        </I18nProps>
        <I18nProps>
          <ResourceChart
            title="Memory Usage"
            data={resourceData?.memory}
            color="#73BF69"
            isLoading={!resourceData && !resourceError}
            error={resourceError ? String(resourceError) : undefined}
            totalBytes={resourceData?.memoryTotalBytes}
            usedBytes={resourceData?.memoryUsedBytes}
          />
        </I18nProps>
        <I18nProps>
          <ResourceChart
            title="Disk Usage"
            data={resourceData?.disk}
            color="#73BF69"
            isLoading={!resourceData && !resourceError}
            error={resourceError ? String(resourceError) : undefined}
            totalBytes={resourceData?.diskTotalBytes}
            usedBytes={resourceData?.diskUsedBytes}
          />
        </I18nProps>
        <I18nProps>
          <ResourceChart
            title="Load Average"
            data={resourceData?.load}
            color="#73BF69"
            unit=""
            isLoading={!resourceData && !resourceError}
            error={resourceError ? String(resourceError) : undefined}
          />
        </I18nProps>
      </div>

      {/* Footer */}
      <div className="text-xs text-muted-foreground text-center space-y-1 mb-4">
        <div>
          <I18nText text={'Last updated:'} />{' '}
          {lastUpdateTime.toLocaleTimeString()}
          {autoRefresh && <I18nText text={' • Refreshing every 5 seconds'} />}
        </div>
        <div>
          <I18nText text={'Dagu v'} />
          {config.version}
        </div>
      </div>
    </div>
  );
}

export default SystemStatus;
