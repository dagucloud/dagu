// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  CLI_PROVIDERS,
  dagHasAgentRun,
  isAgentRunValue,
  readAgentProvider,
  runsProcess,
  secretsNeedOptOut,
  trustOf,
  writtenTrust,
  type TrustStep,
} from '../agentRun';

describe('CLI_PROVIDERS', () => {
  it('lists each provider once and holds no chat-only provider', () => {
    expect(new Set(CLI_PROVIDERS).size).toBe(CLI_PROVIDERS.length);
    for (const name of ['anthropic', 'openai', 'local']) {
      expect(CLI_PROVIDERS as readonly string[]).not.toContain(name);
    }
  });

  // `gemini` and `opencode` are chat providers and built-in CLI providers.
  it('holds the built-in CLI providers that are also chat providers', () => {
    expect(CLI_PROVIDERS).toContain('gemini');
    expect(CLI_PROVIDERS).toContain('opencode');
  });
});

describe('readAgentProvider', () => {
  it('reads a literal provider', () => {
    expect(
      readAgentProvider({ with: { provider: 'claude', prompt: 'x' } })
    ).toBe('claude');
    expect(readAgentProvider({ with: { provider: ' codex ' } })).toBe('codex');
  });

  it('gives no provider for a missing, empty or computed value', () => {
    expect(readAgentProvider({})).toBeUndefined();
    expect(readAgentProvider({ with: 'x' })).toBeUndefined();
    expect(readAgentProvider({ with: { prompt: 'x' } })).toBeUndefined();
    expect(readAgentProvider({ with: { provider: '' } })).toBeUndefined();
    expect(readAgentProvider({ with: { provider: 3 } })).toBeUndefined();
    expect(
      readAgentProvider({ with: { provider: '${PROVIDER}' } })
    ).toBeUndefined();
  });
});

const shell = (value: Record<string, unknown> = {}): TrustStep => ({
  kind: 'run',
  value: { run: 'make', ...value },
});
const action = (
  name: string,
  value: Record<string, unknown> = {}
): TrustStep => ({
  kind: 'action',
  action: name,
  value: { action: name, ...value },
});

describe('dagHasAgentRun', () => {
  it('finds an agent step in the steps and in the handlers', () => {
    expect(
      dagHasAgentRun({ steps: [shell(), action('agent.run')], handlers: [] })
    ).toBe(true);
    expect(
      dagHasAgentRun({ steps: [shell()], handlers: [action('agent.run')] })
    ).toBe(true);
  });

  it('finds none in a DAG without an agent step', () => {
    expect(
      dagHasAgentRun({
        steps: [shell(), action('chat.completion')],
        handlers: [],
      })
    ).toBe(false);
  });
});

describe('runsProcess', () => {
  it('is true for run, command, script and harness.run', () => {
    expect(runsProcess(shell())).toBe(true);
    expect(runsProcess(action('harness.run'))).toBe(true);
  });

  it('is false for another action, a container step and another executor', () => {
    expect(runsProcess(action('http.request'))).toBe(false);
    expect(runsProcess(action('agent.run'))).toBe(false);
    expect(runsProcess(shell({ container: { image: 'alpine' } }))).toBe(false);
    expect(runsProcess(shell({ type: 'ssh' }))).toBe(false);
    expect(runsProcess(shell({ executor: { type: 'docker' } }))).toBe(false);
  });
});

describe('trustOf', () => {
  it('makes every agent step untrusted, in each DAG', () => {
    expect(trustOf(action('agent.run'), false)).toEqual({
      untrusted: true,
      source: 'agent',
    });
    expect(trustOf(action('agent.run'), true).untrusted).toBe(true);
  });

  it('makes a step that runs a process untrusted by default in a DAG that has an agent step', () => {
    expect(trustOf(shell(), true)).toEqual({
      untrusted: true,
      source: 'default',
    });
    expect(trustOf(action('harness.run'), true)).toEqual({
      untrusted: true,
      source: 'default',
    });
  });

  it('is trusted with untrusted: false only, not with a list of secrets', () => {
    expect(trustOf(shell({ untrusted: false }), true)).toEqual({
      untrusted: false,
      source: 'opted-out',
    });
    expect(trustOf(shell({ secrets: ['A'] }), true)).toEqual({
      untrusted: true,
      source: 'default',
    });
    expect(
      trustOf(shell({ untrusted: false, secrets: ['A'] }), true).source
    ).toBe('opted-out');
  });

  it('keeps untrusted: true, also with a list of secrets', () => {
    expect(trustOf(shell({ untrusted: true }), true)).toEqual({
      untrusted: true,
      source: 'marked',
    });
    expect(
      trustOf(shell({ untrusted: true, secrets: ['A'] }), true).untrusted
    ).toBe(true);
  });

  it('does not change a DAG without an agent step', () => {
    expect(trustOf(shell(), false)).toEqual({
      untrusted: false,
      source: 'plain',
    });
    expect(trustOf(shell({ untrusted: true }), false).untrusted).toBe(true);
    expect(trustOf(shell({ untrusted: false }), false).untrusted).toBe(false);
  });

  it('does not apply the default to a step that runs no process', () => {
    expect(trustOf(action('http.request'), true).untrusted).toBe(false);
    expect(trustOf(action('chat.completion'), true).untrusted).toBe(false);
    expect(
      trustOf(shell({ container: { image: 'alpine' } }), true).untrusted
    ).toBe(false);
  });
});

describe('isAgentRunValue', () => {
  it('reads the action of the value, then the given action', () => {
    expect(isAgentRunValue({ action: 'agent.run' })).toBe(true);
    expect(isAgentRunValue({}, 'agent.run')).toBe(true);
    expect(isAgentRunValue({ action: 'chat.completion' })).toBe(false);
    expect(isAgentRunValue({ run: 'x' })).toBe(false);
  });
});

describe('writtenTrust', () => {
  it('gives a mark for the key untrusted only', () => {
    expect(writtenTrust(shell({ untrusted: true }))).toBe('marked');
    expect(writtenTrust(shell({ untrusted: false }))).toBe('opted-out');
    expect(writtenTrust(shell())).toBeUndefined();
    expect(writtenTrust(shell({ secrets: ['A'] }))).toBeUndefined();
  });

  it('gives no mark for an agent step that has no key, or false', () => {
    expect(writtenTrust(action('agent.run'))).toBeUndefined();
    expect(writtenTrust(action('agent.run', { untrusted: false }))).toBe(
      undefined
    );
    expect(writtenTrust(action('agent.run', { untrusted: true }))).toBe(
      'marked'
    );
  });
});

describe('secretsNeedOptOut', () => {
  it('is true for a process step that lists secrets and has no key untrusted, in an agent DAG', () => {
    expect(secretsNeedOptOut(shell({ secrets: ['A'] }), true)).toBe(true);
    expect(
      secretsNeedOptOut(action('harness.run', { secrets: ['A'] }), true)
    ).toBe(true);
  });

  it('is false with the key untrusted, without secrets, or without an agent step', () => {
    expect(
      secretsNeedOptOut(shell({ secrets: ['A'], untrusted: false }), true)
    ).toBe(false);
    expect(
      secretsNeedOptOut(shell({ secrets: ['A'], untrusted: true }), true)
    ).toBe(false);
    expect(secretsNeedOptOut(shell(), true)).toBe(false);
    expect(secretsNeedOptOut(shell({ secrets: [] }), true)).toBe(false);
    expect(secretsNeedOptOut(shell({ secrets: ['A'] }), false)).toBe(false);
  });

  it('is false for a step that runs no process', () => {
    expect(
      secretsNeedOptOut(action('http.request', { secrets: ['A'] }), true)
    ).toBe(false);
  });
});
