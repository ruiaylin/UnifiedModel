# Operations World Model Ontology

中文：[运维领域知识图谱本体与元模型设计](../../zh/architecture/operations-world-model-ontology.md)

## Decision Summary

The main ontology shifts from a database graph to an operations world model. It should be delivered as a UModel model pack, not as a core schema rewrite. Entity types reuse `entity_set`; relation semantics reuse `entity_set_link`; runtime objects and edges continue to flow through EntityStore.

The model has three layers:

| Layer | Scope | UModel representation |
|---|---|---|
| L1 topology | infrastructure, PaaS/middleware, services, business objects, operations objects, coarse code objects | `entity_set`, `entity_set_link`, EntityStore records |
| L2 monitoring | metrics, logs, traces, events, profiles | `metric_set`, `log_set`, `trace_set`, `event_set`, `profile_set`, `data_link`, `storage_link` |
| L3 code graph | repo, module, key API, service-to-code bridge edges | coarse `code.*` entities in UModel; function-level graph stays in GitNexus |

Database graph modeling remains as a `db.*` subdomain and does not block the operations track.

## Existing Context

`schemas/manifest.yaml` already includes the required model kinds: `entity_set`, telemetry datasets, `prometheus`, `entity_set_link`, `data_link`, and `storage_link` (`schemas/manifest.yaml` L9-L31).

`entity_set` explicitly describes IT observability entities such as hosts, containers, applications, code repos, and operators (`schemas/core/dataset/entity_set.schema.yaml` L1-L4). `entity_set_link` already lists relation semantics such as `calls`, `runs`, `contains`, `monitors`, `affects`, `serves`, `hosted_by`, `use`, `exec`, and `access` (`schemas/core/link/entity_set_link.schema.yaml` L113-L167).

`data_link` links EntitySet/Link definitions to datasets and supports `fields_mapping`, `produce`, `related_to`, and `data_filter` (`schemas/core/link/data_link.schema.yaml` L1-L3, L39-L73). Query Service already returns telemetry query plans from `.entity_set get_metrics/get_logs` instead of executing raw storage reads (`docs/en/guides/query-service.md`).

## GitNexus Impact Assessment

Evidence:

- Baseline: `main`, `origin/main`, and the working branch were all at `e5005a51145a88bd0c83cbb4e425554c046032c3`.
- Fresh graph: `UnifiedModel-qfa72-qfa73-main`, built from detached `origin/main`.
- Graph size: 11,445 nodes / 21,474 edges / 235 clusters / 300 flows.
- Local service: `http://127.0.0.1:4747/api/info` reported `postgresql+age`; service-side `UnifiedModel-ruiaylin` was indexed at the same commit.

Impact results:

| Target | GitNexus result | Design response |
|---|---|---|
| `pkg/contract/contracts.go:GraphStore` | MEDIUM, 38 impacted nodes | Do not change the interface |
| `internal/graphstore/provider.go:NewProvider` | HIGH, 7 impacted nodes; affects server and MCP startup | Leave provider changes to UM-08 |
| `internal/bootstrap/app.go:NewAppWithGraphStore` | HIGH, 6 impacted nodes | Do not change bootstrap in UM-01 |
| `internal/query/service.go:Service.Execute` | covered by query golden and routing tests | Do not change SPL behavior in UM-01 |
| `internal/entitystore/service.go:Service.WriteEntities` | affects sample import and expire flows | Use existing write contract |

UM-01 is therefore a low-risk documentation/model-pack change. Any later change to GraphStore, provider registry, bootstrap, Query parser/executor, or public APIs must be reviewed separately.

## Entity Type System

Naming:

- EntitySet name: `{domain}.{entity}`.
- Runtime stable key: `domain/name/entity_id`.
- Main domains: `infra`, `apm`, `biz`, `ops`.
- Coarse code catalog domain: `code`.
- Per-repo code domains: `code.<repo_slug>`, for example `code.unifiedmodel`.
- Database subdomain: `db`.

Core entities:

| Domain | EntitySet | Layer |
|---|---|---|
| `infra` | `infra.host`, `infra.container`, `infra.k8s_cluster`, `infra.k8s_namespace`, `infra.k8s_workload`, `infra.k8s_pod`, `infra.middleware_instance`, `infra.database_instance`, `infra.network_device`, `infra.storage_volume` | L1 |
| `apm` | `apm.service`, `apm.api`, `apm.endpoint`, `apm.slo`, `apm.alert` | L1/L2 |
| `biz` | `biz.transaction`, `biz.session`, `biz.process` | L1/L2 |
| `ops` | `ops.environment`, `ops.deployment`, `ops.change`, `ops.incident`, `ops.team` | L1/L2/L3 |
| `code` | `code.repo` | L3 |
| `code.<repo>` | `code.<repo>.module`, `code.<repo>.api` | L3 |

Every runtime entity should carry source and freshness metadata: `source_system`, `source_ref`, `source_revision`, `confidence`, `first_observed_time`, `last_observed_time`, and `keep_alive_seconds`.

## Relation Type System

Recommended relation directions:

| Relation | Source | Destination |
|---|---|---|
| `contains` | cluster/namespace/workload | child runtime resource |
| `runs_on` | service or pod | workload or host |
| `deployed_in` | service | environment |
| `calls` | service or API | service or API |
| `depends_on` | service | middleware, database, cache, queue |
| `owned_by` | service, infra resource, repo | team |
| `impacts` | incident or alert | service or business transaction |
| `monitors` | SLO or alert | service |
| `implements` | repo or module | service or API |
| `exposes` | service | API |
| `produces` | deployment or change | service |
| `belongs_to` | business transaction or session | service |

Bridge relations should record `mapping_method`, `gitnexus_repo`, `git_ref`, and `confidence`.

## Typical Instances

1. Service-to-host-to-Prometheus:
   `apm.service -> runs_on -> infra.host`, plus `apm.service -> data_link -> apm.metric.service -> storage_link -> prometheus`.

2. Business-to-service-to-incident:
   `biz.transaction -> depends_on -> apm.service`, and `ops.incident -> impacts -> apm.service`.

3. Service-to-code bridge:
   `code.repo -> implements -> apm.service`, and `code.<repo>.module -> exposes -> apm.api`.

Example SPL:

```spl
.entity with(domain='apm', name='apm.service', query='payment-gateway') | project __entity_id__,service_id,owner_team,status
```

```spl
.entity_set with(domain='apm', name='apm.service', ids=['svc-payment-gateway'])
  | entity-call get_metrics('apm', 'apm.metric.service', 'latency_p99_ms', step='30s')
```

```spl
.topo | graph-call getNeighborNodes('in', 1,
  [(:\"apm@apm.service\" {__entity_id__: 'svc-umodel-server'})])
  | with(__relation_type__='implements')
  | project src,relation,dest
```

## Interface Contract

UM-01 adds no public API. It uses existing contracts:

| Purpose | Interface |
|---|---|
| Model pack write/validation | `POST /api/v1/umodel/{workspace}/import`, `validate`, `elements` |
| Runtime entity write | `POST /api/v1/entitystore/{workspace}/entities:write` |
| Runtime relation write | `POST /api/v1/entitystore/{workspace}/relations:write` |
| Read/query | `POST /api/v1/query/{workspace}/execute`, `explain` |
| Agent access | `GET /api/v1/agent/{workspace}/discover`, `tools:execute` |

## Data / Migration Plan

No migration is required for `schemas/manifest.yaml`, `pkg/model`, `pkg/contract`, or GraphStore providers. New assets should be model packs, synthetic fixtures, DataLink/StorageLink examples, and coarse GitNexus bridge fixtures. The old database graph becomes an optional `db` subdomain.

## Implementation Slices

1. Create the operations model pack skeleton.
2. Add L1 EntitySet and EntitySetLink definitions.
3. Add L2 DataLink, MetricSet, LogSet, TraceSet, and Prometheus examples.
4. Add L3 GitNexus bridge examples.
5. Add synthetic runtime entity/relation fixtures.
6. Add validation and query examples.

## Risks

| Risk | Level | Mitigation |
|---|---|---|
| Uncertain entity sources | High | Prefer deterministic sources; mark inferred data |
| Stale topology | High | Require freshness fields and TTL |
| Wrong service-to-code bridges | Medium | Start with manual and deployment-config mappings |
| GitNexus API drift | Medium | Keep only coarse anchors in UModel |
| Telemetry leakage | Medium | Store plans and mappings, not raw telemetry |
| Graph cardinality growth | Medium | Keep function-level graph in GitNexus |

## Acceptance Mapping

UM-01 AC-01 through AC-05 are covered by the entity taxonomy, relation taxonomy, schema mapping, three instances, and SPL examples above.
