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
  const notifications: Message[] = [];
  // The content's height, which resize changes, and the observers it wakes.
  let contentHeight = CONTENT_HEIGHT;
  const observers: (() => void)[] = [];
  const host = {
    postMessage(message: Message) {
      if (message.id === undefined && message.method) {
        notifications.push(message);
        return;
      }
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
          constructor(callback: () => void) {
            observers.push(callback);
          }
          observe() {}
          disconnect() {}
        },
      });
      // The host's frame is taller than the content, which fills it unless
      // measured at max-content.
      Object.defineProperty(
        window.HTMLHtmlElement.prototype,
        'getBoundingClientRect',
        {
          value(this: HTMLElement) {
            const height =
              this.style.height === 'max-content'
                ? contentHeight
                : FRAME_HEIGHT;
            return { height, width: 600, top: 0, left: 0, bottom: height };
          },
        }
      );
      Object.defineProperty(window.HTMLHtmlElement.prototype, 'scrollHeight', {
        get: () => FRAME_HEIGHT,
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
    /** Changes the content's height, as the browser reports a resize. */
    resize: (height: number) => {
      contentHeight = height;
      for (const observe of observers) observe();
    },
    /** The heights the inspector asked the host to give its frame. */
    heights: () =>
      notifications
        .filter((n) => n.method === 'ui/notifications/size-changed')
        .map((n) => n.params?.height),
    /** The tool calls and resource reads that the inspector made. */
    calls: () => requests.filter((r) => r.method !== 'ui/initialize'),
    notify: (method: string, params: Record<string, unknown>) =>
      deliver({ jsonrpc: '2.0', method, params }),
    steps: () =>
      Array.from(document.querySelectorAll('#steps .step-name')).map(
        (step) => step.textContent
      ),
    status: () => document.getElementById('run-status')?.textContent,
    log: () => document.getElementById('log-output')?.textContent,
    message: () => document.getElementById('message')?.textContent,
    eyebrow: () => document.querySelector('.eyebrow')?.textContent,
    title: () => document.getElementById('run-name')?.textContent,
    clickStepLog: (name: string) =>
      Array.from(document.querySelectorAll('#steps .step'))
        .find((row) => row.querySelector('.step-name')?.textContent === name)
        ?.querySelector<HTMLButtonElement>('.step-log')
        ?.click(),
    click: (id: string) =>
      (document.getElementById(id) as HTMLButtonElement).click(),
    button: (id: string) => document.getElementById(id) as HTMLButtonElement,
  };
}

// The heights of the inspector's content and of the frame the host gives it.
const CONTENT_HEIGHT = 120;
const FRAME_HEIGHT = 900;

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

  it('sends the server with step logs, Logs, and Stop', async () => {
    const inspector = openInspector(
      { serverTools: {}, serverResources: {} },
      (method, params) => {
        const args = params?.arguments as Record<string, unknown>;
        if (params?.name === 'dagu_execute') {
          return {
            structuredContent: fromCloud({ dagName: 'etl', dagRunId: 'r1' }),
          };
        }
        if (args?.target === 'step_log') {
          return {
            structuredContent: fromCloud({
              target: 'step_log',
              stepName: 'load',
              data: { stdoutContent: 'loaded 3 rows' },
            }),
          };
        }
        if (args?.uri === `${runURI}/logs`) {
          return {
            structuredContent: fromCloud({
              target: 'run_logs',
              data: { schedulerLog: { content: 'scheduler says hi' } },
            }),
          };
        }
        if (args?.uri === runURI) {
          return {
            structuredContent: fromCloud({
              target: 'run',
              data: normalizedRun('aborted'),
            }),
          };
        }
        throw new Error(`unexpected ${method}`);
      }
    );
    await waitForInit(inspector);
    inspector.notify('ui/notifications/tool-result', {
      structuredContent: fromCloud({
        target: 'run',
        uri: runURI,
        data: normalizedRun('running'),
      }),
    });
    await vi.waitFor(() => expect(inspector.status()).toBe('running'));

    inspector.clickStepLog('load');
    await vi.waitFor(() => expect(inspector.log()).toBe('loaded 3 rows'));
    inspector.click('logs');
    await vi.waitFor(() => expect(inspector.log()).toBe('scheduler says hi'));
    inspector.click('stop');
    await vi.waitFor(() => expect(inspector.status()).toBe('aborted'));

    const server = 't1/42';
    expect(inspector.calls().map((call) => call.params)).toEqual([
      {
        name: 'dagu_read',
        arguments: {
          target: 'step_log',
          name: 'etl',
          dagRunId: 'r1',
          stepName: 'load',
          server,
        },
      },
      { name: 'dagu_read', arguments: { uri: `${runURI}/logs`, server } },
      {
        name: 'dagu_execute',
        arguments: {
          action: 'stop',
          targetType: 'run',
          name: 'etl',
          dagRunId: 'r1',
          server,
        },
      },
      { name: 'dagu_read', arguments: { uri: runURI, server } },
    ]);
  });

  it('keeps no server from a call that named one', async () => {
    const inspector = openInspector(
      { serverTools: {}, serverResources: {} },
      () => ({
        contents: [
          {
            uri: runURI,
            text: JSON.stringify({ dagRunDetails: normalizedRun('failed') }),
          },
        ],
      })
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
    await vi.waitFor(() => expect(inspector.status()).toBe('failed'));

    // A later call on the server's own MCP names no server.
    inspector.notify('ui/notifications/tool-input', {
      arguments: { target: 'run', name: 'etl', dagRunId: 'r1' },
    });
    inspector.notify('ui/notifications/tool-result', {
      structuredContent: {
        target: 'run',
        uri: runURI,
        data: normalizedRun('failed'),
      },
    });
    inspector.click('refresh');
    await vi.waitFor(() => expect(inspector.calls()).toHaveLength(1));
    expect(inspector.calls()[0]).toMatchObject({
      method: 'resources/read',
      params: { uri: runURI },
    });
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

  // A read that is not about a run, such as the DAG list, shows as a short
  // result, and the frame shrinks to it rather than keeping its height.
  it('fits a read that is not about a run', async () => {
    const inspector = openInspector({ serverTools: {} });
    await waitForInit(inspector);
    inspector.notify('ui/notifications/tool-result', {
      content: [{ type: 'text', text: 'Dagu read completed.' }],
      structuredContent: { target: 'reference' },
    });

    await vi.waitFor(() => expect(inspector.title()).toBe('Dagu result'));
    expect(inspector.eyebrow()).toBe('Dagu');
    expect(inspector.message()).toBe('Dagu read completed.');
    await vi.waitFor(() =>
      expect(inspector.heights()).toContain(CONTENT_HEIGHT)
    );
    expect(inspector.heights()).not.toContain(FRAME_HEIGHT);
  });

  // Content that grows and then shrinks, as when a run's logs open and a
  // shorter result replaces them, takes the frame down with it.
  it('shrinks the frame when the content does', async () => {
    const inspector = openInspector({ serverTools: {} });
    await waitForInit(inspector);
    const lastHeight = () => inspector.heights().slice(-1)[0];

    inspector.resize(640);
    await vi.waitFor(() => expect(lastHeight()).toBe(640));
    inspector.resize(CONTENT_HEIGHT);
    await vi.waitFor(() => expect(lastHeight()).toBe(CONTENT_HEIGHT));
  });

  it('says so when a read returns no data', async () => {
    const inspector = openInspector({ serverTools: {} }, () => ({
      content: [{ type: 'text', text: 'Dagu read completed.' }],
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

    inspector.click('refresh');
    await vi.waitFor(() =>
      expect(inspector.message()).toBe('The read did not return data.')
    );
    expect(inspector.steps()).toEqual(['extract', 'load']);
  });
});
