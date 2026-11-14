import React from 'react';
import { Server, Activity, Zap, Gauge } from 'lucide-react';
import { cn } from '../../../lib/utils';

interface WorkerMetricsProps {
  metrics: {
    totalWorkers: number;
    healthyWorkers: number;
    totalPollers: number;
    busyPollers: number;
    totalTasks: number;
    utilization: number;
  };
  isLoading: boolean;
}

function WorkerMetrics({ metrics, isLoading }: WorkerMetricsProps) {
  const metricCards = [
    {
      title: 'Workers',
      value: metrics.totalWorkers,
      subValue: `${metrics.healthyWorkers} healthy`,
      icon: <Server className="h-3 w-3 text-muted-foreground" />,
      valueClass: metrics.totalWorkers > 0 ? 'text-foreground' : 'text-muted-foreground',
    },
    {
      title: 'Pollers',
      value: `${metrics.busyPollers}/${metrics.totalPollers}`,
      subValue: 'busy/total',
      icon: <Activity className="h-3 w-3 text-primary" />,
      valueClass: metrics.busyPollers > 0 ? 'text-primary' : 'text-foreground',
    },
    {
      title: 'Active Tasks',
      value: metrics.totalTasks,
      subValue: 'running',
      icon: <Zap className="h-3 w-3 text-success" />,
      valueClass: metrics.totalTasks > 0 ? 'text-success' : 'text-foreground',
    },
    {
      title: 'Utilization',
      value: `${metrics.utilization}%`,
      subValue: 'capacity',
      icon: <Gauge className="h-3 w-3 text-warning" />,
      valueClass: getUtilizationColor(metrics.utilization),
    },
  ];

  return (
    <div className="border rounded bg-card flex-shrink-0">
      <div className="grid grid-cols-2 lg:grid-cols-4 divide-x divide-y lg:divide-y-0">
        {metricCards.map((card) => (
          <div
            key={card.title}
            className="p-2 sm:p-3 flex items-center justify-between"
          >
            <div className="flex items-center gap-2">
              {card.icon}
              <div>
                <div className="text-xs font-medium text-muted-foreground">
                  {card.title}
                </div>
                <div className="text-xs text-muted-foreground">
                  {card.subValue}
                </div>
              </div>
            </div>
            <div className={cn(
              "text-lg font-bold transition-opacity duration-300",
              isLoading && "opacity-50",
              card.valueClass
            )}>
              {card.value}
            </div>
          </div>
        ))}
      </div>
      
      {/* Utilization bar */}
      <div className="px-3 pb-2">
        <div className="h-1 bg-muted rounded-full overflow-hidden">
          <div
            className={cn(
              "h-full transition-[width,background-color] duration-500 ease-out",
              getUtilizationBarColor(metrics.utilization)
            )}
            style={{ width: `${metrics.utilization}%` }}
          />
        </div>
      </div>
    </div>
  );
}

function getUtilizationColor(utilization: number): string {
  if (utilization >= 90) return 'text-error';
  if (utilization >= 70) return 'text-warning';
  if (utilization >= 50) return 'text-warning';
  if (utilization > 0) return 'text-success';
  return 'text-muted-foreground';
}

function getUtilizationBarColor(utilization: number): string {
  if (utilization >= 90) return 'bg-error';
  if (utilization >= 70) return 'bg-warning';
  if (utilization >= 50) return 'bg-warning';
  if (utilization > 0) return 'bg-success';
  return 'bg-muted';
}

export default WorkerMetrics;