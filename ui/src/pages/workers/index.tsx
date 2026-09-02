import { Activity, Cpu, Search, Server } from 'lucide-react';
import React from 'react';
import type { components } from '../../api/v1/schema';
import { Input } from '@/components/ui/input';
import { RefreshButton } from '@/components/ui/refresh-button';
import { AppBarContext } from '../../contexts/AppBarContext';
import { DAGRunDetailsModal } from '../../features/dag-runs/components/dag-run-details';
import WorkerList from '../../features/workers/components/WorkerList';
import WorkerMetrics from '../../features/workers/components/WorkerMetrics';
import { useQuery } from '../../hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

function Workers() {
  const appBarContext = React.useContext(AppBarContext);
  const [searchText, setSearchText] = React.useState('');

  // State for DAG run modal
  const [modalDAGRun, setModalDAGRun] = React.useState<{
    name: string;
    dagRunId: string;
  } | null>(null);

  React.useEffect(() => {
    appBarContext.setTitle('Workers');
  }, [appBarContext]);

  const { data, error, isLoading, mutate } = useQuery(
    '/workers',
    {
      params: {
        query: {
          remoteNode: appBarContext.selectedRemoteNode || 'local',
        },
      },
    },
    {
      refreshInterval: 1000, // Refresh every second for real-time updates
      revalidateOnFocus: true,
      revalidateOnReconnect: true,
    }
  );

  const handleRefresh = async () => {
    await mutate();
  };

  // Filter workers based on search text
  const filteredWorkers = React.useMemo(() => {
    if (!data?.workers || !searchText) return data?.workers || [];

    const search = searchText.toLowerCase();
    return data.workers.filter((worker) => {
      // Search by ID
      if (worker.id.toLowerCase().includes(search)) return true;

      // Search by labels
      if (worker.labels) {
        return Object.entries(worker.labels).some(([key, value]) =>
          `${key}=${value}`.toLowerCase().includes(search)
        );
      }

      return false;
    });
  }, [data?.workers, searchText]);

  // Calculate metrics
  const metrics = React.useMemo(() => {
    const workers = data?.workers || [];
    const totalWorkers = workers.length;
    const totalPollers = workers.reduce(
      (sum, w) => sum + (w.totalPollers || 0),
      0
    );
    const busyPollers = workers.reduce(
      (sum, w) => sum + (w.busyPollers || 0),
      0
    );
    const totalTasks = workers.reduce(
      (sum, w) => sum + (w.runningTasks?.length || 0),
      0
    );
    const utilization =
      totalPollers > 0 ? Math.round((busyPollers / totalPollers) * 100) : 0;

    // Count healthy workers (heartbeat within last 10 seconds)
    const healthyWorkers = workers.filter((worker) => {
      if (!worker.lastHeartbeatAt) return false;
      const lastHeartbeat = new Date(worker.lastHeartbeatAt).getTime();
      const now = new Date().getTime();
      return now - lastHeartbeat < 10000; // 10 seconds
    }).length;

    return {
      totalWorkers,
      healthyWorkers,
      totalPollers,
      busyPollers,
      totalTasks,
      utilization,
    };
  }, [data?.workers]);

  // Handle task click
  const handleTaskClick = React.useCallback(
    (task: components['schemas']['RunningTask']) => {
      // For nested tasks, we need to set up the URL params for sub DAG view
      if (task.parentDagRunName && task.parentDagRunId) {
        const searchParams = new URLSearchParams();
        searchParams.set('subDAGRunId', task.dagRunId);
        searchParams.set('dagRunId', task.parentDagRunId);
        searchParams.set('dagRunName', task.parentDagRunName);
        window.history.pushState(
          {},
          '',
          `${window.location.pathname}?${searchParams.toString()}`
        );

        // Open modal with parent DAG info
        setModalDAGRun({
          name: task.parentDagRunName,
          dagRunId: task.parentDagRunId,
        });
      } else {
        // For root tasks, open directly
        setModalDAGRun({
          name: task.dagName,
          dagRunId: task.dagRunId,
        });
      }
    },
    []
  );

  if (error) {
    const errorData = error as components['schemas']['Error'];
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center space-y-2">
          <Server className="h-12 w-12 text-muted-foreground mx-auto" />
          <p className="text-sm text-muted-foreground">
            {errorData?.message || <I18nText text={'Failed to load workers'} />}
          </p>
          {data?.errors?.map((err, idx) => (
            <p key={idx} className="text-xs text-error">
              {err}
            </p>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 max-w-7xl h-full overflow-hidden">
      {/* Header with search and refresh */}
      <div className="card-obsidian flex-shrink-0">
        <div className="flex items-center justify-between p-3">
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-2">
              <Activity className="h-4 w-4 text-muted-foreground" />
              <h1 className="text-sm font-semibold">
                <I18nText text={'Distributed Workers'} />
              </h1>
            </div>
            <div className="relative">
              <Search className="absolute left-2 top-1.5 h-3 w-3 text-muted-foreground" />
              <I18nProps>
                <Input
                  type="text"
                  placeholder="Search by ID or labels..."
                  value={searchText}
                  onChange={(e) => setSearchText(e.target.value)}
                  className="h-7 w-[250px] pl-7 text-xs"
                />
              </I18nProps>
            </div>
          </div>
          <RefreshButton onRefresh={handleRefresh} />
        </div>
      </div>

      {/* Metrics */}
      <WorkerMetrics metrics={metrics} isLoading={isLoading} />

      {/* Worker List */}
      <div className="card-obsidian flex-1 flex flex-col min-h-0">
        <div className="p-3 border-b flex-shrink-0">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Cpu className="h-4 w-4 text-muted-foreground" />
              <span className="text-sm font-semibold">
                <I18nText text={'Worker Status'} />
              </span>
              {filteredWorkers.length !== data?.workers?.length && (
                <span className="text-xs text-muted-foreground">
                  <I18nText
                    text="({visible} of {total})"
                    values={{
                      visible: filteredWorkers.length,
                      total: data?.workers?.length ?? 0,
                    }}
                  />
                </span>
              )}
            </div>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <div className="flex items-center gap-1">
                <div className="w-2 h-2 rounded-full bg-success animate-pulse" />
                <span>
                  <I18nText text={'Healthy'} />
                </span>
              </div>
              <div className="flex items-center gap-1">
                <div className="w-2 h-2 rounded-full bg-warning" />
                <span>
                  <I18nText text={'Warning'} />
                </span>
              </div>
              <div className="flex items-center gap-1">
                <div className="w-2 h-2 rounded-full bg-error" />
                <span>
                  <I18nText text={'Offline'} />
                </span>
              </div>
            </div>
          </div>
        </div>
        <div className="flex-1 min-h-0 overflow-auto">
          <WorkerList
            workers={filteredWorkers}
            isLoading={isLoading && !data}
            errors={data?.errors}
            onTaskClick={handleTaskClick}
          />
        </div>
      </div>

      {/* DAG Run Details Modal */}
      {modalDAGRun && (
        <DAGRunDetailsModal
          name={modalDAGRun.name}
          dagRunId={modalDAGRun.dagRunId}
          isOpen={!!modalDAGRun}
          onClose={() => {
            setModalDAGRun(null);
            // Clear URL params when closing modal
            window.history.pushState({}, '', window.location.pathname);
          }}
        />
      )}
    </div>
  );
}

export default Workers;
