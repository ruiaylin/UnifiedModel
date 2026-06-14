# Deployments

This directory contains local deployment assets for UModel Open Source.

| Path | Purpose |
|---|---|
| `docker/Dockerfile` | Builds `umodel-server` into a small runtime image. |
| `compose/docker-compose.yaml` | Runs the PG+AGE server and Vite web UI with persistent local Docker volumes. |

## Provider Default

The open-source deployment assets use `--graphstore file.memory` by default. This avoids local Ladybug runtime requirements and persists GraphStore JSON data under `/data`.

Use `local.ladybug` only in an environment that has:

- A Ladybug-enabled build.
- The `liblbug` runtime available to the process.
- A deliberate operational reason to use the Ladybug-backed provider.

## Docker

Build the image from the repository root:

```bash
docker build -f deployments/docker/Dockerfile -t umodel-open-source:local .
```

Run the server:

```bash
docker run --rm \
  -p 8080:8080 \
  -v umodel-data:/data \
  umodel-open-source:local
```

The container starts:

```bash
umodel-server --addr :8080 --data /data --graphstore file.memory
```

Check health:

```bash
curl http://localhost:8080/healthz
```

## Docker Compose

The Compose stack is configured for the local PG+AGE test environment. Copy the example env file and inject the real `UMODEL_DSN` value through your shell, secret manager, or an untracked `deployments/compose/.env` file:

```bash
cp deployments/compose/.env.example deployments/compose/.env
$EDITOR deployments/compose/.env
```

Start:

```bash
docker compose -f deployments/compose/docker-compose.yaml up --build
```

Stop:

```bash
docker compose -f deployments/compose/docker-compose.yaml down
```

Remove persisted data:

```bash
docker compose -f deployments/compose/docker-compose.yaml down -v
```

## Ports And Data

| Setting | Default | Notes |
|---|---|---|
| API port | `8082` | Exposed as `http://localhost:8082`; override with `UMODEL_SERVER_PORT`. |
| Web UI port | `5174` | Exposed as `http://localhost:5174`; override with `UMODEL_WEB_PORT`. |
| Data directory | `/data` | Mounted to the `umodel-data` Docker volume in Compose. |
| GraphStore provider | `postgres.age` | Override with `UMODEL_GRAPHSTORE`; requires `UMODEL_DSN` for PG+AGE. |
| Graph prefix | `ws_` | Override with `UMODEL_GRAPH_PREFIX`. |

Workspace metadata is persisted separately at `/data/workspaces.json` for persistent providers, including `postgres.age`.

## Import The Demo In Docker

After the server starts:

```bash
go run ./cmd/umctl --addr http://localhost:8082 workspace create demo '{"name":"Demo"}'
curl -X POST http://localhost:8082/api/v1/samples/demo/multi-domain-quickstart:import \
  -H 'Content-Type: application/json' \
  -d '{}'
go run ./cmd/umctl --addr http://localhost:8082 query run demo ".umodel | limit 5"
```

## Compatibility Notes

- The JSON files written by `file.memory` are useful for local inspection and demos, but they are not a long-term storage compatibility contract.
- Do not run multiple writers against the same file-memory directory.
- When upgrading between development builds, prefer exporting or re-importing example data rather than depending on raw JSON layout stability.
- If a future release changes storage layout, document the migration in [CHANGELOG.md](../CHANGELOG.md).
