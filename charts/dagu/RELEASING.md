# Releasing The Dagu Helm Chart

This document covers publication of the Helm chart in `charts/dagu`.

## One-Time Repository Setup

1. Create a `gh-pages` branch in `dagucloud/dagu`.
2. In GitHub repository settings, enable GitHub Pages from the `gh-pages` branch root.
3. Ensure GitHub Actions can write repository contents with `GITHUB_TOKEN`.

The publish workflow in this repository assumes the `gh-pages` branch already exists. That matches the upstream `helm/chart-releaser-action` prerequisites.

## What Triggers A Chart Release

- `.github/workflows/chart-release.yaml` runs on pushes to `main` that touch packaged chart files in `charts/dagu`.
- A new chart release is created only when `charts/dagu/Chart.yaml` contains a chart `version` that has not already been published.
- Chart releases are named `helm-dagu-<chart-version>`.
- The workflow sets `skip_existing: true` so reruns do not fail if the GitHub release tag already exists.
- The workflow sets `mark_as_latest: false` so chart releases do not replace the repository's application release as GitHub's "latest release".
- The release name template and fixed chart release body are defined in `cr.yaml` and `charts/dagu/RELEASE.md`.

## Before Merging A Chart Release

1. Update `charts/dagu/Chart.yaml -> version`.
   Any change to packaged chart files, including `charts/dagu/README.md` and `charts/dagu/RELEASE.md`, requires a new chart version.
2. Set `Chart.yaml -> appVersion` to the supported Dagu image version. `values.yaml -> image.tag` normally remains empty so it inherits `appVersion`.
3. Ensure the chart CI workflow passes:
   - `helm lint --strict ./charts/dagu`
   - `helm template dagu ./charts/dagu`
   - `helm template dagu ./charts/dagu --set deploymentMode=distributed --set persistence.accessMode=ReadWriteMany --set persistence.storageClass=nfs-client`
   - `helm package ./charts/dagu`

`charts/dagu/RELEASING.md` is ignored by `.helmignore` and is not part of the packaged chart. `charts/dagu/RELEASE.md` is packaged and becomes the GitHub release body for chart releases.

## After Merge

1. Confirm the `ReleaseHelmCharts` workflow succeeded on `main`.
2. Confirm a GitHub Release named `helm-dagu-<chart-version>` exists and includes the `.tgz` package.
3. Confirm `gh-pages/index.yaml` contains the new chart version.
4. Confirm the published repository works:

```bash
helm repo add dagu https://dagucloud.github.io/dagu
helm repo update
helm search repo dagu
helm pull dagu/dagu --version <chart-version>
```

## Removing A Broken Chart Version

No automation is provided for yanking a published chart version.

If a published version must be removed:

1. Delete the GitHub Release `helm-dagu-<chart-version>` and its chart asset.
2. Remove that version entry from `gh-pages/index.yaml`.
3. Commit the `index.yaml` change to `gh-pages`.
4. Publish a new chart version. Do not reuse the removed version number.
