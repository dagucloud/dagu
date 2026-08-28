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

Remote CLI contexts use the web/API endpoint, not the coordinator port. Prefer an HTTPS endpoint and include the API path, for example `https://dagu.example.com/api/v1`. A direct `http://host:8080/api/v1` URL sends the API key without transport encryption; use it only on a trusted or encrypted network.

The production stack keeps coordinator port `50055` inside `dagu-net`. Before connecting external workers, set `DAGU_COORDINATOR_ADVERTISE` to an address they can reach and configure peer TLS or mTLS on the coordinator and workers. Publish port `50055` only to the restricted worker network. See the [transport security guide](https://docs.dagu.sh/server-admin/distributed/transport-security).
