// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { describe, expect, it } from 'vitest';
import {
  formatLogStepOutput,
  getExecutorCommand,
  getLogStepMessage,
} from '../executor-utils';

describe('getLogStepMessage', () => {
  it('returns the configured log message for log steps', () => {
    const step = {
      name: 'announce',
      executorConfig: {
        type: 'log',
        config: {
          message: 'Deploying ${ENVIRONMENT}',
        },
      },
    } as components['schemas']['Step'];

    expect(getLogStepMessage(step)).toBe('Deploying ${ENVIRONMENT}');
  });

  it('ignores non-log steps', () => {
    const step = {
      name: 'run',
      executorConfig: {
        type: 'command',
        config: {},
      },
    } as components['schemas']['Step'];

    expect(getLogStepMessage(step)).toBeNull();
  });
});

describe('formatLogStepOutput', () => {
  it('formats stdout content for inline log step display', () => {
    expect(formatLogStepOutput('Deploying production\n')).toBe(
      'Deploying production'
    );
    expect(formatLogStepOutput('one\r\ntwo\r\n')).toBe('one\ntwo');
  });

  it('removes ANSI escape codes from stdout content', () => {
    expect(
      formatLogStepOutput('\u001b[32mDeploying production\u001b[0m\n')
    ).toBe('Deploying production');
  });
});

describe('getExecutorCommand', () => {
  it('shows the start URL of a browser step', () => {
    const step = {
      name: 'checkout',
      executorConfig: {
        type: 'browser',
        config: { url: 'https://shop.example.com', do: [] },
      },
    } as components['schemas']['Step'];

    expect(getExecutorCommand(step)).toBe('browser: https://shop.example.com');
  });

  it('shows the first application a computer step launches', () => {
    const step = (config: Record<string, unknown>) =>
      ({
        name: 'post',
        executorConfig: { type: 'computer', config },
      }) as components['schemas']['Step'];

    expect(
      getExecutorCommand(
        step({
          do: [{ act: 'Log in' }, { launch: { command: 'saplogon.exe' } }],
        })
      )
    ).toBe('computer: saplogon.exe');
    expect(getExecutorCommand(step({ do: [{ launch: 'notepad.exe' }] }))).toBe(
      'computer: notepad.exe'
    );
    expect(getExecutorCommand(step({ do: [{ act: 'Log in' }] }))).toBe(
      'computer'
    );
  });

  it('shows the mailbox of a mail search or organize step', () => {
    const step = {
      name: 'find',
      executorConfig: {
        type: 'mail',
        config: { mailbox: 'support@example.com', unread: true },
      },
    } as components['schemas']['Step'];

    expect(getExecutorCommand(step)).toBe('mail: support@example.com');
  });
});
