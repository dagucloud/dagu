// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Returns the absolute Streamable HTTP endpoint serving this instance's MCP
 * surface, mounted under the server's base path.
 *
 * The endpoint is derived from the browsing origin, which is the address the
 * server was actually reached on. The configured public URL is not published to
 * the frontend.
 */
export function buildMCPServerURL(basePath: string | undefined): string {
  const prefix = (basePath ?? '').replace(/\/+$/, '');
  return `${window.location.origin}${prefix}/mcp`;
}

/**
 * Returns instructions, addressed to an AI coding agent, for connecting its MCP
 * client to the Dagu server at mcpURL using apiKey.
 *
 * The instructions name no particular client: each one states its server
 * configuration differently, so the agent is left to translate the endpoint and
 * the credential into its own format.
 */
export function buildMCPSetupPrompt(mcpURL: string, apiKey: string): string {
  return `Set up the Dagu MCP server for this project.

- Transport: Streamable HTTP
- URL: ${mcpURL}
- Auth header: Authorization: Bearer ${apiKey}

Add it to my MCP client using that client's own configuration format. Keep the key out of version control: where the client supports environment variable expansion, store the key as DAGU_MCP_API_KEY and reference it from the config.

Then confirm the server is connected and that the tools dagu_read, dagu_change, and dagu_execute are listed.

Client setup reference: https://docs.dagu.sh/mcp/clients/
`;
}
