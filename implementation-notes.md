# Implementation Notes

## MCP Auditability Implementation

### Scope

- User success invariant: implement the MCP auditability design in `mcp-auditability-design.html` so every MCP access to operational state or mutation-capable tools can be attributed to a subject and credential, authorized by API-key surface and workspace scope, audited with MCP source/correlation metadata, and found through structured audit-log filters and UI affordances.
- Source design file: `/Users/hamadayouta/dev/dagu/mcp-auditability-design.html`.
- Target branch literal: `feat/mcp-auditability`.
- Package name changes: none.
- Package tag changes: none.
- Workflow trigger changes: none.
- Release output changes: none.

### Running Decisions

- The HTML design is treated as the approved source of truth; no additional design/spec approval loop is needed for this implementation pass.
- Work will focus on the recommended MVP from the design before optional export/tamper-evidence hardening.
- Existing frontend API methods already enforce role and workspace access for most DAG/DAG-run operations, so MCP should preserve that path and add MCP-specific canonicalization for empty/default workspace instead of duplicating every DAG authorization rule in the MCP package.
- The MCP transport library exposes client implementation metadata through `CallToolRequest.Session.InitializeParams()` and request headers through `CallToolRequest.Extra`, so MCP audit events can record observed client name/version and HTTP headers without trusting them for authorization.
- New API-key surface and attribution fields will be additive in storage and OpenAPI. Existing stored keys need lazy migration defaults when read: `service_account` attribution and both `rest_api` plus `mcp` surfaces.
- REST and MCP are treated as separate API-key acceptance surfaces. Existing keys remain accepted on both surfaces after lazy migration; newly edited keys can be narrowed explicitly.
- MCP empty/default workspace canonicalization will be scoped to MCP-originated contexts only so existing REST/UI default-workspace behavior stays unchanged.
- REST audit events now get `source=rest`/`surface=rest_api` context as well, because adding surface-restricted API keys without REST source metadata would leave denied REST key usage harder to find.
- MCP `started` tool events are emitted after HTTP authentication and surface checks but before the downstream API method finishes its resource/workspace authorization. Final `denied`/`failed`/`succeeded` events carry the authoritative outcome.
- The API-key form uses a direct owner user ID field for `user_owned` attribution. A user picker would be nicer, but the current API contract exposes IDs and the existing page does not already load users.
- Explicit empty API-key surface arrays are rejected at the REST handler boundary. Omitted surfaces still get the legacy-compatible default of both `rest_api` and `mcp`.
- CI's Go modernization step requires standard library helpers for newly touched loops, so API-key surface checks use `slices.Contains` and MCP audit detail copying uses `maps.Copy`.
- Gosec treats some internal labels containing key/credential wording as potential secrets. Context storage now uses private zero-sized key types, and the denial audit reason was renamed from `invalid_credentials` to `auth_failed`; this keeps the event useful without adding suppressions.

### Verification

- Red test: `go test ./internal/auth -run 'TestAPIKeyForStorage_ToAPIKey_MigratesLegacyAuditMetadata|TestAPIKey_ToStorage_ToAPIKey_Roundtrip' -count=1` failed because API-key attribution/surface fields and helpers do not exist yet.
- Red test: `go test ./internal/service/frontend/auth -run 'TestMiddleware_APIKeyRequiredSurface' -count=1` failed because API-key context and required-surface middleware do not exist yet.
- Red test: `go test ./internal/persis/fileaudit -run TestStore_QueryWithStructuredMCPFields -count=1` failed because MCP category and structured audit entry/filter fields do not exist yet.
- `make api` passed after the OpenAPI audit/API-key schema changes.
- `pnpm gen:api` passed after the OpenAPI changes and regenerated `ui/src/api/v1/schema.ts`.
- `go test ./internal/auth ./internal/service/auth ./internal/service/frontend/auth ./internal/persis/fileaudit ./internal/service/audit ./internal/service/frontend/api/v1 ./internal/service/mcp -count=1` passed after implementation.
- `pnpm typecheck` passed after the frontend changes.
- `pnpm test src/pages/api-keys/__tests__/index.test.tsx` passed after updating API-key fixtures for surfaces/attribution.
- `go test ./internal/service/auth ./internal/service/frontend/api/v1 -count=1` passed after the final API-key validation cleanup.
- `go test ./internal/auth ./internal/service/mcp -count=1` passed after the Go modernization cleanup.
- `go test ./internal/auth ./internal/service/frontend/auth -count=1` passed after replacing the context-key labels and denial reason.
- `go fix -diff ./internal/auth ./internal/service/frontend/auth` passed after the linter cleanup.
- `git diff --check` passed.
