// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { useClient } from '@/hooks/api';

export type Inventory = components['schemas']['Inventory'];
export type InventoryDefaults = components['schemas']['InventoryDefaults'];
export type InventoryNodeSpec = components['schemas']['InventoryNodeSpec'];
type RuleError = components['schemas']['InventoryRuleError'];
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
    const message = error instanceof Error ? error.message : String(error);
    return { refusal: { message, rules: [] } };
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
    return { failure: error instanceof Error ? error.message : String(error) };
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

/** Adds node to the inventory id, based on version, or replaces the node of that name. */
export function putNode(
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
