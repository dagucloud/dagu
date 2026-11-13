// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Bot, createLucideIcon, type LucideIcon } from 'lucide-react';

/**
 * The Anthropic mark. The path is the icon "Anthropic" of Simple Icons
 * (CC0 1.0), drawn filled, in the colour of the text, in the 24 px box of the
 * other icons.
 */
const Anthropic = createLucideIcon('Anthropic', [
  [
    'path',
    {
      d: 'M17.3041 3.541h-3.6718l6.696 16.918H24Zm-10.6082 0L0 20.459h3.7442l1.3693-3.5527h7.0052l1.3693 3.5528h3.7442L10.5363 3.5409Zm-.3712 10.2232 2.2914-5.9456 2.2914 5.9456Z',
      fill: 'currentColor',
      stroke: 'none',
      key: 'anthropic',
    },
  ],
]);

/** The providers that have a logo, by name in lower case. */
const LOGOS: Record<string, LucideIcon> = {
  anthropic: Anthropic,
  claude: Anthropic,
};

/**
 * The icon of the Model node for a provider: its logo when it has one, else the
 * bot. A `${...}` reference, an empty name or an unknown provider gives the bot.
 */
export function providerIcon(provider: string | undefined): LucideIcon {
  return LOGOS[provider?.trim().toLowerCase() ?? ''] ?? Bot;
}

/**
 * The icon of the Model node of a step. An endpoint that the AI page lists
 * gives its preset, for an agent step and a chat step alike: the logo of the
 * preset, or the bot when it has none. A step with no known endpoint takes the
 * logo of its provider or CLI.
 */
export function modelIcon(
  preset: string | undefined,
  provider: string | undefined
): LucideIcon {
  return providerIcon(preset ?? provider);
}
