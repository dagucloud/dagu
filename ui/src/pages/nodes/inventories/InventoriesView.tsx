// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { useClient } from '@/hooks/api';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import React, { Suspense, lazy, useRef, useState } from 'react';
import { downloadBlob } from '@/lib/download';
import { ImportDialog } from './ImportDialog';
import { InventoryDialog } from './InventoryDialog';
import {
  deleteInventory,
  exportRecords,
  readInventory,
  type ChangeRefusal,
  type Inventory,
} from './inventoryChanges';
import { RefusalNotice } from './RefusalNotice';

type InventorySummary = components['schemas']['InventorySummary'];

// The JSON editor loads Monaco, so it loads when first opened.
const InventoryJSONDialog = lazy(() =>
  import('./InventoryJSONDialog').then((module) => ({
    default: module.InventoryJSONDialog,
  }))
);

/** What the view has open. */
type Editing =
  | { kind: 'create' | 'import' }
  | { kind: 'form' | 'json'; inventory: Inventory }
  | { kind: 'delete'; inventory: InventorySummary };

/**
 * Lists the inventories with their node counts, and exports them as
 * `dagu nodes export` prints them. A user who can edit them creates,
 * renames, and deletes them, edits their defaults, edits one as JSON, and
 * imports inventories.
 */
export function InventoriesView({
  inventories,
  canEdit,
  onChanged,
}: {
  inventories: InventorySummary[];
  canEdit: boolean;
  onChanged: () => void;
}): React.ReactElement {
  const client = useClient();
  const { ts } = useI18n();
  const [editing, setEditing] = useState<Editing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refusal, setRefusal] = useState<ChangeRefusal | null>(null);
  // Counts the actions that open something, so a read that a later action
  // superseded opens nothing.
  const actionRef = useRef(0);

  /** Opens what an action asks for, and supersedes any pending read. */
  function show(next: Editing | null) {
    actionRef.current += 1;
    setEditing(next);
  }

  /** Reads the whole inventory, which a summary lacks, and opens it. */
  async function open(id: string, kind: 'form' | 'json') {
    const action = ++actionRef.current;
    setError(null);
    const read = await readInventory(client, id);
    if (action !== actionRef.current) {
      return;
    }
    if ('failure' in read) {
      setError(read.failure || ts("Couldn't read the inventory."));
      return;
    }
    setEditing({ kind, inventory: read });
  }

  async function remove(inventory: InventorySummary) {
    show(null);
    const { refusal } = await deleteInventory(
      client,
      inventory.id,
      inventory.version
    );
    setRefusal(refusal ?? null);
    onChanged();
  }

  /** Downloads the inventories ids as `dagu nodes export` prints them. */
  async function exportAs(ids: string[], filename: string) {
    setError(null);
    const records = await exportRecords(client, ids);
    if ('failure' in records) {
      setError(records.failure || ts("Couldn't read the inventory."));
      return;
    }
    downloadBlob(
      new Blob([`${JSON.stringify(records, null, 2)}\n`], {
        type: 'application/json',
      }),
      filename
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <div className="flex flex-wrap gap-2">
        {canEdit && (
          <>
            <Button size="sm" onClick={() => show({ kind: 'create' })}>
              <I18nText text={'New inventory'} />
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => show({ kind: 'import' })}
            >
              <I18nText text={'Import'} />
            </Button>
          </>
        )}
        <Button
          size="sm"
          variant="outline"
          disabled={inventories.length === 0}
          onClick={() =>
            void exportAs(
              inventories.map((inventory) => inventory.id),
              'inventories.json'
            )
          }
        >
          <I18nText text={'Export'} />
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-xs text-error">
          {error}
        </p>
      )}
      {refusal && (
        <RefusalNotice
          refusal={{ ...refusal, current: undefined }}
          onLoadCurrent={() => {}}
        />
      )}
      <div className="card-obsidian min-h-0 flex-1 !overflow-auto">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[240px]">
                <I18nText text={'Name'} />
              </TableHead>
              <TableHead>
                <I18nText text={'Description'} />
              </TableHead>
              <TableHead className="w-[80px] text-right">
                <I18nText text={'Nodes'} />
              </TableHead>
              <TableHead className="w-[300px]" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {inventories.map((inventory) => (
              <TableRow key={inventory.id}>
                <TableCell className="font-medium">{inventory.name}</TableCell>
                <TableCell className="text-muted-foreground">
                  {inventory.description}
                </TableCell>
                <TableCell className="text-right font-mono">
                  {inventory.nodeCount}
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1">
                    <RowAction
                      label="Export"
                      name={inventory.name}
                      onClick={() =>
                        void exportAs(
                          [inventory.id],
                          `${fileNameOf(inventory.name)}.json`
                        )
                      }
                    />
                    {canEdit && (
                      <>
                        <RowAction
                          label="Edit"
                          name={inventory.name}
                          onClick={() => void open(inventory.id, 'form')}
                        />
                        <RowAction
                          label="Edit JSON"
                          name={inventory.name}
                          onClick={() => void open(inventory.id, 'json')}
                        />
                        <RowAction
                          label="Delete"
                          name={inventory.name}
                          onClick={() => {
                            setRefusal(null);
                            show({ kind: 'delete', inventory });
                          }}
                        />
                      </>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {editing?.kind === 'import' && (
        <ImportDialog onClose={() => show(null)} onImported={onChanged} />
      )}
      {editing?.kind === 'create' && (
        <InventoryDialog onClose={() => show(null)} onSaved={onChanged} />
      )}
      {editing?.kind === 'form' && (
        <InventoryDialog
          inventory={editing.inventory}
          onClose={() => show(null)}
          onSaved={onChanged}
        />
      )}
      {editing?.kind === 'json' && (
        <Suspense fallback={null}>
          <InventoryJSONDialog
            inventory={editing.inventory}
            onClose={() => show(null)}
            onSaved={onChanged}
          />
        </Suspense>
      )}
      {editing?.kind === 'delete' && (
        <ConfirmDialog
          visible
          title={ts('Delete inventory')}
          buttonText={ts('Delete')}
          initialFocus="cancel"
          dismissModal={() => show(null)}
          onSubmit={() => void remove(editing.inventory)}
        >
          <p className="text-sm">
            <I18nTemplate
              text="Delete {name} and its {count} nodes? Their trusted host keys are forgotten, and steps no longer match them."
              values={{
                name: <strong>{editing.inventory.name}</strong>,
                count: editing.inventory.nodeCount,
              }}
            />
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}

function RowAction({
  label,
  name,
  onClick,
}: {
  label: string;
  name: string;
  onClick: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  return (
    <Button
      size="sm"
      variant="ghost"
      className="h-7"
      aria-label={`${ts(label)} ${name}`}
      onClick={onClick}
    >
      <I18nText text={label} />
    </Button>
  );
}

/** A file name for an inventory, with any character a file system may refuse replaced. */
function fileNameOf(name: string): string {
  return (
    name.replace(/[^A-Za-z0-9_.-]+/g, '-').replace(/^-+|-+$/g, '') ||
    'inventory'
  );
}
