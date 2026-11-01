import React, { useEffect, useRef, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { Button } from './button';
import { Spinner } from './spinner';
import { useI18n } from '@/i18n/I18nProvider';

interface RefreshButtonProps {
  onRefresh: () => void | Promise<void>;
  className?: string;
  disabled?: boolean;
  /** The caller is loading the data: show the spinner and ignore clicks. */
  isLoading?: boolean;
  /** A tooltip that explains what the button reloads; the name stays Refresh. */
  description?: string;
}

export const RefreshButton: React.FC<RefreshButtonProps> = ({
  onRefresh,
  className,
  disabled = false,
  isLoading = false,
  description,
}) => {
  const { t } = useI18n();
  const [isRefreshing, setIsRefreshing] = useState(false);
  const busy = isRefreshing || isLoading;
  // The feedback timer must not outlive the button.
  const feedbackTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined
  );
  useEffect(() => () => clearTimeout(feedbackTimer.current), []);

  const handleRefresh = async () => {
    setIsRefreshing(true);
    try {
      await onRefresh();
    } finally {
      // Brief visual feedback
      feedbackTimer.current = setTimeout(() => setIsRefreshing(false), 500);
    }
  };

  return (
    <Button
      size="icon"
      onClick={handleRefresh}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={className}
      title={description ?? t('common.refresh')}
      aria-label={t('common.refresh')}
    >
      {busy ? (
        <Spinner className="size-4" />
      ) : (
        <RefreshCw className="h-4 w-4" />
      )}
    </Button>
  );
};
