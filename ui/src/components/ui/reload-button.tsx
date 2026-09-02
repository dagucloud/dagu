import { RefreshCw } from 'lucide-react';
import React, { useState } from 'react';
import { Button } from './button';
import { cn } from '@/lib/utils';
import { useI18n } from '@/i18n/I18nProvider';

interface ReloadButtonProps {
  onReload: () => void | Promise<void>;
  isLoading?: boolean;
  disabled?: boolean;
  className?: string;
  title?: string;
}

export const ReloadButton: React.FC<ReloadButtonProps> = ({
  onReload,
  isLoading = false,
  disabled = false,
  className = '',
  title,
}) => {
  const { t } = useI18n();
  const [isReloading, setIsReloading] = useState(false);

  const handleClick = async () => {
    setIsReloading(true);
    try {
      await onReload();
      setTimeout(() => setIsReloading(false), 500);
    } catch {
      setIsReloading(false);
    }
  };

  const isDisabled = disabled || isLoading || isReloading;

  return (
    <Button
      size="icon"
      onClick={handleClick}
      disabled={isDisabled}
      className={className}
      title={title ?? t('common.reload')}
    >
      <RefreshCw
        className={cn('h-4 w-4', (isReloading || isLoading) && 'animate-spin')}
      />
    </Button>
  );
};
