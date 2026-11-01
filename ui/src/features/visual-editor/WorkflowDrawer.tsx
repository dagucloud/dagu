// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement, ReactNode } from 'react';
import {
  PANEL_BODY_CLASS,
  PanelHeader,
} from '@/features/dags/components/step-details/PanelShell';
import { cn } from '@/lib/utils';
import { StepSideDrawer } from '@/features/dags/components/step-details/StepSideDrawer';
import { useI18n } from '@/i18n/I18nProvider';

/** The side drawer of the workflow panel: what the workflow is, apart from one step. */
export function WorkflowDrawer({
  open,
  onClose,
  bare = false,
  children,
}: {
  open: boolean;
  onClose(): void;
  /** The content brings its own body (tabs): no wrapper. */
  bare?: boolean;
  children: ReactNode;
}): ReactElement {
  const { ts } = useI18n();
  return (
    <StepSideDrawer
      isOpen={open}
      onClose={onClose}
      hasContent={open}
      modal={false}
      label={ts('Workflow')}
      panelId="workflow"
      closeOnPageEscape
    >
      <div data-testid="ve-workflow" className="flex min-h-0 flex-1 flex-col">
        <PanelHeader
          title={ts('Workflow')}
          closeLabel={ts('Close')}
          onClose={onClose}
        />
        {bare ? (
          children
        ) : (
          <div className={cn(PANEL_BODY_CLASS, 'space-y-6')}>{children}</div>
        )}
      </div>
    </StepSideDrawer>
  );
}
