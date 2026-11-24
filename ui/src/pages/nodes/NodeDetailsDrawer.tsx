// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { InventoryNodeKind, type components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import React from 'react';
import { NodeEditor } from './NodeEditor';
import { TestOutcome } from './NodeTestPanel';

type InventoryNode = components['schemas']['InventoryNode'];
type ConnectionTest = components['schemas']['InventoryNodeConnectionTest'];

type NodeDetailsDrawerProps = {
  node: InventoryNode | null;
  /** The node's last test on this page, if any. */
  lastTest?: ConnectionTest;
  /** Whether the user can change the node: edit, disable, or remove it. */
  canEdit?: boolean;
  onChanged?: () => void;
  onRemoved?: () => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

type DetailField = {
  label: string;
  value?: React.ReactNode;
};

/**
 * Shows one node as its runs reach it, with its inventory's defaults applied,
 * and its last test. Credentials appear as key paths and secret refs, never
 * as values.
 */
export function NodeDetailsDrawer({
  node,
  lastTest,
  canEdit = false,
  onChanged = () => {},
  onRemoved = () => {},
  open,
  onOpenChange,
}: NodeDetailsDrawerProps): React.ReactElement | null {
  const { ts } = useI18n();
  if (!node) {
    return null;
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="left-auto right-0 top-0 flex h-[100dvh] max-h-[100dvh] w-full max-w-none translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden p-0 sm:w-[min(40rem,calc(100vw-2rem))] sm:rounded-l-md sm:rounded-r-none">
        <DialogHeader className="shrink-0 border-b border-border px-3 py-2 pr-14">
          <div className="flex flex-wrap items-center gap-2">
            <DialogTitle className="break-all font-mono">
              {node.name}
            </DialogTitle>
            {node.disabled && (
              <Badge variant="outline">
                <I18nText text={'Disabled'} />
              </Badge>
            )}
          </div>
          <DialogDescription>{node.inventory.name}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4">
          <DetailSection
            title="Details"
            fields={[
              { label: 'Inventory', value: node.inventory.name },
              { label: 'Kind', value: node.kind },
              { label: 'Description', value: node.description },
              {
                label: 'Status',
                value: ts(node.disabled ? 'Disabled' : 'Enabled'),
              },
              {
                label: 'Labels',
                value:
                  node.labels.length > 0 ? (
                    <span className="flex flex-wrap gap-1">
                      {node.labels.map((label) => (
                        <Badge key={label} variant="outline">
                          {label}
                        </Badge>
                      ))}
                    </span>
                  ) : undefined,
              },
            ]}
          />
          <DetailSection
            title="Connection"
            fields={[
              { label: 'Host', value: node.host },
              { label: 'Port', value: String(node.port) },
              { label: 'User', value: node.user },
              { label: 'Bastion', value: node.bastion },
              { label: 'Shell', value: node.shell },
              { label: 'Connect timeout', value: node.timeout },
            ]}
          />
          <DetailSection
            title="Credentials"
            fields={[
              { label: 'Key file', value: node.auth.keyPath },
              { label: 'Key secret', value: node.auth.keySecret },
              { label: 'Password secret', value: node.auth.passwordSecret },
              { label: 'Passphrase secret', value: node.auth.passphraseSecret },
            ]}
          />
          <DetailSection
            title="Host key"
            fields={[
              {
                label: 'Strict checking',
                value: node.strictHostKey ? (
                  ts('On')
                ) : (
                  <Badge variant="warning">
                    <I18nText text={'Off: any key is accepted'} />
                  </Badge>
                ),
              },
              { label: 'Pinned key', value: node.hostKey },
            ]}
          />
          {lastTest && (
            <DetailSection
              title="Last test"
              fields={[
                { label: 'Result', value: <TestOutcome result={lastTest} /> },
                { label: 'Bastion key', value: lastTest.bastionFingerprint },
              ]}
            />
          )}
          {canEdit && node.kind === InventoryNodeKind.ssh && (
            <NodeEditor
              key={`${node.inventory.id}/${node.name}`}
              node={node}
              onChanged={onChanged}
              onRemoved={onRemoved}
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function DetailSection({
  title,
  fields,
}: {
  title: string;
  fields: DetailField[];
}): React.ReactElement {
  return (
    <section>
      <h3 className="border-b border-border pb-2 text-xs font-semibold text-muted-foreground">
        <I18nText text={title} />
      </h3>
      <dl className="mt-2 divide-y divide-border/60">
        {fields.map((field) => (
          <div
            key={field.label}
            className="grid gap-1 py-2 sm:grid-cols-[9rem_minmax(0,1fr)] sm:gap-4"
          >
            <dt className="text-xs text-muted-foreground">
              <I18nText text={field.label} />
            </dt>
            <dd className="break-all font-mono text-xs text-foreground">
              {field.value || '—'}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
