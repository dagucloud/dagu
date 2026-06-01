# Docker Assets

This directory hosts Docker-centric deployment assets for Dagu.

- `compose.minimal.yaml` – lightweight stack with scheduler, worker, and UI for local experiments.
- `compose.prod.yaml` – production-like stack including OpenTelemetry collector and Prometheus.
- `Dockerfile.dev` – image for iterative development with build tooling preinstalled.
- `Dockerfile.alpine` – minimal Alpine-based image for slim deployments.
- `otel-collector.yaml` – default collector configuration used by `compose.prod.yaml`.
- `prometheus.yaml` – scrape configuration paired with the production-like compose stack.

Run examples from the repository root:

```bash
docker compose -f deploy/docker/compose.minimal.yaml up -d
```

The Compose stacks mount `deploy/docker/dags/` read-write so Dagu can seed first-run examples and save DAG edits. Add `:ro` to that mount only when using immutable DAG sources.
