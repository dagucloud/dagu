// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useConfig } from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import React, { useState } from 'react';
import { Field } from './inventories/FormField';
import {
  addNode,
  type ChangeRefusal,
  type InventoryNodeSpec,
} from './inventories/inventoryChanges';
import { RefusalNotice } from './inventories/RefusalNotice';
import { postNodeEvents } from './nodeEvents';
import { TestOutcome } from './NodeTestPanel';

type InventorySummary = components['schemas']['InventorySummary'];
type ConnectionTest = components['schemas']['InventoryNodeConnectionTest'];
type AcceptedKey = components['schemas']['InventoryAcceptedHostKey'];

type Fields = {
  inventoryId: string;
  name: string;
  host: string;
  port: string;
  user: string;
  keyFile: string;
  keySecret: string;
  passwordSecret: string;
  labels: string;
  description: string;
};

/**
 * A test, the fields it ran with, and the keys accepted for it, a bastion's
 * first. A change to the fields makes it stale.
 */
type Tested = {
  fields: string;
  accept: AcceptedKey[];
  result?: ConnectionTest;
  error?: string;
};

/**
 * Adds an SSH node to an inventory, as `dagu nodes add` does. The server
 * tests the node before it is stored; an unknown host key is shown and,
 * when the user accepts it, tested again with that key accepted for that
 * test only. The node is added once its test passes, and the accepted keys
 * are trusted after it is stored.
 */
export function AddNodeDialog({
  inventories,
  onClose,
  onAdded,
}: {
  inventories: InventorySummary[];
  onClose: () => void;
  onAdded: () => void;
}): React.ReactElement {
  const client = useClient();
  const { apiURL } = useConfig();
  const { ts } = useI18n();
  const [fields, setFields] = useState<Fields>(() => ({
    ...EMPTY,
    inventoryId: inventories[0]?.id ?? '',
  }));
  const [tested, setTested] = useState<Tested | null>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [refusal, setRefusal] = useState<ChangeRefusal | null>(null);
  // An inventory's version as loaded after a change refused as stale.
  const [loaded, setLoaded] = useState<{ id: string; version: number } | null>(
    null
  );

  const built = nodeOf(fields);
  const fieldsKey = JSON.stringify(fields);
  const test = tested?.fields === fieldsKey ? tested : null;
  const accepted = test?.accept ?? [];
  const refused = test?.result?.hostKey;
  const unknownKey =
    refused?.status === 'unknown' &&
    refused.presented &&
    !accepted.some((key) => key.node === refused.node)
      ? { node: refused.node, hop: refused.hop, fingerprint: refused.presented }
      : null;

  const set = (name: keyof Fields) => (value: string) => {
    setFields((prev) => ({ ...prev, [name]: value }));
  };

  async function runTest(accept: AcceptedKey[]) {
    if (typeof built === 'string') {
      setProblem(ts(built));
      return;
    }
    setProblem(null);
    setBusy(true);
    let result: ConnectionTest | undefined;
    try {
      await postNodeEvents<ConnectionTest, unknown>(
        apiURL,
        `/inventories/${encodeURIComponent(fields.inventoryId)}/node-tests`,
        { node: built, accept: accept.length > 0 ? accept : undefined },
        (event) => {
          result = event;
        }
      );
      setTested({ fields: fieldsKey, accept, result });
    } catch (error) {
      setTested({ fields: fieldsKey, accept, error: messageOf(error) });
    } finally {
      setBusy(false);
    }
  }

  async function add(trust: AcceptedKey[]) {
    if (typeof built === 'string') {
      setProblem(ts(built));
      return;
    }
    const inventory = inventories.find((i) => i.id === fields.inventoryId);
    if (!inventory) {
      return;
    }
    setProblem(null);
    setRefusal(null);
    setBusy(true);
    try {
      const { refusal } = await addNode(
        client,
        inventory.id,
        loaded?.id === inventory.id ? loaded.version : inventory.version,
        built,
        trust
      );
      if (refusal) {
        setRefusal(refusal);
        return;
      }
      onAdded();
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[600px]">
        <DialogHeader>
          <DialogTitle>
            <I18nText text={'Add node'} />
          </DialogTitle>
          <DialogDescription>
            <I18nText
              text={
                'The server tests the connection before the node is added. An unknown host key is trusted only once the node is added.'
              }
            />
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <Select
              value={fields.inventoryId}
              onValueChange={set('inventoryId')}
            >
              <I18nProps>
                <SelectTrigger className="h-8" aria-label="Inventory">
                  <SelectValue />
                </SelectTrigger>
              </I18nProps>
              <SelectContent>
                {inventories.map((inventory) => (
                  <SelectItem key={inventory.id} value={inventory.id}>
                    {inventory.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Field label="Name">
            <Input
              value={fields.name}
              onChange={(e) => set('name')(e.target.value)}
            />
          </Field>
          <Field label="Host">
            <Input
              placeholder="10.0.1.11"
              value={fields.host}
              onChange={(e) => set('host')(e.target.value)}
            />
          </Field>
          <Field label="Port">
            <Input
              inputMode="numeric"
              placeholder="22"
              value={fields.port}
              onChange={(e) => set('port')(e.target.value)}
            />
          </Field>
          <Field label="User">
            <Input
              value={fields.user}
              onChange={(e) => set('user')(e.target.value)}
            />
          </Field>
          <Field label="Key file">
            <Input
              placeholder="~/.ssh/id_ed25519"
              value={fields.keyFile}
              onChange={(e) => set('keyFile')(e.target.value)}
            />
          </Field>
          <Field label="Key secret">
            <Input
              value={fields.keySecret}
              onChange={(e) => set('keySecret')(e.target.value)}
            />
          </Field>
          <Field label="Password secret">
            <Input
              value={fields.passwordSecret}
              onChange={(e) => set('passwordSecret')(e.target.value)}
            />
          </Field>
          <Field label="Labels">
            <Input
              placeholder="role=web, canary"
              value={fields.labels}
              onChange={(e) => set('labels')(e.target.value)}
            />
          </Field>
          <Field label="Description" className="sm:col-span-2">
            <Input
              value={fields.description}
              onChange={(e) => set('description')(e.target.value)}
            />
          </Field>
        </div>

        <div aria-live="polite" className="space-y-2 text-xs">
          {busy && (
            <p className="text-muted-foreground">
              <I18nText text={'Testing...'} />
            </p>
          )}
          {test?.result && <TestOutcome result={test.result} />}
          {test?.error && (
            <p role="alert" className="text-error">
              {test.error}
            </p>
          )}
          {unknownKey && (
            <div className="flex flex-wrap items-center gap-2">
              <span>
                <I18nTemplate
                  text="The host key of {hop} {node} is unknown: {fingerprint}"
                  values={{
                    hop: unknownKey.hop,
                    node: <code>{unknownKey.node}</code>,
                    fingerprint: (
                      <code className="break-all">
                        {unknownKey.fingerprint}
                      </code>
                    ),
                  }}
                />
              </span>
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() =>
                  void runTest([
                    ...accepted,
                    {
                      node: unknownKey.node,
                      fingerprint: unknownKey.fingerprint,
                    },
                  ])
                }
              >
                <I18nText text={'Trust this key and test again'} />
              </Button>
            </div>
          )}
          {problem && (
            <p role="alert" className="text-error">
              {problem}
            </p>
          )}
          {refusal && (
            <RefusalNotice
              refusal={refusal}
              onLoadCurrent={(current) => {
                setLoaded({ id: current.id, version: current.version });
                // The inventory's defaults may have changed since the test.
                setTested(null);
                setRefusal(null);
              }}
            />
          )}
        </div>

        <DialogFooter className="gap-2 sm:justify-between">
          <Button
            type="button"
            variant="ghost"
            disabled={busy}
            onClick={() => void add([])}
          >
            <I18nText text={'Add without testing'} />
          </Button>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              <I18nText text={'Cancel'} />
            </Button>
            {/* A new test starts with no key accepted, so a key the host
                no longer presents is not sent again. */}
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => void runTest([])}
            >
              <I18nText text={'Test connection'} />
            </Button>
            <Button
              type="button"
              disabled={busy || !test?.result?.ok}
              onClick={() => void add(accepted)}
            >
              <I18nText text={'Add node'} />
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const EMPTY: Omit<Fields, 'inventoryId'> = {
  name: '',
  host: '',
  port: '',
  user: '',
  keyFile: '',
  keySecret: '',
  passwordSecret: '',
  labels: '',
  description: '',
};

/** The node record the fields make, or why they make none. */
function nodeOf(fields: Fields): InventoryNodeSpec | string {
  const port = fields.port.trim();
  if (port && !/^\d+$/.test(port)) {
    return 'The port must be a whole number.';
  }
  const keyFile = fields.keyFile.trim();
  const keySecret = fields.keySecret.trim();
  if (keyFile && keySecret) {
    return 'Give a key file or a key secret, not both.';
  }
  const node: InventoryNodeSpec = {
    name: fields.name.trim(),
    host: fields.host.trim(),
  };
  if (port) {
    node.port = Number(port);
  }
  if (fields.user.trim()) {
    node.user = fields.user.trim();
  }
  if (keyFile) {
    node.key = keyFile;
  } else if (keySecret) {
    node.key = { secret: keySecret };
  }
  if (fields.passwordSecret.trim()) {
    node.password = { secret: fields.passwordSecret.trim() };
  }
  const labels = fields.labels
    .split(',')
    .map((label) => label.trim())
    .filter(Boolean);
  if (labels.length > 0) {
    node.labels = labels;
  }
  if (fields.description.trim()) {
    node.description = fields.description.trim();
  }
  return node;
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
