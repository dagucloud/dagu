// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { WebhookAuthMode } from '../../../../../api/v1/schema';
import {
  buildWebhookExamples,
  findActiveAllowedProfile,
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

  it('selects an active allowed profile for generated examples', () => {
    expect(
      findActiveAllowedProfile(['retired', 'staging'], ['prod', 'staging'])
    ).toBe('staging');
    expect(findActiveAllowedProfile(['retired'], ['prod'])).toBe('');
  });

  it('builds profile-bound HMAC request examples', () => {
    const examples = buildWebhookExamples({
      authMode: WebhookAuthMode.token_and_hmac,
      profileName: 'prod',
      webhookUrl: 'https://dagu.example/api/v1/webhooks/example',
    });

    expect(examples.curl).toContain('-H "Authorization: Bearer <YOUR_TOKEN>"');
    expect(examples.curl).toContain(
      '-H "X-Dagu-Signature: sha256=<SIGNATURE>"'
    );
    expect(examples.curl).toContain('-H "X-Dagu-Profile: prod"');
    expect(examples.hmacShell).toContain("profile='prod'");
    expect(examples.hmacShell).toContain(
      'signature_input=$(printf \'x-dagu-profile:%s\\n%s\' "$profile" "$body")'
    );
    expect(examples.hmacNode).toContain(
      "const signatureInput = 'x-dagu-profile:' + profile + '\\n' + body;"
    );
    expect(examples.hmacNode).toContain(
      "crypto.createHmac('sha256', process.env.DAGU_HMAC_SECRET)"
    );
    expect(examples.hmacNode).toContain(
      "'Authorization': 'Bearer <YOUR_TOKEN>',\n  };"
    );
  });

  it('builds examples for token-only and HMAC-only authentication', () => {
    const tokenOnly = buildWebhookExamples({
      authMode: WebhookAuthMode.token_only,
      profileName: '',
      webhookUrl: 'https://dagu.example/token',
    });
    expect(tokenOnly.curl).toContain('Authorization: Bearer <YOUR_TOKEN>');
    expect(tokenOnly.curl).not.toContain('X-Dagu-Signature');
    expect(tokenOnly.curl).not.toContain('X-Dagu-Profile');

    const hmacOnly = buildWebhookExamples({
      authMode: WebhookAuthMode.hmac_only,
      profileName: '',
      webhookUrl: 'https://dagu.example/hmac',
    });
    expect(hmacOnly.curl).not.toContain('Authorization');
    expect(hmacOnly.curl).toContain('X-Dagu-Signature');
    expect(hmacOnly.hmacShell).toContain('signature_input="$body"');
    expect(hmacOnly.hmacNode).toContain('const signatureInput = body;');
  });
});
