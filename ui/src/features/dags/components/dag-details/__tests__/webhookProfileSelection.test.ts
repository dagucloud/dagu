// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  buildHMACSignatureInputExamples,
  findUnavailableAllowedProfiles,
  updateAllowedProfiles,
} from '../webhookProfileSelection';

describe('webhook profile selection', () => {
  it('adds profiles once in sorted order and removes unchecked profiles', () => {
    expect(updateAllowedProfiles(['prod'], 'staging', true)).toEqual([
      'prod',
      'staging',
    ]);
    expect(updateAllowedProfiles(['staging', 'prod'], 'prod', true)).toEqual([
      'prod',
      'staging',
    ]);
    expect(updateAllowedProfiles(['prod', 'staging'], 'prod', false)).toEqual([
      'staging',
    ]);
  });

  it('finds configured profiles missing from the active profile list', () => {
    expect(
      findUnavailableAllowedProfiles(
        ['prod', 'retired', 'staging'],
        ['prod', 'staging']
      )
    ).toEqual(['retired']);
  });

  it('builds profile-bound and default signature input examples', () => {
    expect(buildHMACSignatureInputExamples('prod')).toEqual({
      shell: `profile='prod'
signature_input=$(printf 'x-dagu-profile:%s\\n%s' "$profile" "$body")`,
      node: `const profile = 'prod';
const signatureInput = 'x-dagu-profile:' + profile + '\\n' + body;`,
    });
    expect(buildHMACSignatureInputExamples('')).toEqual({
      shell: 'signature_input="$body"',
      node: 'const signatureInput = body;',
    });
  });
});
