// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { Input } from '@/components/ui/input';
import { useClient } from '@/hooks/api';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Field } from './inventories/FormField';
import {
  deleteNode,
  readInventory,
  replaceNode,
  type ChangeRefusal,
  type Inventory,
  type InventoryNodeSpec,
} from './inventories/inventoryChanges';
import { RefusalNotice } from './inventories/RefusalNotice';

type InventoryNode = components['schemas']['InventoryNode'];

/** The node's own record, and the inventory version it was read at. */
type OwnRecord = { inventory: Inventory; spec: InventoryNodeSpec };

/**
 * Edits a node's own labels, disables or enables it, and removes it. The
 * drawer shows the node with its inventory's defaults applied, so the node's
 * own record is read from its inventory first; a change keeps every other
 * field of it.
 */
export function NodeEditor({
  node,
  onChanged,
  onRemoved,
}: {
  node: InventoryNode;
  onChanged: () => void;
  onRemoved: () => void;
}): React.ReactElement {
  const client = useClient();
  const { ts } = useI18n();
  const [record, setRecord] = useState<OwnRecord | null>(null);
  const [labels, setLabels] = useState('');
  const [problem, setProblem] = useState<string | null>(null);
  const [refusal, setRefusal] = useState<ChangeRefusal | null>(null);
  const [busy, setBusy] = useState(false);
  const [removing, setRemoving] = useState(false);
  // Counts the reads, so an older read that finishes late is dropped.
  const readsRef = useRef(0);
  // The editor is shown only for an SSH node, which has an inventory.
  const inventoryId = node.inventory?.id ?? '';

  const show = useCallback(
    (inventory: Inventory) => {
      const spec = inventory.nodes.find(
        (n) => n.name.toLowerCase() === node.name.toLowerCase()
      );
      if (!spec) {
        setRecord(null);
        setProblem(ts('The node is no longer in its inventory.'));
        return;
      }
      setRecord({ inventory, spec });
      setLabels((spec.labels ?? []).join(', '));
      setProblem(null);
    },
    [node.name, ts]
  );

  const read = useCallback(async () => {
    const attempt = ++readsRef.current;
    const read = await readInventory(client, inventoryId);
    if (attempt !== readsRef.current) {
      return;
    }
    if ('failure' in read) {
      setProblem(read.failure || ts("Couldn't read the inventory."));
      return;
    }
    show(read);
  }, [client, inventoryId, show, ts]);

  useEffect(() => {
    void read();
  }, [read]);

  /** Stores spec in place of the node's record, and reads it again. */
  async function save(spec: InventoryNodeSpec) {
    if (!record) {
      return;
    }
    setBusy(true);
    setRefusal(null);
    try {
      const { refusal } = await replaceNode(
        client,
        record.inventory.id,
        record.inventory.version,
        spec
      );
      if (refusal) {
        setRefusal(refusal);
        return;
      }
      onChanged();
      await read();
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setRemoving(false);
    if (!record) {
      return;
    }
    setBusy(true);
    setRefusal(null);
    try {
      const { refusal } = await deleteNode(
        client,
        record.inventory.id,
        record.inventory.version,
        record.spec.name
      );
      if (refusal) {
        setRefusal(refusal);
        return;
      }
      onRemoved();
    } finally {
      setBusy(false);
    }
  }

  const spec = record?.spec;
  const ownLabels = labels
    .split(',')
    .map((label) => label.trim())
    .filter(Boolean);

  return (
    <section className="space-y-3">
      <h3 className="border-b border-border pb-2 text-xs font-semibold text-muted-foreground">
        <I18nText text={'Edit node'} />
      </h3>
      <Field label="Node labels">
        <Input
          value={labels}
          disabled={!spec || busy}
          placeholder="role=web, canary"
          onChange={(event) => setLabels(event.target.value)}
        />
      </Field>
      <p className="text-xs text-muted-foreground">
        <I18nText text={"The inventory's default labels are added to these."} />
      </p>
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          disabled={!spec || busy}
          onClick={() => {
            if (!spec) return;
            const next: InventoryNodeSpec = { ...spec };
            if (ownLabels.length > 0) {
              next.labels = ownLabels;
            } else {
              delete next.labels;
            }
            void save(next);
          }}
        >
          <I18nText text={'Save labels'} />
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={!spec || busy}
          onClick={() => {
            if (!spec) return;
            const next: InventoryNodeSpec = { ...spec };
            if (spec.disabled) {
              delete next.disabled;
            } else {
              next.disabled = true;
            }
            void save(next);
          }}
        >
          {spec?.disabled ? (
            <I18nText text={'Enable'} />
          ) : (
            <I18nText text={'Disable'} />
          )}
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="text-error"
          disabled={!spec || busy}
          onClick={() => setRemoving(true)}
        >
          <I18nText text={'Remove'} />
        </Button>
      </div>
      {problem && (
        <p role="alert" className="text-xs text-error">
          {problem}
        </p>
      )}
      {refusal && (
        <RefusalNotice
          refusal={refusal}
          onLoadCurrent={(current) => {
            setRefusal(null);
            show(current);
          }}
        />
      )}
      {removing && record && (
        <ConfirmDialog
          visible
          title={ts('Remove node')}
          buttonText={ts('Remove')}
          initialFocus="cancel"
          dismissModal={() => setRemoving(false)}
          onSubmit={() => void remove()}
        >
          <p className="text-sm">
            <I18nTemplate
              text="Remove {name} from {inventory}? Its trusted host key is forgotten, and steps no longer run on it."
              values={{
                name: <strong>{record.spec.name}</strong>,
                inventory: record.inventory.name,
              }}
            />
          </p>
        </ConfirmDialog>
      )}
    </section>
  );
}
