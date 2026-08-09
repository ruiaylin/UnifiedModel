# 部署

English version: [README.md](README.md)

本目录包含 UModel Open Source 的本地部署资产。

| 路径 | 作用 |
|---|---|
| `docker/Dockerfile` | 将 `umodel-server` 构建为小型运行时镜像。 |
| `compose/docker-compose.yaml` | 使用持久化 Docker volume 运行 PG+AGE 后端和 Vite 前端。 |

## 默认 Provider

开源部署资产默认使用 `--graphstore file.memory`。这样不需要本地 Ladybug runtime，并将 GraphStore JSON 数据持久化到 `/data`。

仅在具备 Ladybug-enabled build、`liblbug` runtime 且有明确运维原因时使用 `local.ladybug`。

## Docker

```bash
docker build -f deployments/docker/Dockerfile -t umodel-open-source:local .
docker run --rm \
  -p 8080:8080 \
  -v umodel-data:/data \
  umodel-open-source:local
```

健康检查：

```bash
curl http://localhost:8080/healthz
```

## Docker Compose

Compose 栈面向本地 PG+AGE 测试环境。先复制示例环境文件，再通过 shell、密钥管理器或未跟踪的 `deployments/compose/.env` 注入真实 `UMODEL_DSN`：

```bash
cp deployments/compose/.env.example deployments/compose/.env
$EDITOR deployments/compose/.env
```

```bash
docker compose -f deployments/compose/docker-compose.yaml up --build
docker compose -f deployments/compose/docker-compose.yaml down
```

删除持久化数据：

```bash
docker compose -f deployments/compose/docker-compose.yaml down -v
```

## 端口与数据

| 配置 | 默认值 | 说明 |
|---|---|---|
| API port | `8082` | `http://localhost:8082`，可用 `UMODEL_SERVER_PORT` 覆盖。 |
| Web UI port | `5174` | `http://localhost:5174`，可用 `UMODEL_WEB_PORT` 覆盖。 |
| Data directory | `/data` | Compose 中挂载到 `umodel-data` volume。 |
| GraphStore provider | `postgres.age` | 可用 `UMODEL_GRAPHSTORE` 覆盖；PG+AGE 需要 `UMODEL_DSN`。 |
| Graph prefix | `ws_` | 可用 `UMODEL_GRAPH_PREFIX` 覆盖。 |

持久化 provider（包括 `postgres.age`）的 workspace metadata 会写入 `/data/workspaces.json`。

## 导入 Demo

```bash
go run ./cmd/umctl --addr http://localhost:8082 workspace create demo '{"name":"Demo"}'
curl -X POST http://localhost:8082/api/v1/samples/demo/multi-domain-quickstart:import \
  -H 'Content-Type: application/json' \
  -d '{}'
go run ./cmd/umctl --addr http://localhost:8082 query run demo ".umodel | limit 5"
```
