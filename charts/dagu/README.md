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

The deployed container image comes from `values.yaml -> image.repository` and `values.yaml -> image.tag`. With the current defaults, the chart deploys `ghcr.io/dagucloud/dagu:latest`.

For chart publication and repository maintenance, see [`RELEASING.md`](./RELEASING.md).

## Architecture

The chart deploys four components:

- **Coordinator**: gRPC server for distributed task execution (port 50055, HTTP health on 8091 by default)
- **Scheduler**: Manages DAG execution schedules (port 8090 for health)
- **Worker**: Executes DAG steps (configurable pools with independent replicas, HTTP health on 8092 by default)
- **UI**: Web interface for managing DAGs (port 8080)

All components share a single PersistentVolumeClaim with `ReadWriteMany` access mode.
The chart mounts that shared volume at `/data`, sets `DAGU_HOME=/data`, and stores the shared base config at `/data/base.yaml` so fallback paths and inherited env stay aligned across UI, scheduler, coordinator, and workers.

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
    resources:
      requests:
        memory: "128Mi"
        cpu: "100m"
      limits:
        memory: "256Mi"
        cpu: "200m"
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

### Component Resources

```yaml
image:
  repository: ghcr.io/dagucloud/dagu
  tag: latest

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
    resources:
      requests:
        memory: "128Mi"
        cpu: "100m"

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

```bash
# Port forward to access UI
kubectl port-forward svc/dagu-ui 8080:8080

# Then visit http://localhost:8080
# On first run, you'll be prompted to create an admin account
```

## Current Constraints

This chart reflects Dagu's current architecture:

- **Shared filesystem required**: All components must share the same RWX volume
- **File-based state**: State is stored in files on the shared volume
- **No database**: Dagu does not use a database for state management

## Uninstall

```bash
helm uninstall dagu
```

**Warning**: This will delete the PersistentVolumeClaim and all data. Backup your DAGs and logs first!
