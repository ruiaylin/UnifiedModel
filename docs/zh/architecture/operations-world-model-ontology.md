# 运维领域知识图谱本体与元模型设计

English: [Operations World Model Ontology](../../en/architecture/operations-world-model-ontology.md)

## Decision Summary

推荐把主线从数据库对象图调整为运维世界模型，并以 UModel model pack 表达领域本体。模型层不新增 core schema kind，不修改 `GraphStore` contract，不新增公共读取 API；实体类型继续复用 `entity_set`，关系类型继续复用 `entity_set_link`，实体与关系实例继续写入 EntityStore。

运维世界模型分三层：

| 层 | 范围 | UModel 表达 |
|---|---|---|
| L1 拓扑 | 基础设施、PaaS/中间件、应用服务、业务对象、运维对象、粗粒度代码对象 | `entity_set` + `entity_set_link` + EntityStore runtime records |
| L2 监控 | Metric、Log、Trace、Event、Profile 与实体的绑定 | `metric_set`、`log_set`、`trace_set`、`event_set`、`profile_set`、`data_link`、`storage_link` |
| L3 代码图谱 | repo、module、key API、service 到 repo/module 的桥边 | UModel 保存粗粒度 `code.*` 实体；函数级调用图保留在 GitNexus |

数据库图谱保留为 `db.*` 子域，作为运维模型里的资源或依赖对象，不再阻塞主线。

## Existing Context

当前 `schemas/manifest.yaml` 已内置本设计需要的模型 kind：`entity_set`、`metric_set`、`log_set`、`event_set`、`trace_set`、`profile_set`、`prometheus`、`entity_set_link`、`data_link`、`storage_link` 等，见 `schemas/manifest.yaml` L9-L31。

`entity_set` schema 已明确支持 IT 可观测场景中的主机、容器、进程、应用、Code Repo、运维人员等实体，见 `schemas/core/dataset/entity_set.schema.yaml` L1-L4。`entity_set_link` 已有 `calls`、`runs`、`contains`、`monitors`、`affects`、`serves`、`hosted_by`、`use`、`exec`、`access` 等建议关系语义，见 `schemas/core/link/entity_set_link.schema.yaml` L113-L167。

`data_link` 用于 EntitySet/Link 与 DataSet 的关联，并提供 `fields_mapping`、`produce`、`related_to` 与 `data_filter`，见 `schemas/core/link/data_link.schema.yaml` L1-L3、L39-L73。`metric_set` 面向主机 CPU、内存、磁盘等监控指标，见 `schemas/core/dataset/metric_set.schema.yaml` L1-L3；Query Service 已能通过 `.entity_set` 的 `get_metrics`/`get_logs` 只返回下游查询计划，不直接查询遥测存储，见 `docs/zh/guides/query-service.md` L67-L79。

Workspace 和 domain 已是 UModel 的隔离与命名边界，公共 API 在路径中携带 workspace，domain 用于语义命名空间，见 `docs/zh/concepts/workspaces-and-domains.md` L10-L25、L34-L69。

## GitNexus Impact Assessment

图谱依据：

- 基线：`main`、`origin/main`、当前工作分支均为 `e5005a51145a88bd0c83cbb4e425554c046032c3`。
- 图谱：在 detached `origin/main` worktree `UnifiedModel-qfa72-qfa73-main` 上执行 `gitnexus analyze --force --skip-agents-md --name UnifiedModel-qfa72-qfa73-main`。
- 结果：11,445 nodes / 21,474 edges / 235 clusters / 300 flows。
- 4747 GitNexus 服务：`/api/info` 返回 `postgresql+age` backend，`/api/repos` 中 `UnifiedModel-ruiaylin` 同为 `e5005a5`。

影响范围：

| 目标 | GitNexus 结果 | 影响 |
|---|---|---|
| `pkg/contract/contracts.go:GraphStore` | MEDIUM，38 impacted nodes | 修改接口会触达 provider registry、bootstrap、server/MCP、CLI、SDK、文档与测试 |
| `internal/graphstore/provider.go:NewProvider` | HIGH，7 impacted nodes；影响 `cmd/umodel-server/main.go:main` 和 `cmd/umodel-mcp/main.go:run` | 新 provider 或 provider 选择逻辑必须重点验证 |
| `internal/bootstrap/app.go:NewAppWithGraphStore` | HIGH，6 impacted nodes；影响 server/MCP 启动流程 | 不应在 UM-01 中修改 bootstrap |
| `internal/query/service.go:Service.Execute` context | incoming 包含 golden、query routing、EntityStore visibility 测试 | SPL/Query 行为变更需要 query test 覆盖 |
| `internal/entitystore/service.go:Service.WriteEntities` context | upstream 触达 sample import、expire flows | 实体模型变更应通过现有 EntityStore 写入契约验证 |

结论：UM-01 采用文档和 model pack 方式落地，风险等级为 Low。若实现阶段改 `GraphStore`、provider selection、bootstrap、Query parser/executor 或公共 API，则风险升级为 Medium/High，并必须补充对应测试路径。

重点验证路径：

- `.umodel with(kind='entity_set')` 能列出运维实体定义。
- EntityStore 写入 `infra.*`、`apm.*`、`biz.*`、`ops.*`、`code.*` 实体后，`.entity` 可读取。
- EntityStore 写入 `calls`、`runs_on`、`deployed_in`、`implements` 等关系后，`.topo` 可追溯。
- `.entity_set ... get_metrics(...)` 对 Prometheus 返回查询计划，不读取原始指标数据。
- `umodel-server` 与 `umodel-mcp` 启动路径不因 model pack 增加而变化。

## Entity Type System

命名规范：

- EntitySet name 使用 `{domain}.{entity}`。
- 运行时 entity stable key 继续由 `domain/name/entity_id` 组成。
- L1/L2 主域为 `infra`、`apm`、`biz`、`ops`。
- 代码粗粒度目录域为 `code`，每个 GitNexus repo 的私有代码实体使用独立 domain：`code.<repo_slug>`，例如 `code.unifiedmodel`。
- 数据库子域使用 `db`，例如 `db.instance`、`db.schema`、`db.table`，不阻塞运维主线。

核心实体：

| Domain | EntitySet | 层 | 关键属性 |
|---|---|---|---|
| `infra` | `infra.host` | L1 | `host_id`、`hostname`、`ip`、`region`、`zone`、`os`、`owner_team` |
| `infra` | `infra.container` | L1 | `container_id`、`image`、`runtime`、`host_id`、`status` |
| `infra` | `infra.k8s_cluster` | L1 | `cluster_id`、`provider`、`region`、`environment` |
| `infra` | `infra.k8s_namespace` | L1 | `cluster_id`、`namespace`、`environment` |
| `infra` | `infra.k8s_workload` | L1 | `workload_uid`、`kind`、`namespace`、`replicas`、`image` |
| `infra` | `infra.k8s_pod` | L1 | `pod_uid`、`pod_name`、`node_name`、`phase` |
| `infra` | `infra.middleware_instance` | L1 | `instance_id`、`middleware_type`、`version`、`endpoint` |
| `infra` | `infra.database_instance` | L1 | `instance_id`、`engine`、`version`、`endpoint`、`role` |
| `infra` | `infra.network_device` | L1 | `device_id`、`type`、`cidr`、`region` |
| `infra` | `infra.storage_volume` | L1 | `volume_id`、`class`、`capacity`、`mount_path` |
| `apm` | `apm.service` | L1/L2 | `service_id`、`service_name`、`language`、`tier`、`slo_tier`、`owner_team` |
| `apm` | `apm.api` | L1/L3 | `api_id`、`method`、`path`、`protocol`、`version` |
| `apm` | `apm.endpoint` | L2 | `endpoint_id`、`host`、`port`、`protocol` |
| `apm` | `apm.slo` | L2 | `slo_id`、`indicator`、`target`、`window` |
| `apm` | `apm.alert` | L2 | `alert_id`、`severity`、`status`、`source`、`fired_at` |
| `biz` | `biz.transaction` | L1/L2 | `transaction_id`、`name`、`criticality`、`owner` |
| `biz` | `biz.session` | L2 | `session_id`、`tenant_id`、`channel`、`region` |
| `biz` | `biz.process` | L1 | `process_id`、`name`、`stage`、`sla` |
| `ops` | `ops.environment` | L1 | `env_id`、`name`、`type`、`region` |
| `ops` | `ops.deployment` | L1/L3 | `deployment_id`、`version`、`commit_sha`、`image`、`deployed_at` |
| `ops` | `ops.change` | L1/L3 | `change_id`、`summary`、`risk`、`status` |
| `ops` | `ops.incident` | L2 | `incident_id`、`severity`、`status`、`opened_at`、`closed_at` |
| `ops` | `ops.team` | L1 | `team_id`、`name`、`owner_group`、`oncall` |
| `code` | `code.repo` | L3 | `repo_id`、`git_url`、`default_branch`、`gitnexus_repo`、`last_indexed_ref` |
| `code.<repo>` | `code.<repo>.module` | L3 | `module_id`、`path`、`language`、`package` |
| `code.<repo>` | `code.<repo>.api` | L3 | `api_id`、`symbol`、`file_path`、`line_start`、`line_end` |

所有实体都应带来源与时效字段：

| 字段 | 含义 |
|---|---|
| `source_system` | CMDB、service_registry、kubernetes、deployment_config、gitnexus、manual_registry 等 |
| `source_ref` | 源系统中的稳定引用 |
| `source_revision` | Git SHA、resourceVersion、配置版本或抓取批次 |
| `confidence` | `manual`、`verified`、`inferred` |
| `first_observed_time` | 首次观测时间 |
| `last_observed_time` | 最近观测时间 |
| `keep_alive_seconds` | 过期窗口 |

实体数据源策略：确定性高于自动化。手工注册、CMDB、服务注册表、部署配置、Kubernetes owner references 的可信度高于从日志或指标自动猜测出的实体；自动发现结果必须标注 `confidence=inferred`，并设置较短 `keep_alive_seconds`。

## Relation Type System

关系通过 `entity_set_link.spec.entity_link_type` 定义，并通过 EntityStore 写入 runtime relation。推荐方向如下：

| Relation | Source | Destination | 基数 | 用途 |
|---|---|---|---|---|
| `contains` | `infra.k8s_cluster` | `infra.k8s_namespace` / `infra.k8s_workload` | 1:N | L1 资源层级 |
| `runs_on` | `apm.service` / `infra.k8s_pod` | `infra.k8s_workload` / `infra.host` | N:1 | 服务或 workload 到运行位置 |
| `deployed_in` | `apm.service` | `ops.environment` | N:1 | 服务部署环境 |
| `calls` | `apm.service` / `apm.api` | `apm.service` / `apm.api` | N:N | 服务/API 调用链 |
| `depends_on` | `apm.service` | `infra.middleware_instance` / `infra.database_instance` | N:N | 应用到中间件、数据库、缓存、MQ |
| `owned_by` | `apm.service` / `infra.*` / `code.repo` | `ops.team` | N:1 | 责任边界 |
| `impacts` | `ops.incident` / `apm.alert` | `apm.service` / `biz.transaction` | N:N | 事件影响面 |
| `monitors` | `apm.slo` / `apm.alert` | `apm.service` | N:N | 监控与告警绑定 |
| `implements` | `code.repo` / `code.<repo>.module` | `apm.service` / `apm.api` | N:N | L3 到 L1 桥边 |
| `exposes` | `apm.service` | `apm.api` | 1:N | 服务公开 API |
| `produces` | `ops.deployment` / `ops.change` | `apm.service` | N:N | 发布、变更到服务 |
| `belongs_to` | `biz.transaction` / `biz.session` | `apm.service` | N:N | 业务到应用 |

关系也应带 `source_system`、`source_ref`、`source_revision`、`confidence`、`first_observed_time`、`last_observed_time` 和 `keep_alive_seconds`。桥边 `implements` 必须记录 `mapping_method`，取值为 `manual`、`deployment_config`、`service_registry`、`gitnexus_export` 或 `inferred`。

## Typical Model Instances

### 实例 1：服务到主机和 Prometheus 指标

Model elements：

- `apm.service`
- `infra.host`
- `apm.service_runs_on_infra.host`，`entity_link_type=runs_on`
- `apm.metric.service`
- `apm.prometheus.core`
- `apm.service_related_to_apm.metric.service`，`data_link_type=related_to`，`fields_mapping.service_id=service_id`
- `apm.metric.service_to_prometheus`，`storage_link`

SPL：

```spl
.entity with(domain='apm', name='apm.service', query='payment-gateway') | project __entity_id__,service_id,owner_team,status
```

```spl
.topo | graph-call getNeighborNodes('out', 1,
  [(:\"apm@apm.service\" {__entity_id__: 'svc-payment-gateway'})])
  | with(__relation_type__='runs_on')
  | project src,relation,dest
```

```spl
.entity_set with(domain='apm', name='apm.service', ids=['svc-payment-gateway'])
  | entity-call get_metrics('apm', 'apm.metric.service', 'latency_p99_ms', step='30s')
```

### 实例 2：业务交易到服务到事故

Model elements：

- `biz.transaction`
- `apm.service`
- `ops.incident`
- `biz.transaction_depends_on_apm.service`，`entity_link_type=depends_on`
- `ops.incident_impacts_apm.service`，`entity_link_type=impacts`

SPL：

```spl
.entity with(domain='ops', name='ops.incident', query='sev1 open') | project incident_id,severity,status,opened_at
```

```spl
.topo | graph-call getNeighborNodes('both', 2,
  [(:\"ops@ops.incident\" {__entity_id__: 'inc-20260613-001'})])
  | project src,relation,dest
```

### 实例 3：服务到 GitNexus 代码图谱桥边

Model elements：

- `apm.service`
- `code.repo`
- `code.unifiedmodel.module`
- `code.unifiedmodel.api`
- `code.repo_implements_apm.service`，`entity_link_type=implements`
- `code.unifiedmodel.module_exposes_apm.api`，`entity_link_type=exposes`

Runtime relation properties：

```json
{
  "relation_type": "implements",
  "mapping_method": "deployment_config",
  "gitnexus_repo": "UnifiedModel-ruiaylin",
  "git_ref": "e5005a51145a88bd0c83cbb4e425554c046032c3",
  "confidence": "verified"
}
```

SPL：

```spl
.topo | graph-call getNeighborNodes('in', 1,
  [(:\"apm@apm.service\" {__entity_id__: 'svc-umodel-server'})])
  | with(__relation_type__='implements')
  | project src,relation,dest
```

函数级调用链不进入 UModel。需要函数、方法、文件级影响面时，Agent 通过 GitNexus 读取代码图谱；UModel 只保存能参与运维追溯的粗粒度 repo/module/API 桥边。

## Interface Contract

UM-01 不新增公共接口。设计落地使用现有接口：

| 目的 | 接口 |
|---|---|
| 写入/校验 model pack | `POST /api/v1/umodel/{workspace}/import`、`POST /api/v1/umodel/{workspace}/validate`、`POST /api/v1/umodel/{workspace}/elements` |
| 写入运行时实体 | `POST /api/v1/entitystore/{workspace}/entities:write` |
| 写入运行时关系 | `POST /api/v1/entitystore/{workspace}/relations:write` |
| 查询模型、实体、拓扑、查询计划 | `POST /api/v1/query/{workspace}/execute`、`POST /api/v1/query/{workspace}/explain` |
| Agent 访问 | `GET /api/v1/agent/{workspace}/discover`、`POST /api/v1/agent/{workspace}/tools:execute` |

CLI 继续使用：

- `umctl umodel import|validate|put`
- `umctl entity write|expire`
- `umctl topo write|expire`
- `umctl query run|explain`

## Data / Migration Plan

无需迁移 `schemas/manifest.yaml`，无需变更 `pkg/model` 或 `pkg/contract`，无需修改 GraphStore provider。数据库图谱旧方案不删除，后续以 `db` 子域 model pack 形式保留。

新增数据以 model pack、样例 fixture 和运行时 EntityStore records 进入：

1. 新增 `examples/ops-world-model` 或扩展 `examples/incident-investigation`，仅使用合成数据。
2. 先导入 L1 EntitySet 与 EntitySetLink。
3. 再导入 L2 Metric/Log/Trace/Event/Profile、DataLink、StorageLink。
4. 最后导入 L3 `code.*` 粗粒度实体与 `implements` 桥边。

回滚方式：删除新增 model pack 或切换 workspace；不需要数据 schema rollback。

## Implementation Slices

1. S：建立运维 model pack skeleton，覆盖 `infra`、`apm`、`biz`、`ops`、`code` domain 和 README。
2. S：补齐 L1 EntitySet 与 EntitySetLink，包含 `runs_on`、`contains`、`deployed_in`、`calls`、`depends_on`、`owned_by`。
3. M：补齐 L2 DataLink、MetricSet、LogSet、TraceSet、Prometheus storage 示例，并提供查询计划 SPL。
4. M：补齐 L3 GitNexus bridge 示例，定义 `code.repo`、`code.<repo>.module`、`code.<repo>.api` 与 `implements` 关系。
5. S：补充合成 runtime entity/relation fixture，验证服务到主机、服务到指标、服务到 repo 三条链路。
6. S：补充文档和验收脚本，运行 `umodel import`、`entity write`、`topo write`、`query run`。

## Risks

| 风险 | 等级 | 缓解 |
|---|---|---|
| 实体来源不确定 | High | 确定性来源优先；所有自动发现实体标注 `confidence=inferred` |
| 时效性失真 | High | 所有 runtime entity/relation 带 `last_observed_time` 与 `keep_alive_seconds` |
| 代码桥边误连 | Medium | 先支持手工映射和部署配置映射，再引入自动发现 |
| GitNexus 依赖变化 | Medium | UModel 只保存粗粒度同步结果；函数级查询留在 GitNexus |
| 遥测数据泄露 | Medium | UModel 只保存 DataLink 与查询计划，不保存原始 metric/log/trace |
| 图谱规模膨胀 | Medium | L3 仅入 repo/module/API；函数、方法、调用边不入 UModel |
| 公共接口误扩张 | Medium | 所有读取仍走 Query Service；新 API 必须单独设计和评审 |

## Acceptance Mapping

- UM-01 AC-01：核心实体类型由 `infra.*`、`apm.*`、`biz.*`、`ops.*`、`code.*` 覆盖。
- UM-01 AC-02：核心关系由 `contains`、`runs_on`、`deployed_in`、`calls`、`depends_on`、`owned_by`、`implements` 等覆盖。
- UM-01 AC-03：已映射到现有 `entity_set`、`entity_set_link`、`data_link`、`metric_set`、`prometheus` 等 schema kind。
- UM-01 AC-04：已给出服务-主机-监控、业务-服务-事故、服务-代码桥边 3 个实例。
- UM-01 AC-05：已给出 `.entity`、`.topo`、`.entity_set get_metrics` SPL 查询示例。
