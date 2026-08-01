Helm chart release for Dagu.

This GitHub release publishes the packaged `dagu` Helm chart for the Helm repository at `https://dagucloud.github.io/dagu`. It is not a Dagu application binary release.

Install the chart with:

```bash
helm repo add dagu https://dagucloud.github.io/dagu
helm repo update
helm upgrade --install dagu dagu/dagu \
  --namespace dagu \
  --create-namespace \
  --wait
```

The default standalone mode uses `ReadWriteOnce` access with the cluster's default StorageClass. Distributed installations can opt into separate workers and `ReadWriteMany` storage.

Application releases use separate `vX.Y.Z` tags.
