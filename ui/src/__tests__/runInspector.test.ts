// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { JSDOM } from 'jsdom';
import { describe, expect, it, vi } from 'vitest';

// The run inspector is the MCP app that the server's MCP tools attach. It
// runs in the AI host's frame and talks to the host over postMessage.
const inspectorHTML = readFileSync(
  path.resolve(
    __dirname,
    '../../../internal/service/mcp/app/run-inspector.html'
  ),
  'utf8'
);

type Message = {
  jsonrpc: '2.0';
  id?: number;
  method?: string;
  params?: Record<string, unknown>;
  result?: unknown;
};

type Capabilities = { serverTools?: object; serverResources?: object };

/**
 * Opens the inspector in a host with the given capabilities. The host answers
 * each request of the inspector with answer, and records what it was sent.
 */
function openInspector(
  hostCapabilities: Capabilities,
  answer: (method: string, params: Message['params']) => unknown = () => ({})
) {
  const requests: Message[] = [];
  const host = {
    postMessage(message: Message) {
      if (message.id === undefined || !message.method) return;
      requests.push(message);
      const result =
        message.method === 'ui/initialize'
          ? { hostCapabilities }
          : answer(message.method, message.params);
      setTimeout(() => deliver({ jsonrpc: '2.0', id: message.id, result }));
    },
  };
  const dom = new JSDOM(inspectorHTML, {
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    beforeParse(window) {
      Object.defineProperty(window, 'parent', { value: host });
      Object.defineProperty(window, 'ResizeObserver', {
        value: class {
          observe() {}
          disconnect() {}
        },
      });
    },
  });
  const { document } = dom.window;

  function deliver(data: Message) {
    const event = new dom.window.MessageEvent('message', { data });
    Object.defineProperty(event, 'source', { value: host });
    dom.window.dispatchEvent(event);
  }

  return {
    requests,
    /** The tool calls and resource reads that the inspector made. */
    calls: () => requests.filter((r) => r.method !== 'ui/initialize'),
    notify: (method: string, params: Record<string, unknown>) =>
      deliver({ jsonrpc: '2.0', method, params }),
    steps: () =>
      Array.from(document.querySelectorAll('#steps .step-name')).map(
        (step) => step.textContent
      ),
    status: () => document.getElementById('run-status')?.textContent,
    click: (id: string) =>
      (document.getElementById(id) as HTMLButtonElement).click(),
    button: (id: string) => document.getElementById(id) as HTMLButtonElement,
  };
}

const runURI = 'dagu://runs/etl/r1';

function normalizedRun(statusLabel: string) {
  return {
    name: 'etl',
    dagRunId: 'r1',
    uri: runURI,
    logsUri: `${runURI}/logs`,
    statusLabel,
    steps: [
      { name: 'extract', statusLabel: 'succeeded' },
      { name: 'load', statusLabel },
    ],
  };
}

// What Dagu Cloud's tools answer: the server's own result inside.
function fromCloud(result: object) {
  return {
    server: { name: 'prod', workspace: 'acme', serverRef: 't1/42' },
    untrusted: true,
    result,
    links: [],
  };
}

// Waits until the host has answered ui/initialize.
async function waitForInit(inspector: ReturnType<typeof openInspector>) {
  await vi.waitFor(() => expect(inspector.requests).toHaveLength(1));
  await new Promise((resolve) => setTimeout(resolve));
}

describe('run inspector', () => {
  it('works on a run through Dagu Cloud, naming the server', async () => {
    const inspector = openInspector(
      { serverTools: {}, serverResources: {} },
      (method, params) => {
        const args = params?.arguments as Record<string, unknown>;
        if (method === 'tools/call' && params?.name === 'dagu_execute') {
          return {
            structuredContent: fromCloud({ dagName: 'etl', dagRunId: 'r1' }),
          };
        }
        if (method === 'tools/call' && args?.uri === runURI) {
          return {
            structuredContent: fromCloud({
              target: 'run',
              uri: runURI,
              data: normalizedRun('running'),
            }),
          };
        }
        throw new Error(`unexpected ${method}`);
      }
    );
    await waitForInit(inspector);
    inspector.notify('ui/notifications/tool-input', {
      arguments: { target: 'run', server: 'prod', name: 'etl', dagRunId: 'r1' },
    });
    inspector.notify('ui/notifications/tool-result', {
      structuredContent: fromCloud({
        target: 'run',
        uri: runURI,
        data: normalizedRun('failed'),
      }),
    });
    await vi.waitFor(() =>
      expect(inspector.steps()).toEqual(['extract', 'load'])
    );
    expect(inspector.status()).toBe('failed');
    // The result already holds the run: nothing is read again.
    expect(inspector.calls()).toEqual([]);

    inspector.click('retry');
    await vi.waitFor(() => expect(inspector.status()).toBe('running'));
    expect(inspector.calls().map((call) => call.params)).toEqual([
      {
        name: 'dagu_execute',
        arguments: {
          action: 'retry',
          targetType: 'run',
          name: 'etl',
          dagRunId: 'r1',
          server: 't1/42',
        },
      },
      { name: 'dagu_read', arguments: { uri: runURI, server: 't1/42' } },
    ]);
  });

  it('reads the server resources of its own MCP', async () => {
    const inspector = openInspector(
      { serverTools: {}, serverResources: {} },
      (method) => {
        if (method !== 'resources/read')
          throw new Error(`unexpected ${method}`);
        return {
          contents: [
            {
              uri: runURI,
              text: JSON.stringify({
                dagRunDetails: {
                  name: 'etl',
                  dagRunId: 'r1',
                  statusLabel: 'succeeded',
                  nodes: [{ step: { name: 'only' }, statusLabel: 'succeeded' }],
                },
              }),
            },
          ],
        };
      }
    );
    await waitForInit(inspector);
    inspector.notify('ui/notifications/tool-result', {
      structuredContent: {
        target: 'run',
        uri: runURI,
        data: normalizedRun('failed'),
      },
    });
    await vi.waitFor(() =>
      expect(inspector.steps()).toEqual(['extract', 'load'])
    );

    inspector.click('refresh');
    await vi.waitFor(() => expect(inspector.steps()).toEqual(['only']));
    expect(inspector.calls().map((call) => call.method)).toEqual([
      'resources/read',
    ]);
  });

  it('reads through dagu_read in a host that reads no resources', async () => {
    const inspector = openInspector({ serverTools: {} }, () => ({
      structuredContent: {
        target: 'run',
        uri: runURI,
        data: normalizedRun('succeeded'),
      },
    }));
    await waitForInit(inspector);
    inspector.notify('ui/notifications/tool-result', {
      structuredContent: {
        target: 'run',
        uri: runURI,
        data: normalizedRun('failed'),
      },
    });
    await vi.waitFor(() => expect(inspector.status()).toBe('failed'));
    expect(inspector.button('refresh').disabled).toBe(false);

    inspector.click('refresh');
    await vi.waitFor(() => expect(inspector.status()).toBe('succeeded'));
    expect(inspector.calls().map((call) => call.params)).toEqual([
      { name: 'dagu_read', arguments: { uri: runURI } },
    ]);
  });
});
