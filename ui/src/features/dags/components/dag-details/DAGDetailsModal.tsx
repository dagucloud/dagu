import React from 'react';
import DAGDetailsSidePanel from './DAGDetailsSidePanel';
import { I18nText } from '@/i18n/I18nText';

type Props = {
  fileName: string;
  isOpen: boolean;
  onClose: () => void;
};

function DAGDetailsModal({
  fileName,
  isOpen,
  onClose,
}: Props): React.ReactElement | null {
  return (
    <DAGDetailsSidePanel
      fileName={fileName}
      isOpen={isOpen}
      onClose={onClose}
      initialTab="status"
      toolbarHint={
        <>
          <I18nText text={"Use"} />{' '}
          <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">↑</kbd>{' '}
          <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">↓</kbd>{' '}
          <I18nText text={"to navigate DAGs"} />
        </>
      }
    />
  );
}

export default DAGDetailsModal;
