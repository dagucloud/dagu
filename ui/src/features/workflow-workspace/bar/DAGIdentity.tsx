// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link2 } from 'lucide-react';
import { useSWRConfig } from 'swr';
import { CopyButton } from '@/components/ui/copy-button';
import DAGPinButton from '@/features/dags/components/dag-details/DAGPinButton';
import { useI18n } from '@/i18n/I18nProvider';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import { cn, FOCUS_RING } from '@/lib/utils';
import { useWorkspaceRefresh } from '../data';
import { DescriptionPopover } from './DescriptionPopover';
import { useWorkflowShare } from './useWorkflowShare';
import { WorkflowRenameButton } from './WorkflowEditButtons';

export interface DAGIdentityProps extends BarSlotProps {
  /** Whether the DAG is pinned; the pin button is hidden when unknown. */
  pinned?: boolean;
}

// The look of a small IconButton, for the description button, which owns its
// own element.
const infoButton = cn(
  'inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground',
  FOCUS_RING
);

/**
 * Pin, name, Rename, copy name, copy link and description of the workflow. The
 * Rename button sits next to the name; the copy
 * and description buttons need a bar of 672px (@2xl): in a narrower bar they
 * hide before the name is cut.
 */
export function DAGIdentity({
  fileName,
  dag,
  workspaceName,
  pinned,
}: DAGIdentityProps) {
  const { ts } = useI18n();
  const refresh = useWorkspaceRefresh();
  const { mutate } = useSWRConfig();
  // The workflow list, which may be below the page in the overlay, shows
  // the pins too.
  const onPinChanged = () => {
    void refresh().catch(() => undefined);
    void mutate((key) => Array.isArray(key) && key[1] === '/dags');
  };
  const dirty = useWorkspaceEditor().state?.dirty ?? false;
  const name = dag.name;
  const description = dag.description?.trim();
  const { getPageLink } = useWorkflowShare(fileName);

  return (
    <div
      data-testid="ww-identity"
      className="flex min-w-0 shrink items-center gap-1"
    >
      {pinned !== undefined && (
        <div className="hidden shrink-0 md:flex">
          <DAGPinButton
            fileName={fileName}
            name={name}
            pinned={pinned}
            workspace={workspaceName ?? ''}
            onChanged={onPinChanged}
          />
        </div>
      )}
      <h1
        className="min-w-0 truncate text-base font-semibold tracking-[-0.01em] text-foreground"
        title={name}
      >
        {name}
      </h1>
      <WorkflowRenameButton fileName={fileName} workspaceName={workspaceName} />
      <div
        data-testid="ww-identity-secondary"
        className="hidden shrink-0 items-center @2xl:flex"
      >
        <CopyButton
          size="sm"
          text={name}
          label={ts('Copy name')}
          copiedLabel={ts('Name copied')}
        />
        <CopyButton
          size="sm"
          icon={Link2}
          text={getPageLink}
          label={ts('Copy link to this workflow')}
          copiedLabel={ts('Link copied')}
        />
        {description && (
          <DescriptionPopover
            description={description}
            className={infoButton}
          />
        )}
      </div>
      {/* No visual marker: Save and Revert show the unsaved state. */}
      {dirty && (
        <span data-testid="ww-unsaved" className="sr-only">
          {ts('This workflow has unsaved changes')}
        </span>
      )}
      {description && <span className="sr-only">{description}</span>}
    </div>
  );
}
