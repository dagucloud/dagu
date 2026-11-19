// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Problem } from './endpoint';

type Translate = (text: string) => string;

/** The sentence of a problem of the form. */
export function problemText(ts: Translate, problem: Problem): string {
  switch (problem) {
    case 'name-pattern':
      return ts(
        'The name must start with a lowercase letter. Use lowercase letters, digits and -. The limit is 64 characters.'
      );
    case 'name-exists':
      return ts('An endpoint with this name exists in this scope.');
    case 'description-long':
      return ts('The description has more than 200 characters.');
    case 'url-required':
      return ts('Give the base URL.');
    case 'url-invalid':
      return ts('The URL is not valid.');
    case 'url-scheme':
      return ts(
        'The URL must start with https. Only a local model and Custom accept http.'
      );
    case 'url-parts':
      return ts(
        'The URL must not have user information, a query or a fragment.'
      );
    case 'allow-empty':
      return ts('Choose at least one name, or All.');
    case 'key-required':
      return ts('Give the key.');
    case 'key-control':
      return ts('The key has a control character. Paste it again.');
  }
}
