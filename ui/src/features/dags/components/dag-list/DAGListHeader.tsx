import React from 'react';
import { RefreshButton } from '@/components/ui/refresh-button';
import Title from '@/components/ui/title';
import { CreateDAGButton } from '../common';
import { I18nText } from '@/i18n/I18nText';

interface DAGListHeaderProps {
  onRefresh?: () => void | Promise<void>;
}

const DAGListHeader: React.FC<DAGListHeaderProps> = ({ onRefresh }) => (
  <div className="flex flex-row items-center justify-between mb-2">
    <Title><I18nText text={"Workflows"} /></Title>
    <div className="flex gap-2">
      <CreateDAGButton />
      {onRefresh && <RefreshButton onRefresh={onRefresh} />}
    </div>
  </div>
);

export default DAGListHeader;
