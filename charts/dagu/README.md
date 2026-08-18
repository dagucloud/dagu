# Dagu Helm Chart

A Helm chart for deploying Dagu on Kubernetes.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- A default StorageClass or an existing PersistentVolumeClaim

The default standalone deployment uses ordinary `ReadWriteOnce` storage. The optional distributed deployment requires a `ReadWriteMany` StorageClass such as NFS, EFS, CephFS, or Azure Files.

## Install

Official Helm repository URL:

```text
https://dagucloud.github.io/dagu
```

Add the repository and install the chart:

```bash
helm repo add dagu https://dagucloud.github.io/dagu
helm repo update
helm upgrade --install dagu dagu/dagu \
  --namespace dagu \
  --create-namespace \
  --wait
```

The default installation runs Dagu's web UI, scheduler, and workflow executor in one pod. Kubernetes provisions a `ReadWriteOnce` PVC through the cluster's default StorageClass.

Open the UI:

```bash
kubectl --namespace dagu port-forward service/dagu-ui 8080:8080
```

Then visit <http://localhost:8080>.

After the pod is ready, verify in-cluster connectivity:

```bash
helm test dagu --namespace dagu
```

Render manifests without installing:

```bash
helm template dagu dagu/dagu --namespace dagu
```

From a source checkout, the local chart path remains available:

```bash
helm upgrade --install dagu ./charts/dagu \
  --namespace dagu \
  --create-namespace \
  --wait
```

## Versions

`charts/dagu/Chart.yaml` defines the chart `version`, which is the version published to the Helm repository.

The chart pins the default Dagu image to `Chart.appVersion`. Leave `image.tag` empty to use that version, or set it explicitly when testing another application release. The default pull policy is `IfNotPresent`.

For chart publication and repository maintenance, see [`RELEASING.md`](https://github.com/dagucloud/dagu/blob/main/charts/dagu/RELEASING.md).

## Deployment Modes

### Standalone (Default)

Standalone mode runs `dagu start-all` in one Deployment. The pod provides the UI, API, scheduler, queues, and local workflow execution. It uses a regular `ReadWriteOnce` PVC and a `Recreate` deployment strategy so upgrades do not contend for the same volume.

The default per-run work root, `/data/dag-run-work`, uses that PVC; no additional volume is required. Backups that select individual `/data` subdirectories must include both `dag-runs` and `dag-run-work`.

This is the recommended mode for evaluating Dagu and for small or medium installations that do not need independent worker pools.

### Distributed

Distributed mode creates separate UI, scheduler, coordinator, and worker-pool Deployments. The server-side components share a `ReadWriteMany` PVC; workers use local ephemeral storage and communicate with the coordinator Service.

```yaml
deploymentMode: distributed

persistence:
  accessMode: ReadWriteMany
  storageClass: "<your-rwx-storage-class>"
```

The coordinator defaults to one replica. Set two replicas to keep the coordinator endpoint available when a pod is replaced:

```yaml
coordinator:
  replicas: 2
```

Coordinator replicas must share the same `ReadWriteMany` volume. The durable lease on that volume authorizes owner-bound worker traffic, so another replica can continue an active run even when its process ID or network endpoint differs from the original owner. No cluster ID setting is required: the shared data root is the ownership boundary. The chart configures the shared volume and stable coordinator Service automatically.

Upgrades replace one coordinator at a time without creating surge replicas. With two or more replicas, at least one coordinator remains available throughout the rollout; with one replica, the replacement causes a brief coordinator outage. During the first upgrade from a version that fenced traffic by process ID, an older pod may temporarily reject traffic for work accepted by a newer pod. Workers retry final status, log, and artifact delivery through the available replicas during that mixed-version window.

Worker pools use their existing ephemeral `/data` volumes for per-run work, so the separate work root requires no additional volume. Custom deployments that instead share a durable work root across execution processes must drain those processes before upgrading from the nested work-directory layout. Old and new versions must not execute the same run concurrently during that transition.

Install with those values:

```bash
helm upgrade --install dagu dagu/dagu \
  --namespace dagu \
  --create-namespace \
  --values distributed-values.yaml \
  --wait
```

## Configuration

Leave `nameOverride` and `fullnameOverride` empty to use release-derived resource names. Set them only when cluster naming conventions require a different prefix.

### Persistence

The chart creates a PVC using the cluster's default StorageClass when `persistence.storageClass` is empty. Set a StorageClass explicitly when the cluster does not have a suitable default:

```yaml
persistence:
  storageClass: standard
  size: 20Gi
```

The PVC is retained when the Helm release is uninstalled by default. Set `persistence.retain: false` if Helm should delete it with the release.

To reuse a PVC that is managed outside this release, set its name. The chart will mount it without creating or deleting it:

```yaml
persistence:
  existingClaim: dagu-data
```

`persistence.enabled` must remain `true`. Distributed mode requires `persistence.accessMode: ReadWriteMany`, including when an existing claim is used; standalone mode accepts either supported access mode. The declared access mode must match the existing PVC. Helm cannot inspect the PVC while rendering the chart.

The chart sets `podSecurityContext.fsGroup: 1000` by default so `/data` remains writable after the image entrypoint switches to the default Dagu user. Match `fsGroup` to custom `PUID` or `PGID` values.

### Service Account

The chart creates a release-scoped Kubernetes ServiceAccount and assigns it to every Dagu pod. Add provider-specific annotations when Dagu should use a workload identity:

```yaml
serviceAccount:
  create: true
  name: ""
  annotations:
    example.com/workload-identity: dagu
```

To use an account managed outside the release, set its name without creating it:

```yaml
serviceAccount:
  create: false
  name: dagu-runtime
```

When `create: false` is combined with an empty name, pods use the namespace's `default` ServiceAccount. The chart does not grant Kubernetes API permissions. Bind any required Roles or ClusterRoles to the selected account separately, including access used by Dagu's Kubernetes Secret provider.

### Additional Volumes

`extraVolumes` and `extraVolumeMounts` add file-backed configuration to every Dagu container. For example, mount a custom CA bundle and make it available to workflow processes:

```yaml
extraVolumes:
  - name: ca-bundle
    secret:
      secretName: dagu-ca-bundle

extraVolumeMounts:
  - name: ca-bundle
    mountPath: /etc/ssl/certs/dagu-ca-bundle.pem
    subPath: ca-bundle.pem
    readOnly: true

extraEnv:
  - name: SSL_CERT_FILE
    value: /etc/ssl/certs/dagu-ca-bundle.pem

config:
  envPassthrough:
    - SSL_CERT_FILE
```

Additional volume names must not conflict with the chart-managed `data` and `config` volumes. The referenced Secret, ConfigMap, PVC, or CSI resource must be available in the release namespace where applicable.

### Worker Pools

Worker pools are used only in distributed mode. Each pool creates a Kubernetes Deployment with independent replicas, labels, resources, and scheduling constraints. DAGs select workers through `workerSelector` labels that match a pool's labels.

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

Set `config.corsAllowedOrigins: ["*"]` only when any website should be allowed to call the API. Wildcard CORS does not allow credentials and is especially risky with `auth.mode: none`.

### IP Access Allowlist

Restrict every UI and API HTTP route to specific client addresses or networks:

```yaml
config:
  ipAccess:
    allowedIPs:
      - 203.0.113.10
      - 10.0.0.0/8
    trustedProxies:
      - 10.42.0.0/16
```

`allowedIPs` accepts IPv4 and IPv6 addresses or CIDR ranges. An empty list
disables filtering. When Dagu runs behind an ingress or reverse proxy, list only
the proxy network in `trustedProxies`; forwarding headers from all other peers
are ignored. The proxy must remove client-supplied forwarding headers and set or
append the verified client address.

The allowlist covers health and metrics routes. When it is enabled, the chart
uses TCP liveness, readiness, and connection checks so Kubernetes does not need
an HTTP allowlist exemption for node or test-pod addresses.

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
kubectl --namespace dagu create secret generic dagu-license \
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
helm upgrade --install dagu dagu/dagu \
  --namespace dagu \
  --set license.existingSecret=dagu-license
```

The chart exposes the selected Secret value to the server container as `DAGU_LICENSE_KEY`. Separate scheduler, coordinator, and worker pods in distributed mode do not receive it.

Secret-backed environment variables are read when the UI pod starts. After rotating the license Secret—or the OIDC client Secret described below—restart the UI Deployment so it reads the new value:

```bash
kubectl --namespace dagu rollout restart deployment \
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

#### Basic

Basic authentication uses a username and password stored in an existing Secret:

```bash
kubectl --namespace dagu create secret generic dagu-basic-auth \
  --from-literal=username=admin \
  --from-literal=password='<strong-password>'
```

```yaml
auth:
  mode: basic
  basic:
    existingSecret: dagu-basic-auth
    usernameKey: username
    passwordKey: password
```

The credentials are exposed only to the server container and are not written to the chart-managed ConfigMap. Restart the UI Deployment after rotating the Secret so the pod reads the new values.

#### Disable Authentication

To disable authentication:

```bash
helm upgrade --install dagu dagu/dagu \
  --namespace dagu \
  --set auth.mode=none
```

#### OIDC

OIDC runs as part of builtin authentication and requires an active license. The license may come from `license.existingSecret`, supported license variables in `extraEnv`, an offline license file, or activation data already persisted on the shared volume. Create the first builtin administrator through the setup page before testing OIDC login.

Store the provider's client secret in the release namespace:

```bash
kubectl --namespace dagu create secret generic dagu-oidc \
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

The chart renders the provider settings, access filters, and complete role mapping into `dagu.yaml` in the ConfigMap. Only the client secret stays in a Kubernetes Secret and is exposed to the server container as `DAGU_AUTH_OIDC_CLIENT_SECRET`.

Global `groupMappings` take precedence over `workspaceMappings`. Workspace roles may be `manager`, `developer`, `operator`, or `viewer`; `admin` is available only for global mappings. Set `defaultWorkspaceAccess` to `none` to deny unmatched users access to named workspaces, or `all` to apply `defaultRole` across all workspaces. The chart retains Dagu's runtime-compatible `all` default, which gives every unmatched validated OIDC user viewer access to all named workspaces; set it explicitly to `none` when workspaces must be isolated by default.

#### Proxy

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

In standalone mode, `ui.resources` controls the combined Dagu pod. Scheduler, coordinator, and worker-pool resources apply only in distributed mode.

The default server pod has resource requests but no limits because locally executed workflow processes share its container. Set limits when the workload envelope is known.

```yaml
image:
  repository: ghcr.io/dagucloud/dagu
  tag: "" # Uses Chart.appVersion
  pullPolicy: IfNotPresent

ui:
  replicas: 1
  resources:
    requests:
      memory: "256Mi"
      cpu: "250m"

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
```

Use `nodeSelector`, `tolerations`, and `affinity` for scheduling all Dagu pods. Non-empty scheduling values on a worker pool override the corresponding global value for that pool. `imagePullSecrets` and `podAnnotations` are also applied to every Dagu pod.

`worker.maxActiveRuns` controls the maximum concurrent runs handled by each distributed worker process.

To force a different tag:

```yaml
image:
  tag: "<dagu-version>"
```

## Accessing the UI

For a regular internal deployment, save these settings as `dagu-values.yaml` and point internal DNS at your ingress controller:

```yaml
ingress:
  enabled: true
  className: your-ingress-class
  host: dagu.internal.example.com
  tls:
    enabled: true
    secretName: dagu-internal-tls

config:
  publicUrl: https://dagu.internal.example.com
```

Apply the values:

```bash
helm upgrade --install dagu dagu/dagu \
  --namespace dagu \
  --create-namespace \
  --values dagu-values.yaml \
  --wait
```

Replace `your-ingress-class` with a controller installed in the cluster. The bundled UI and API use the same host, so this setup does not require `config.corsAllowedOrigins`. If OIDC is enabled, use the same URL for `auth.oidc.clientUrl` and register its `/oidc-callback` URL with the identity provider.

Proxy-header authentication cannot use the chart-managed Ingress because the chart cannot verify provider-specific external-auth behavior. Keep `ingress.enabled: false` and follow [`PROXY_AUTH.md`](./PROXY_AUTH.md) to create an authenticated Ingress that cannot bypass the proxy.

Ingress is disabled by default because the chart cannot know the cluster's ingress class, DNS name, or TLS Secret. The UI Service remains a `ClusterIP`. For clusters without an ingress controller, set `ui.service.type` to `LoadBalancer` or `NodePort`; `ui.service.annotations` supports provider-specific internal load-balancer settings.

`ui.service.port` controls the port exposed by the Kubernetes Service. Dagu continues to listen on `ui.containerPort` (8080 by default), so the Service can expose port 80 without requiring the container to bind a privileged port:

```yaml
ui:
  containerPort: 8080
  service:
    port: 80
```

When `ingress.tls.enabled` is true, set `ingress.tls.secretName` to a TLS Secret or leave it empty when the ingress controller provides the default certificate.

For temporary access with the defaults:

```bash
kubectl --namespace dagu port-forward service/dagu-ui 8080:8080

# Visit http://localhost:8080
```

## Storage Constraints

State is file-backed in both deployment modes:

- Standalone mode runs one replica and uses a `ReadWriteOnce` PVC by default.
- Distributed mode requires the UI, scheduler, and coordinator to share `ReadWriteMany` storage.
- Workers use ephemeral pod storage for their local runtime files.
- Dagu does not require an external database.

## Uninstall

```bash
helm uninstall dagu --namespace dagu
```

The `dagu-data` PersistentVolumeClaim and its data are retained by default. Delete it explicitly when the data is no longer needed:

```bash
kubectl delete pvc dagu-data --namespace dagu
```

An existing claim supplied through `persistence.existingClaim` is never created or deleted by the chart. For another release name or a name override, locate chart-created PVCs with `kubectl get pvc --namespace <namespace> -l app.kubernetes.io/instance=<release>`.
