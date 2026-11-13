import React from 'react';
import { RefreshButton } from '@/components/ui/refresh-button';
import Title from '@/components/ui/title';
import { useI18n } from '@/i18n/I18nProvider';
import { CreateDAGButton } from '../common';
import { I18nText } from '@/i18n/I18nText';
import { WorkflowsModeToggle } from './WorkflowsModeToggle';

interface DAGListHeaderProps {
  onRefresh?: () => void | Promise<void>;
  /** The workflows (or runs) that match the filters. */
  resultCount?: number;
  /** The count has more behind it: the list loads more as it scrolls. */
  hasMore?: boolean;
  /** The mode of the page: its list of workflows, or its list of runs. */
  mode?: 'workflows' | 'runs';
}

const DAGListHeader: React.FC<DAGListHeaderProps> = ({
  onRefresh,
  resultCount,
  hasMore = false,
  mode = 'workflows',
}) => {
  const { ts } = useI18n();
  return (
    <div className="flex flex-row items-center justify-between mb-2">
      <div className="flex min-w-0 items-baseline gap-2">
        <Title>
          <I18nText text={'Workflows'} />
        </Title>
        <WorkflowsModeToggle mode={mode} />
        {resultCount !== undefined && (
          <span
            data-testid="workflow-count"
            className="text-xs text-muted-foreground"
          >
            {ts(
              mode === 'runs'
                ? resultCount === 1 && !hasMore
                  ? '{count} run'
                  : '{count} runs'
                : resultCount === 1
                  ? '{count} workflow'
                  : '{count} workflows',
              {
                count: `${resultCount.toLocaleString()}${hasMore ? '+' : ''}`,
              }
            )}
          </span>
        )}
      </div>
      <div className="flex gap-2">
        <CreateDAGButton />
        {onRefresh && <RefreshButton onRefresh={onRefresh} />}
      </div>
    </div>
  );
};

export default DAGListHeader;
