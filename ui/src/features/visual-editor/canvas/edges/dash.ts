// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Dash pattern of a dashed edge and of a dashed node border (a launcher that
 * is not set up): the same in every state. A dashed element stays dashed
 * when it is selected, focused or hovered.
 */
export const DASHED = '6 4';

/** Dash pattern of a dotted edge: the order of a chain. */
export const DOTTED = '2 4';

/**
 * Dash pattern of a conditional link: long dashes with a dot, so that it does
 * not read as an inferred link, which is dashed too.
 */
export const CONDITIONAL = '10 3 2 3';

/** Dash pattern of the thin link from a slot of a chat step to a sub-node. */
export const SUB_LINK_DASH = '5 4';

/**
 * Colour of the links, the slot connectors and the entry dots of the
 * sub-nodes of a chat step. The muted token follows the theme.
 */
export const SUB_LINK_COLOR = 'var(--muted-foreground)';
