import React, { useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { Button } from './button';
import { cn } from '../../lib/utils';
import { useI18n } from '@/i18n/I18nProvider';

interface RefreshButtonProps {
  onRefresh: () => void | Promise<void>;
  className?: string;
  disabled?: boolean;
}

export const RefreshButton: React.FC<RefreshButtonProps> = ({
  onRefresh,
  className,
  disabled = false,
}) => {
  const { t } = useI18n();
  const [isRefreshing, setIsRefreshing] = useState(false);

  const handleRefresh = async () => {
    setIsRefreshing(true);
    try {
      await onRefresh();
    } finally {
      // Brief visual feedback
      setTimeout(() => setIsRefreshing(false), 500);
    }
  };

  return (
    <Button
      size="icon"
      onClick={handleRefresh}
      disabled={disabled || isRefreshing}
      className={className}
      title={t('common.refresh')}
    >
      <RefreshCw className={cn('h-4 w-4', isRefreshing && 'animate-spin')} />
    </Button>
  );
};
