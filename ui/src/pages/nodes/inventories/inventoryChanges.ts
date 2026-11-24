// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { useClient } from '@/hooks/api';

export type Inventory = components['schemas']['Inventory'];
export type InventoryDefaults = components['schemas']['InventoryDefaults'];
export type InventoryNodeSpec = components['schemas']['InventoryNodeSpec'];
type RuleError = components['schemas']['InventoryRuleError'];
type AcceptedKey = components['schemas']['InventoryAcceptedHostKey'];
type Client = ReturnType<typeof useClient>;

/** An inventory as `dagu nodes export` prints it and a change sends it. */
export type InventoryRecord = {
  name: string;
  description?: string;
  defaults?: InventoryDefaults;
  nodes: InventoryNodeSpec[];
};

/** Why the server refused a change. */
export type ChangeRefusal = {
  message: string;
  /** Each rule the change breaks, as `field: message`. */
  rules: string[];
  /** The inventory as it is now, when the change was based on an older version. */
  current?: Inventory;
};

/** The record of a stored inventory, without what Dagu sets. */
export function recordOf(inventory: Inventory): InventoryRecord {
  const record: InventoryRecord = {
    name: inventory.name,
    nodes: inventory.nodes,
  };
  if (inventory.description) {
    record.description = inventory.description;
  }
  if (inventory.defaults && Object.keys(inventory.defaults).length > 0) {
    record.defaults = inventory.defaults;
  }
  return record;
}

type Outcome = { refusal?: ChangeRefusal };

/**
 * The outcome of a change request: a refusal for an error response, and
 * for a request that failed to reach the server or to come back.
 */
async function outcomeOf(
  request: Promise<{ error?: unknown }>
): Promise<Outcome> {
  try {
    const { error } = await request;
    return { refusal: error ? refusalOf(error) : undefined };
  } catch (error) {
    return { refusal: { message: messageOf(error), rules: [] } };
  }
}

/** Reads the inventory id, or why it could not be read. */
export async function readInventory(
  client: Client,
  id: string
): Promise<Inventory | { failure: string }> {
  try {
    const { data, error } = await client.GET('/inventories/{inventoryId}', {
      params: { path: { inventoryId: id } },
    });
    return data ?? { failure: error?.message ?? '' };
  } catch (error) {
    return { failure: messageOf(error) };
  }
}

/** Creates an inventory from record. */
export function createInventory(
  client: Client,
  record: InventoryRecord
): Promise<Outcome> {
  return outcomeOf(client.POST('/inventories', { body: record }));
}

/** Replaces the inventory id, based on version, with record. */
export function replaceInventory(
  client: Client,
  id: string,
  version: number,
  record: InventoryRecord
): Promise<Outcome> {
  return outcomeOf(
    client.PUT('/inventories/{inventoryId}', {
      params: { path: { inventoryId: id }, query: { version } },
      body: record,
    })
  );
}

/**
 * Adds node to the inventory id, based on version, and then trusts the keys
 * accepted in its test. A name the inventory holds is refused.
 */
export function addNode(
  client: Client,
  id: string,
  version: number,
  node: InventoryNodeSpec,
  trust: AcceptedKey[]
): Promise<Outcome> {
  return outcomeOf(
    client.POST('/inventories/{inventoryId}/nodes', {
      params: { path: { inventoryId: id }, query: { version } },
      body: { node, trust: trust.length > 0 ? trust : undefined },
    })
  );
}

/** Replaces the node of node's name in the inventory id, based on version. */
export function replaceNode(
  client: Client,
  id: string,
  version: number,
  node: InventoryNodeSpec
): Promise<Outcome> {
  return outcomeOf(
    client.PUT('/inventories/{inventoryId}/nodes/{nodeName}', {
      params: {
        path: { inventoryId: id, nodeName: node.name },
        query: { version },
      },
      body: node,
    })
  );
}

/** Removes the node named name from the inventory id, based on version. */
export function deleteNode(
  client: Client,
  id: string,
  version: number,
  name: string
): Promise<Outcome> {
  return outcomeOf(
    client.DELETE('/inventories/{inventoryId}/nodes/{nodeName}', {
      params: { path: { inventoryId: id, nodeName: name }, query: { version } },
    })
  );
}

export type ImportResult = components['schemas']['InventoryImportResult'];

/**
 * Imports records as `dagu nodes import` does: with dryRun it only checks
 * them, and with replace it replaces an inventory whose name is taken.
 */
export async function importInventories(
  client: Client,
  records: unknown[],
  options: { replace: boolean; dryRun: boolean }
): Promise<{ result?: ImportResult; refusal?: ChangeRefusal }> {
  try {
    const { data, error } = await client.POST('/inventory-imports', {
      params: { query: options },
      body: records as { [key: string]: unknown }[],
    });
    return data ? { result: data } : { refusal: refusalOf(error) };
  } catch (error) {
    return { refusal: { message: messageOf(error), rules: [] } };
  }
}

/**
 * Reads the inventories ids as `dagu nodes export` prints them, or why one
 * could not be read.
 */
export async function exportRecords(
  client: Client,
  ids: string[]
): Promise<InventoryRecord[] | { failure: string }> {
  const records: InventoryRecord[] = [];
  for (const id of ids) {
    const read = await readInventory(client, id);
    if ('failure' in read) {
      return read;
    }
    records.push(recordOf(read));
  }
  return records;
}

/** Deletes the inventory id, based on version, with its nodes. */
export function deleteInventory(
  client: Client,
  id: string,
  version: number
): Promise<Outcome> {
  return outcomeOf(
    client.DELETE('/inventories/{inventoryId}', {
      params: { path: { inventoryId: id }, query: { version } },
    })
  );
}

function refusalOf(error: unknown): ChangeRefusal {
  const body = (error ?? {}) as {
    message?: string;
    errors?: RuleError[];
    inventory?: Inventory;
  };
  return {
    message: body.message ?? 'The change was refused.',
    rules: (body.errors ?? []).map(ruleText),
    current: body.inventory,
  };
}

function ruleText(rule: RuleError): string {
  const where = [rule.node && `node "${rule.node}"`, rule.field]
    .filter(Boolean)
    .join(': ');
  return where ? `${where}: ${rule.message}` : rule.message;
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
