// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  WebhookAuthMode as WebhookAuthModeValue,
} from '../../../../api/v1/schema';

type WebhookAuthMode = components['schemas']['WebhookAuthMode'];

interface BuildWebhookExamplesInput {
  authMode: WebhookAuthMode;
  profileName: string;
  webhookUrl: string;
}

interface WebhookExamples {
  curl: string;
  hmacShell: string;
  hmacNode: string;
}

export function updateAllowedProfiles(
  current: string[],
  profileName: string,
  checked: boolean
): string[] {
  if (!checked) {
    return current.filter((name) => name !== profileName);
  }
  return Array.from(new Set([...current, profileName])).sort();
}

export function findUnavailableAllowedProfiles(
  allowedProfiles: string[],
  availableProfileNames: string[]
): string[] {
  const available = new Set(availableProfileNames);
  return allowedProfiles.filter((name) => !available.has(name));
}

function buildHMACSignatureInputExamples(profileName: string): {
  shell: string;
  node: string;
} {
  if (!profileName) {
    return {
      shell: 'signature_input="$body"',
      node: 'const signatureInput = body;',
    };
  }
  return {
    shell: `profile='${profileName}'
signature_input=$(printf 'x-dagu-profile:%s\\n%s' "$profile" "$body")`,
    node: `const profile = '${profileName}';
const signatureInput = 'x-dagu-profile:' + profile + '\\n' + body;`,
  };
}

export function buildWebhookExamples({
  authMode,
  profileName,
  webhookUrl,
}: BuildWebhookExamplesInput): WebhookExamples {
  const curlHeaders: string[] = [];
  if (authMode !== WebhookAuthModeValue.hmac_only) {
    curlHeaders.push('Authorization: Bearer <YOUR_TOKEN>');
  }
  if (authMode !== WebhookAuthModeValue.token_only) {
    curlHeaders.push('X-Dagu-Signature: sha256=<SIGNATURE>');
  }
  if (profileName) {
    curlHeaders.push(`X-Dagu-Profile: ${profileName}`);
  }
  curlHeaders.push('Content-Type: application/json');

  const curl = [
    `curl -X POST "${webhookUrl}" \\`,
    ...curlHeaders.map((header) => `  -H "${header}" \\`),
    `  -d '{"dagRunId": "my-unique-id", "payload": {"key": "value"}}'`,
  ].join('\n');

  const signatureInput = buildHMACSignatureInputExamples(profileName);
  const tokenShellHeader =
    authMode === WebhookAuthModeValue.token_and_hmac
      ? '-H "Authorization: Bearer <YOUR_TOKEN>" \\\n  '
      : '';
  const profileShellHeader = profileName
    ? '-H "X-Dagu-Profile: $profile" \\\n  '
    : '';
  const hmacShell = `body='{"dagRunId":"my-unique-id","payload":{"key":"value"}}'
${signatureInput.shell}
sig=$(printf '%s' "$signature_input" | openssl dgst -sha256 -hmac "$DAGU_HMAC_SECRET" -hex | sed 's/^.* //')

curl -X POST "${webhookUrl}" \\
  ${tokenShellHeader}-H "X-Dagu-Signature: sha256=$sig" \\
  ${profileShellHeader}-H "Content-Type: application/json" \\
  -d "$body"`;

  const tokenNodeHeader =
    authMode === WebhookAuthModeValue.token_and_hmac
      ? "'Authorization': 'Bearer <YOUR_TOKEN>',\n  "
      : '';
  const profileNodeHeader = profileName ? "'X-Dagu-Profile': profile,\n  " : '';
  const hmacNode = `import crypto from 'node:crypto';

const body = JSON.stringify({
  dagRunId: 'my-unique-id',
  payload: { key: 'value' },
});

${signatureInput.node}

const signature =
  'sha256=' +
  crypto.createHmac('sha256', process.env.DAGU_HMAC_SECRET!)
    .update(signatureInput, 'utf8')
    .digest('hex');

const headers = {
  'Content-Type': 'application/json',
  'X-Dagu-Signature': signature,
  ${profileNodeHeader}${tokenNodeHeader}}

await fetch('${webhookUrl}', {
  method: 'POST',
  headers,
  body,
});`;

  return { curl, hmacShell, hmacNode };
}
