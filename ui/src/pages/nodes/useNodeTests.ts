// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { useCallback, useEffect, useRef, useState } from 'react';
import { postNodeEvents } from './nodeEvents';

type ConnectionTest = components['schemas']['InventoryNodeConnectionTest'];
type ConnectionTestSummary =
  components['schemas']['InventoryNodeConnectionTestSummary'];
type HostKeyTrust = components['schemas']['InventoryHostKeyTrust'];
type HostKeyTrustSummary =
  components['schemas']['InventoryHostKeyTrustSummary'];
type HostKeyTrustEntry = components['schemas']['InventoryHostKeyTrustEntry'];

/** A host key that stopped a test, and that a trust can store. */
export type KeyOffer = {
  /** The node whose key it is: the node tested, or its bastion. */
  node: string;
  presented: string;
  /** The key trusted for the node, which this one replaces. */
  trusted?: string;
};

/** The nodes of the last test, and how far it got. */
export type NodeTestRun = {
  names: string[];
  /** Lowercased names of the nodes whose test has not finished. */
  pending: ReadonlySet<string>;
  error?: string;
};

/** Node names are unique without regard to case. */
export const nodeKey = (name: string): string => name.toLowerCase();

/**
 * The keys that stopped the tests of names and that a trust can store, one
 * per node. A changed key is offered only in place of a trusted key: a key
 * that differs from the node's pinned host_key alone is fixed in the
 * inventory.
 */
export function keyOffers(
  names: string[],
  results: Record<string, ConnectionTest>
): KeyOffer[] {
  const offers = new Map<string, KeyOffer>();
  for (const name of names) {
    const refused = results[nodeKey(name)]?.hostKey;
    if (
      !refused?.presented ||
      (refused.expected.length > 0 && !refused.trusted)
    ) {
      continue;
    }
    if (!offers.has(nodeKey(refused.node))) {
      offers.set(nodeKey(refused.node), {
        node: refused.node,
        presented: refused.presented,
        trusted: refused.trusted,
      });
    }
  }
  return [...offers.values()];
}

/**
 * Tests node connections from the server and trusts host keys. `results`
 * keeps the last result of each node for the page's lifetime; `runResults`
 * holds only the results of the current run. After a trust, the nodes the
 * trusted keys stopped are tested again.
 */
export function useNodeTests(apiURL: string) {
  const [results, setResults] = useState<Record<string, ConnectionTest>>({});
  const [runResults, setRunResults] = useState<Record<string, ConnectionTest>>(
    {}
  );
  const [trusts, setTrusts] = useState<Record<string, HostKeyTrust>>({});
  const [run, setRun] = useState<NodeTestRun | null>(null);
  const [trusting, setTrusting] = useState(false);
  // The current run's results as the stream delivers them, for a trust to
  // read which nodes its keys stopped.
  const runResultsRef = useRef<Record<string, ConnectionTest>>({});
  const abortRef = useRef<AbortController | null>(null);
  // Changes when a run starts or closes, so a trust of an older run does
  // not test again.
  const generationRef = useRef(0);

  useEffect(() => () => abortRef.current?.abort(), []);

  const fail = useCallback((error: unknown) => {
    if (error instanceof DOMException && error.name === 'AbortError') {
      return;
    }
    const message = error instanceof Error ? error.message : String(error);
    setRun((prev) => prev && { ...prev, pending: new Set(), error: message });
  }, []);

  /**
   * Tests names. A new run replaces the last one. A retest keeps the run's
   * other results and its error, such as a trust that did not finish.
   */
  const testNodes = useCallback(
    async (names: string[], retest: boolean) => {
      const controller = new AbortController();
      abortRef.current = controller;
      const pending = new Set(names.map(nodeKey));
      if (retest) {
        for (const key of pending) {
          delete runResultsRef.current[key];
        }
        setRun((prev) => prev && { ...prev, pending });
      } else {
        generationRef.current += 1;
        runResultsRef.current = {};
        setRun({ names, pending });
        setTrusts({});
      }
      setRunResults({ ...runResultsRef.current });
      try {
        await postNodeEvents<ConnectionTest, ConnectionTestSummary>(
          apiURL,
          '/node-connection-tests',
          { nodes: names },
          (result) => {
            const key = nodeKey(result.node);
            runResultsRef.current[key] = result;
            setRunResults({ ...runResultsRef.current });
            setResults((prev) => ({ ...prev, [key]: result }));
            setRun((prev) => {
              if (!prev) {
                return prev;
              }
              const rest = new Set(prev.pending);
              rest.delete(key);
              return { ...prev, pending: rest };
            });
          },
          controller.signal
        );
      } catch (error) {
        fail(error);
      }
    },
    [apiURL, fail]
  );

  const test = useCallback(
    (names: string[]) => testNodes(names, false),
    [testNodes]
  );

  /** Reports why a test could not start, as a run's error. */
  const refuse = useCallback((message: string) => {
    generationRef.current += 1;
    runResultsRef.current = {};
    setRunResults({});
    setRun({ names: [], pending: new Set(), error: message });
  }, []);

  const trust = useCallback(
    async (offers: KeyOffer[], names: string[]) => {
      const generation = generationRef.current;
      const controller = new AbortController();
      abortRef.current = controller;
      const trusted = new Set<string>();
      setTrusting(true);
      try {
        await postNodeEvents<HostKeyTrust, HostKeyTrustSummary>(
          apiURL,
          '/node-host-keys',
          {
            keys: offers.map(
              (offer): HostKeyTrustEntry => ({
                node: offer.node,
                fingerprint: offer.presented,
                replace: offer.trusted,
              })
            ),
          },
          (result) => {
            setTrusts((prev) => ({ ...prev, [nodeKey(result.node)]: result }));
            if (!result.error) {
              trusted.add(nodeKey(result.node));
            }
          },
          controller.signal
        );
      } catch (error) {
        fail(error);
      } finally {
        setTrusting(false);
      }
      if (controller.signal.aborted || generation !== generationRef.current) {
        return;
      }
      const retest = names.filter((name) => {
        const refused = runResultsRef.current[nodeKey(name)]?.hostKey;
        return !!refused && trusted.has(nodeKey(refused.node));
      });
      if (retest.length > 0) {
        await testNodes(retest, true);
      }
    },
    [apiURL, fail, testNodes]
  );

  const close = useCallback(() => {
    generationRef.current += 1;
    abortRef.current?.abort();
    setRun(null);
  }, []);

  return {
    results,
    runResults,
    trusts,
    run,
    running: !!run && run.pending.size > 0,
    trusting,
    test,
    refuse,
    trust,
    close,
  };
}
