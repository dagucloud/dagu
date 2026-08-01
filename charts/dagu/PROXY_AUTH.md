# Proxy authentication

Proxy authentication lets an authenticating reverse proxy establish a
Dagu browser session. Dagu trusts the configured identity headers only on
`GET /proxy-login`; sending the same headers to APIs, webhooks, health
checks, static files, or other routes does not authenticate those requests.

This feature is safe only when every route to the Dagu UI service passes through
the authenticating proxy. A client that can reach the UI directly can choose the
identity headers and impersonate a user.

Dagu's built-in tunnel must remain disabled because it creates a separate
ingress path that does not pass through the authenticating proxy. Dagu rejects
this combination at startup.

## Prerequisites

- Builtin authentication is initialized with a local emergency administrator.
- The active license includes SSO.
- Exactly one UI replica is running. The Helm chart rejects other replica counts
  while proxy authentication is enabled and uses the `Recreate` upgrade
  strategy to prevent old and new UI pods from overlapping.
- The proxy supplies a stable, unique, non-reassigned user identifier. Changing
  the meaning of the user header can create another account or reuse an old one.
- Network policy, firewall rules, and service exposure prevent direct UI access.

Back up the user store before enabling the integration. With the chart defaults,
it is stored under `/data/users` on the shared volume.

## Dagu configuration

The following Helm values enable the integration:

```yaml
ui:
  replicas: 1

auth:
  mode: builtin
  proxy:
    enabled: true
    source: corporate-sso
    buttonLabel: Continue with Corporate SSO
    headers:
      user: X-Auth-Request-User
      groups: X-Auth-Request-Groups
    autoSignup: true
    roleMapping:
      defaultRole: viewer
      defaultWorkspaceAccess: none
      # Optional strict mode: reject identities that match no mapping.
      requireMapping: true
      skipOrgRoleSync: false
      groupMappings:
        dagu-admins: admin
      workspaceMappings:
        payments-team:
          - workspace: payments
            role: developer
```

The equivalent Dagu YAML uses snake-case nested keys under `auth.proxy`. The
optional `source` is an identity namespace and defaults to an empty string.
Changing it makes the same user-header value resolve to a distinct account; do
not change it during routine proxy upgrades. The groups header is required when
either mapping is configured. Group matching is case-sensitive.

For non-Helm deployments, the scalar settings are also available as
environment variables:

```text
DAGU_AUTH_PROXY_ENABLED
DAGU_AUTH_PROXY_SOURCE
DAGU_AUTH_PROXY_BUTTON_LABEL
DAGU_AUTH_PROXY_HEADERS_USER
DAGU_AUTH_PROXY_HEADERS_GROUPS
DAGU_AUTH_PROXY_AUTO_SIGNUP
DAGU_AUTH_PROXY_DEFAULT_ROLE
DAGU_AUTH_PROXY_DEFAULT_WORKSPACE_ACCESS
DAGU_AUTH_PROXY_REQUIRE_MAPPING
DAGU_AUTH_PROXY_SKIP_ORG_ROLE_SYNC
```

`DAGU_AUTH_PROXY_REQUIRE_MAPPING` defaults to `false`. Set it to `true` only
when identities without a matching global or workspace mapping must be denied.

`DAGU_AUTH_PROXY_GROUP_MAPPINGS` and
`DAGU_AUTH_PROXY_WORKSPACE_MAPPINGS` accept JSON objects matching the
YAML mapping values. Invalid JSON or trailing input prevents server startup.
Helm deployments must use the `auth.proxy` values above; the chart rejects
`DAGU_AUTH_PROXY_*` entries in `extraEnv` so its replica and rollout safeguards
cannot be bypassed.

When `skipOrgRoleSync` is false, each proxy login replaces the user's role and
workspace access with the current mapping result. API edits remain possible but
can be overwritten by the next login. When it is true, Dagu keeps the
existing role and workspace access, including manual changes. When
`requireMapping` is true, it still checks the current proxy groups on every
login, even when synchronization is skipped. New users always receive the
current mapping when their account is created.

With the default `requireMapping: false`, `defaultWorkspaceAccess: none` gives
an unmatched user no named-workspace grants, but the user's global `viewer`
role still permits viewing unlabelled DAGs and their logs. Only an explicit
`requireMapping: true` denies a user with no matching global or workspace
mapping. The chart requires at least one mapping when proxy authentication and
`requireMapping` are both enabled.

## Proxy contract

For requests sent to Dagu, the authenticating proxy must:

1. Remove identity headers supplied by the client.
2. Authenticate the request.
3. Set or overwrite the configured user and groups headers from authenticated
   session data.
4. Forward the request to a private Dagu UI service.

The user value is an opaque identity key. Dagu does not link it to an existing
username, email address, or OIDC account. The groups value is CSV; quoted fields
are supported.

Do not use `Authorization`, `Cookie`, `Host`, forwarding, routing, or
tenant-selection headers as identity headers. Do not log identity headers at the
proxy. Dagu redacts its configured identity headers from request logs.

## oauth2-proxy with ingress-nginx

oauth2-proxy's
[`--set-xauthrequest`](https://oauth2-proxy.github.io/oauth2-proxy/configuration/overview/)
option returns `X-Auth-Request-User` and `X-Auth-Request-Groups` from its auth
endpoint. Configure oauth2-proxy with at least:

```yaml
extraArgs:
  reverse-proxy: "true"
  trusted-proxy-ip: "<ingress-controller IP or CIDR>"
  set-xauthrequest: "true"
  oidc-groups-claim: groups
```

The exact provider, cookie, redirect URL, and client-secret settings are
deployment-specific. Scope the `trusted-proxy-ip` option to the ingress
controllers that can reach oauth2-proxy; reverse-proxy mode otherwise trusts
forwarded headers from every source for backward compatibility. Configure every
required ingress-controller CIDR using the argument syntax supported by the
deployed oauth2-proxy chart version. Expose oauth2-proxy's `/oauth2/start` and
callback routes through the same public hostname.

Following ingress-nginx's
[external authentication pattern](https://kubernetes.github.io/ingress-nginx/examples/auth/oauth-external-auth/),
protect the Dagu Ingress and copy only the two authenticated response headers:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: dagu
  namespace: dagu
  annotations:
    nginx.ingress.kubernetes.io/auth-url: "http://oauth2-proxy.auth.svc.cluster.local/oauth2/auth"
    nginx.ingress.kubernetes.io/auth-signin: "https://$host/oauth2/start?rd=$escaped_request_uri"
    nginx.ingress.kubernetes.io/auth-response-headers: "X-Auth-Request-User,X-Auth-Request-Groups"
spec:
  ingressClassName: nginx
  rules:
    - host: dagu.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: dagu-ui
                port:
                  number: 8080
```

Confirm against the deployed ingress-nginx version that auth response headers
overwrite client request headers even when the auth response omits a value.
Proxy configuration is not complete until the negative tests below pass.

## Network isolation

[`examples/proxy-network-policy.yaml`](./examples/proxy-network-policy.yaml)
is a starting point. In the ingress-nginx external-auth topology above,
ingress-nginx forwards the application request after consulting oauth2-proxy,
so the policy must allow the ingress controller rather than oauth2-proxy. Edit
its release, namespace, and ingress-controller labels before applying it, and
label the controller namespace with `dagu.sh/proxy-ingress=true`. The example
is deliberately not installed by the chart because ingress and monitoring
topology cannot be inferred safely.

```bash
kubectl label namespace ingress-nginx dagu.sh/proxy-ingress=true
kubectl apply -f charts/dagu/examples/proxy-network-policy.yaml
```

Keep the UI Service as `ClusterIP`. Do not add a public `LoadBalancer`,
`NodePort`, alternate Ingress, or service mesh route that bypasses authentication.
Do not use `kubectl port-forward` for normal access while this feature is enabled.
Confirm the policy with the cluster's CNI; host-network proxies may require
node-level firewall rules instead of pod selectors.

Verify both boundaries before rollout:

```bash
# A forged identity must be rejected by the proxy or overwritten with the
# identity of the authenticated browser session.
curl -i https://dagu.example.com/proxy-login \
  -H 'X-Auth-Request-User: forged-user'

# From an unrelated pod, direct UI access must time out or be denied.
kubectl -n default run dagu-direct-check --rm -i --restart=Never \
  --image=curlimages/curl -- \
  curl --connect-timeout 5 http://dagu-ui.dagu.svc.cluster.local:8080/api/v1/health
```

Also verify a valid browser login, group mapping, strict no-match denial, and a
disabled account before exposing the service broadly.

Chart upgrades briefly interrupt the UI while proxy authentication is enabled
because the old UI pod is stopped before its replacement starts. Standalone
mode already uses the same `Recreate` strategy for its single PVC-backed pod;
distributed scheduler, coordinator, and worker strategies are unchanged.

## Account lifecycle and recovery

- Proxy users do not have Dagu passwords. Password login, change, and
  administrator reset are unavailable for them.
- Disabling a user takes effect when the current JWT is checked on the next
  request. Mapping changes take effect at the next proxy login.
- Deleting a user allows the same identity to be provisioned again when
  `autoSignup` is true. Disable the user for durable denial.
- Keep the local emergency administrator and test it periodically through a
  network-restricted operator path. That path must strip identity headers and
  deny `/proxy-login`; it exists only for local password recovery.

To roll back, first remove `auth.proxy`, restore a safe access path for
the local administrator, and then deploy the older Dagu version. Preserve a user
store backup: older binaries may not retain proxy identity metadata if they
update these user records.
