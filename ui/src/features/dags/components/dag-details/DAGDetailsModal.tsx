import React from 'react';
import DAGDetailsSidePanel from './DAGDetailsSidePanel';
import { I18nTemplate } from '@/i18n/I18nTemplate';

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
        <I18nTemplate
          text="Use {up} {down} to navigate DAGs"
          values={{
            up: (
              <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                ↑
              </kbd>
            ),
            down: (
              <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                ↓
              </kbd>
            ),
          }}
        />
      }
    />
  );
}

export default DAGDetailsModal;
