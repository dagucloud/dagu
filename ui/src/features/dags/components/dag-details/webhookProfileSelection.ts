// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

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

export function buildHMACSignatureInputExamples(profileName: string): {
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
