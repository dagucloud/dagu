# Dagu Helm Chart

A Helm chart for deploying Dagu on Kubernetes.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- **A storage class that supports `ReadWriteMany` access mode** (required)

Dagu uses a shared filesystem for state persistence. You must have a storage class that supports `ReadWriteMany`:
- NFS (via nfs-client-provisioner)
- AWS EFS
- CephFS
- Azure Files (Premium)
- GlusterFS

## Install

Official Helm repository URL:

```text
https://dagucloud.github.io/dagu
```

Add the repository and install the chart:

```bash
helm repo add dagu https://dagucloud.github.io/dagu
helm repo update
helm install dagu dagu/dagu --set persistence.storageClass=<your-rwx-storage-class>
```

Render manifests without installing:

```bash
helm template dagu dagu/dagu --set persistence.storageClass=<your-rwx-storage-class>
```

Upgrade an existing release:

```bash
helm repo update
helm upgrade dagu dagu/dagu --set persistence.storageClass=<your-rwx-storage-class>
```

From a source checkout, the local chart path remains available:

```bash
helm install dagu ./charts/dagu --set persistence.storageClass=<your-rwx-storage-class>
```

Replace `<your-rwx-storage-class>` with a StorageClass in your cluster that supports `ReadWriteMany`. If your cluster default storage class already supports `ReadWriteMany`, you can omit the flag.

## Versions

`charts/dagu/Chart.yaml` defines the chart `version`, which is the version published to the Helm repository.

The deployed container image comes from `values.yaml -> image.repository` and `values.yaml -> image.tag`. With the current defaults, the chart deploys `ghcr.io/dagucloud/dagu:latest` and always checks the registry when starting a pod. When pinning `image.tag`, also set `image.pullPolicy: IfNotPresent` if pods should reuse the pinned image without contacting the registry on every start.

For chart publication and repository maintenance, see [`RELEASING.md`](./RELEASING.md).

## Architecture

The chart deploys four components:

- **Coordinator**: gRPC server for distributed task execution (port 50055, HTTP health on 8091 by default)
- **Scheduler**: Manages DAG execution schedules (port 8090 for health)
- **Worker**: Executes DAG steps (configurable pools with independent replicas, HTTP health on 8092 by default)
- **UI**: Web interface for managing DAGs (port 8080)

The UI, scheduler, and coordinator share a PersistentVolumeClaim with `ReadWriteMany` access mode.
Workers connect to the coordinator service through `--worker.coordinators` and use local pod storage for their own runtime files.

## Configuration

### Persistence Values

The chart always renders a PVC. `persistence.enabled` must remain `true`.

If `persistence.storageClass` is the empty string, the rendered PVC omits `storageClassName` and Kubernetes uses the cluster default behavior. If your cluster does not provide a suitable default RWX storage class, set `persistence.storageClass` explicitly:

```yaml
persistence:
  storageClass: "<your-rwx-storage-class>"
```

The chart sets `podSecurityContext.fsGroup: 1000` by default so the shared `/data` volume stays writable after the image entrypoint drops from root to the default `PUID`/`PGID` user. If you override `PUID` or `PGID`, set `podSecurityContext.fsGroup` to the same group. Clusters that support `fsGroupChangePolicy` can add it under `podSecurityContext`; it is not set by default so the chart remains compatible with Kubernetes 1.19.

### Local Testing (Kind, Docker Desktop)

For local single-node clusters that don't support RWX:

```bash
helm install dagu dagu/dagu \
  --set persistence.accessMode=ReadWriteOnce \
  --set persistence.skipValidation=true \
  --set workerPools.general.replicas=1
```

From a source checkout, the equivalent command is:

```bash
helm install dagu ./charts/dagu \
  --set persistence.accessMode=ReadWriteOnce \
  --set persistence.skipValidation=true \
  --set workerPools.general.replicas=1
```

### Worker Pools

Workers are organized into pools. Each pool creates a separate Kubernetes Deployment with its own replicas, labels, resources, and scheduling constraints. DAGs select workers via `workerSelector` labels that match a pool's labels.

```yaml
workerPools:
  general:
    replicas: 2
    labels: {}
    dataVolume:
      sizeLimit: "2Gi"
    resources:
      requests:
        memory: "128Mi"
        cpu: "100m"
        ephemeral-storage: "1Gi"
      limits:
        memory: "256Mi"
        cpu: "200m"
        ephemeral-storage: "2Gi"
    nodeSelector: {}
    tolerations: []
    affinity: {}

  gpu:
    replicas: 1
    labels:
      gpu: "true"
    resources:
      requests:
        memory: "512Mi"
        cpu: "500m"
        nvidia.com/gpu: "1"
      limits:
        memory: "1Gi"
        cpu: "1000m"
        nvidia.com/gpu: "1"
    nodeSelector:
      nvidia.com/gpu.present: "true"
    tolerations:
      - key: nvidia.com/gpu
        operator: Exists
        effect: NoSchedule
    affinity: {}
```

A pool with `labels: {}` (like `general` above) matches any DAG that has no `workerSelector`. To route a DAG to a specific pool, set `workerSelector` in the DAG definition to match the pool's labels:

```yaml
# In your DAG file
workerSelector:
  gpu: "true"
```

### Cross-Origin Browser Access

Cross-origin browser access is disabled by default. This does not affect the bundled Dagu UI because it uses the same origin as the API. To allow a separate browser application to call Dagu, list its exact origins:

```yaml
config:
  corsAllowedOrigins:
    - https://app.example.com
    - https://admin.example.com
```

Set `corsAllowedOrigins: ["*"]` only when any website should be allowed to call the API. Wildcard CORS does not allow credentials and is especially risky with `auth.mode: none`.

### Environment Passthrough

Dagu filters host/container environment variables before exposing them to workflow steps. To allow additional runtime env vars such as proxy or certificate settings, configure both:

- `extraEnv` to place the source env vars into the Dagu pods
- `config.envPassthrough` or `config.envPassthroughPrefixes` to forward selected env vars into step execution

Example:

```yaml
config:
  envPassthrough:
    - SSL_CERT_FILE
  envPassthroughPrefixes:
    - HTTP_
    - HTTPS_
    - NO_

extraEnv:
  - name: HTTP_PROXY
    value: http://proxy.example.com:8080
  - name: HTTPS_PROXY
    value: http://proxy.example.com:8080
  - name: NO_PROXY
    value: 127.0.0.1,localhost,.svc
  - name: SSL_CERT_FILE
    value: /etc/ssl/certs/custom-ca.pem
```

`config.envPassthrough` matches exact env var names. `config.envPassthroughPrefixes` matches by prefix. Existing built-in defaults such as Kubernetes discovery env vars still apply automatically.

### License

Store the Dagu license key in a Kubernetes Secret in the same namespace as the Helm release:

```bash
kubectl create secret generic dagu-license \
  --from-literal=license-key='<your-license-key>'
```

Reference that Secret from the chart:

```yaml
license:
  existingSecret: dagu-license
  secretKey: license-key
```

`license.secretKey` defaults to `license-key`, so an install can also set only the Secret name:

```bash
helm install dagu dagu/dagu \
  --set persistence.storageClass=<your-rwx-storage-class> \
  --set license.existingSecret=dagu-license
```

The chart exposes the selected Secret value to the UI container as `DAGU_LICENSE_KEY`. It does not copy the license key into the ConfigMap or expose it to the scheduler, coordinator, or workers.

Secret-backed environment variables are read when the UI pod starts. After rotating the license Secret—or the OIDC client Secret described below—restart the UI Deployment so it reads the new value:

```bash
kubectl rollout restart deployment \
  --selector app.kubernetes.io/instance=dagu,app.kubernetes.io/component=ui
```

The example uses the release name `dagu`; replace that instance-label value when the release has another name. The selector also works with `nameOverride` and `fullnameOverride`.

### Authentication

By default, the chart uses builtin authentication. On first run, visit the UI to create an admin account via the setup page.

```yaml
auth:
  mode: "builtin"  # Options: "none", "basic", "builtin" (default)
  builtin:
    token:
      secret: ""               # optional: auto-generated at {data_dir}/auth/token_secret
      ttl: "24h"
```

To disable authentication:
```bash
helm install dagu dagu/dagu \
  --set persistence.storageClass=<your-rwx-storage-class> \
  --set auth.mode=none
```

#### OIDC

OIDC runs as part of builtin authentication and requires an active license. The license may come from `license.existingSecret`, supported license variables in `extraEnv`, an offline license file, or activation data already persisted on the shared volume. Create the first builtin administrator through the setup page before testing OIDC login.

Store the provider's client secret in the release namespace:

```bash
kubectl create secret generic dagu-oidc \
  --from-literal=client-secret='<your-oidc-client-secret>'
```

Configure the provider and authorization policy through Helm values:

```yaml
auth:
  mode: builtin
  oidc:
    enabled: true
    clientId: dagu
    clientUrl: https://dagu.example.com
    issuer: https://idp.example.com
    scopes: [openid, profile, email]
    whitelist: []
    autoSignup: true
    allowedDomains:
      - example.com
    buttonLabel: Login with SSO
    clientSecret:
      existingSecret: dagu-oidc
      secretKey: client-secret
    roleMapping:
      defaultRole: viewer
      groupsClaim: groups
      groupMappings:
        dagu-org-admins: admin
      workspaceMappings:
        payments-team:
          - workspace: payments
            role: developer
        sre-team:
          - workspace: infra
            role: operator
      defaultWorkspaceAccess: none
      roleAttributePath: ""
      roleAttributeStrict: false
      skipOrgRoleSync: false
```

The chart renders the provider settings, access filters, and complete role mapping into `dagu.yaml` in the ConfigMap. Only the client secret stays in a Kubernetes Secret and is exposed to the UI container as `DAGU_AUTH_OIDC_CLIENT_SECRET`.

Global `groupMappings` take precedence over `workspaceMappings`. Workspace roles may be `manager`, `developer`, `operator`, or `viewer`; `admin` is available only for global mappings. Set `defaultWorkspaceAccess` to `none` to deny unmatched users access to named workspaces, or `all` to apply `defaultRole` across all workspaces.

Proxy authentication is available for deployments where an
authenticating reverse proxy is the only network path to the UI. It requires
builtin authentication, `auth.proxy.enabled: true`, and one UI replica. See
[`PROXY_AUTH.md`](./PROXY_AUTH.md) for the trust contract,
oauth2-proxy and ingress-nginx configuration, NetworkPolicy example, validation,
and recovery guidance. By default, unmatched proxy users may log in and receive
no named-workspace grants, while retaining the global viewer permission for
unlabelled DAGs and their logs. Set `auth.proxy.roleMapping.requireMapping: true`
to require a matching global or workspace mapping. Access is recalculated on
every login for existing proxy users unless
`auth.proxy.roleMapping.skipOrgRoleSync` is enabled.

### Component Resources

```yaml
image:
  repository: ghcr.io/dagucloud/dagu
  tag: latest
  pullPolicy: Always

coordinator:
  replicas: 1
  resources:
    requests:
      memory: "128Mi"
      cpu: "100m"

scheduler:
  replicas: 1
  resources:
    requests:
      memory: "256Mi"
      cpu: "250m"

workerPools:
  general:
    replicas: 2
    labels: {}
    dataVolume:
      sizeLimit: "2Gi"
    resources:
      requests:
        memory: "128Mi"
        cpu: "100m"
        ephemeral-storage: "1Gi"
      limits:
        memory: "256Mi"
        cpu: "200m"
        ephemeral-storage: "2Gi"

ui:
  replicas: 1
  resources:
    requests:
      memory: "256Mi"
      cpu: "250m"
```

To force a different tag:

```yaml
image:
  tag: 2.2.4
```

## Accessing the UI

For a regular internal deployment, enable the Ingress and point internal DNS at your ingress controller:

```yaml
ingress:
  enabled: true
  className: nginx
  host: dagu.internal.example.com
  path: /
  pathType: Prefix
  tls:
    enabled: true
    secretName: dagu-internal-tls

config:
  publicUrl: https://dagu.internal.example.com
```

The bundled UI and API use the same host, so this setup does not require `config.corsAllowedOrigins`. If OIDC is enabled, use the same URL for `auth.oidc.clientUrl` and register its `/oidc-callback` URL with the identity provider.

Ingress is disabled by default because the chart cannot know the cluster's ingress class, DNS name, or TLS Secret. The UI Service remains a `ClusterIP`. For clusters without an ingress controller, set `ui.service.type` to `LoadBalancer` or `NodePort`; `ui.service.annotations` supports provider-specific internal load-balancer settings.

When `ingress.tls.enabled` is true, set `ingress.tls.secretName` to a TLS Secret or leave it empty when the ingress controller provides the default certificate.

For temporary access with the defaults:

```bash
kubectl port-forward svc/dagu-ui 8080:8080

# Visit http://localhost:8080
```

## Current Constraints

This chart reflects Dagu's current architecture:

- **Shared filesystem required for server-side state**: UI, scheduler, and coordinator share the RWX volume
- **File-based state**: State is stored in files on the shared volume
- **No database**: Dagu does not use a database for state management

## Uninstall

```bash
helm uninstall dagu
```

**Warning**: This will delete the PersistentVolumeClaim and all data. Backup your DAGs and logs first!
