import { Clock } from 'lucide-react';
import React from 'react';
import { cn } from '../../../lib/utils';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

interface ServiceInstance {
  instanceId: string;
  host: string;
  status: 'active' | 'inactive' | 'unknown';
  startedAt: string;
  port?: number;
}

interface MiniServiceCardProps {
  title: string;
  instances: ServiceInstance[];
  icon: React.ReactNode;
  isLoading?: boolean;
  error?: string;
}

function MiniServiceCard({
  title,
  instances,
  icon,
  isLoading,
  error,
}: MiniServiceCardProps) {
  const { ts } = useI18n();
  const activeCount = instances.filter((i) => i.status === 'active').length;
  const hasActive = activeCount > 0;

  const getStatusColor = () => {
    if (error) return 'bg-error';
    if (!hasActive) return 'bg-warning';
    return 'bg-success';
  };

  const getUptime = (startedAt: string): string => {
    const start = new Date(startedAt);
    const now = new Date();
    const diff = now.getTime() - start.getTime();

    const days = Math.floor(diff / (1000 * 60 * 60 * 24));
    const hours = Math.floor((diff % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
    const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));

    if (days > 0) {
      return `${days}d ${hours}h`;
    } else if (hours > 0) {
      return `${hours}h ${minutes}m`;
    } else {
      return `${minutes}m`;
    }
  };

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 p-2 bg-muted/30 rounded animate-pulse">
        <div className="w-4 h-4 bg-muted rounded" />
        <div className="h-4 w-16 bg-muted rounded" />
      </div>
    );
  }

  return (
    <div className="flex items-center gap-2 min-w-0">
      <div className="text-muted-foreground shrink-0">{icon}</div>
      <span className="text-sm font-medium truncate">{title}</span>
      <div className="flex items-center gap-1.5 shrink-0">
        <div className="relative">
          <div
            className={cn(
              'w-2 h-2 rounded-full transition-colors',
              getStatusColor()
            )}
          />
          {hasActive && !error && (
            <div
              className={cn(
                'absolute inset-0 rounded-full animate-ping opacity-75',
                getStatusColor()
              )}
            />
          )}
        </div>
        <span
          className={cn(
            'text-xs font-medium',
            error ? 'text-error' : hasActive ? 'text-success' : 'text-warning'
          )}
        >
          {error ? (
            <I18nText text="Error" />
          ) : activeCount > 0 ? (
            ts('{count} active', { count: activeCount })
          ) : (
            <I18nText text="Inactive" />
          )}
        </span>
      </div>
      {instances.length > 0 &&
        instances[0] &&
        instances[0].status === 'active' &&
        !error && (
          <div className="flex items-center gap-1 text-xs text-muted-foreground shrink-0">
            <Clock className="h-3 w-3" />
            <span>{getUptime(instances[0].startedAt)}</span>
          </div>
        )}
    </div>
  );
}

export default MiniServiceCard;
