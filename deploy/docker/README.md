# Docker Assets

This directory hosts Docker-centric deployment assets for Dagu.

- `compose.minimal.yaml` – lightweight stack with the web UI, scheduler, and coordinator for local experiments.
- `compose.prod.yaml` – production-like stack including OpenTelemetry collector and Prometheus.
- `otel-collector.yaml` – default collector configuration used by `compose.prod.yaml`.
- `prometheus.yaml` – scrape configuration paired with the production-like compose stack.

Run examples from the repository root:

```bash
docker build -f Dockerfile.dev -t dagu:dev .
docker build -f Dockerfile.alpine -t dagu:alpine .
docker compose -f deploy/docker/compose.minimal.yaml up -d
```

The standard Ubuntu image includes CA certificates and common runtime utilities such as `curl`, `git`, `jq`, the OpenSSH client, and `unzip`. The Alpine image remains minimal, while the development image includes the broader build and language toolchain.

The Compose stacks mount `deploy/docker/dags/` read-write on server-side Dagu services so Dagu can seed first-run examples and save DAG edits. Add `:ro` to that mount only when using immutable DAG sources.

Remote CLI contexts use the HTTP API on port `8080`. Include the API path in the context URL, for example `http://host:8080/api/v1`.

The production stack keeps coordinator port `50055` inside `dagu-net`. External workers require publishing that port and setting `DAGU_COORDINATOR_ADVERTISE` to a hostname or IP they can reach.
