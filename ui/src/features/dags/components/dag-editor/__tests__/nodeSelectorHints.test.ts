// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from 'vitest';
import type { languages } from 'monaco-editor';
import {
  nodeSelectorCounter,
  nodeSelectorReader,
  registerNodeSelectorHints,
} from '../nodeSelectorHints';

const yaml = `steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes: web,!canary
  - id: check
    run: uptime
    nodes:
      - web
      - "!canary"
  - id: restart
    run: systemctl restart app
    nodes:
      labels: role=web
      max_concurrent: 2
  - id: env
    run: uptime
    nodes: env=\${params.env}
  - id: plain
    run: echo hi
`;

describe('nodeSelectorReader', () => {
  // A list joins with commas, as the server reads it; a selector with a
  // reference resolves only when the step starts.
  it('reads each literal selector and the last row of its value', () => {
    expect(nodeSelectorReader()(yaml)).toEqual([
      { selector: 'web,!canary', line: 6 },
      { selector: 'web,!canary', line: 11 },
      { selector: 'role=web', line: 15 },
    ]);
  });
});

describe('nodeSelectorCounter', () => {
  it('asks once for each selector and tells when a count arrives', async () => {
    const fetchCount = vi.fn(async (selector: string) =>
      selector === 'web' ? 3 : null
    );
    const onCount = vi.fn();
    const count = nodeSelectorCounter(fetchCount, onCount);

    expect(count('web')).toBeUndefined();
    expect(count('web')).toBeUndefined();
    expect(count('bad,')).toBeUndefined();
    await vi.waitFor(() => expect(onCount).toHaveBeenCalledTimes(1));

    expect(count('web')).toBe(3);
    // A selector the server refused stays without a count.
    expect(count('bad,')).toBeUndefined();
    expect(fetchCount).toHaveBeenCalledTimes(2);
  });
});

describe('registerNodeSelectorHints', () => {
  function fakeMonaco() {
    let provider: languages.InlayHintsProvider | null = null;
    const fire = vi.fn();
    const monaco = {
      Emitter: class {
        event = vi.fn();
        fire = fire;
        dispose = vi.fn();
      },
      languages: {
        registerInlayHintsProvider: (
          _id: string,
          next: languages.InlayHintsProvider
        ) => {
          provider = next;
          return { dispose: vi.fn() };
        },
      },
    };
    return { monaco, fire, provider: () => provider! };
  }

  it('puts the count at the end of the row of each counted selector', () => {
    const { monaco, fire, provider } = fakeMonaco();
    const model = {
      getLineCount: () => 31,
      getValue: () => yaml,
      getLineMaxColumn: (line: number) => line + 10,
    };
    const codeEditor = { getModel: () => model };
    const registration = registerNodeSelectorHints(
      monaco as never,
      codeEditor as never,
      () => ({
        points: nodeSelectorReader(),
        count: (selector) => (selector === 'role=web' ? 1 : undefined),
        label: (n) => `${n} node`,
      })
    );

    const result = provider().provideInlayHints(
      model as never,
      { startLineNumber: 1, endLineNumber: 31 } as never,
      {} as never
    ) as languages.InlayHintList;
    expect(result.hints).toEqual([
      {
        position: { lineNumber: 15, column: 25 },
        label: '1 node',
        paddingLeft: true,
      },
    ]);

    registration.refresh();
    expect(fire).toHaveBeenCalled();
  });
});
