// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Bot } from 'lucide-react';
import { describe, expect, it } from 'vitest';
import { modelIcon, providerIcon } from '../providerLogo';

describe('providerIcon', () => {
  it('gives the Anthropic logo to claude and anthropic, in any case', () => {
    const logo = providerIcon('claude');
    expect(logo).not.toBe(Bot);
    expect(providerIcon('Anthropic')).toBe(logo);
    expect(providerIcon(' CLAUDE ')).toBe(logo);
  });

  it('keeps the bot for another provider, a reference or no provider', () => {
    expect(providerIcon('openai')).toBe(Bot);
    expect(providerIcon('${PROVIDER}')).toBe(Bot);
    expect(providerIcon('')).toBe(Bot);
    expect(providerIcon(undefined)).toBe(Bot);
  });
});

// A step that names an endpoint takes the logo of its preset, so that an agent
// step and a chat step on one endpoint look alike.
describe('modelIcon', () => {
  const logo = providerIcon('anthropic');

  it('takes the logo of the preset of the endpoint', () => {
    expect(modelIcon('anthropic', undefined)).toBe(logo);
    expect(modelIcon('anthropic', 'codex')).toBe(logo);
  });

  it('shows the bot for an endpoint whose preset has no logo', () => {
    expect(modelIcon('openrouter', 'claude')).toBe(Bot);
  });

  it('falls back to the provider when the step names no known endpoint', () => {
    expect(modelIcon(undefined, 'claude')).toBe(logo);
    expect(modelIcon(undefined, undefined)).toBe(Bot);
  });
});
