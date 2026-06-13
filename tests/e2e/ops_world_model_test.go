package e2e_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alibaba/UnifiedModel/internal/bootstrap"
)

// TestOpsWorldModelMVP validates the ops-world model MVP end-to-end:
//   - Slice 1: model pack import (EntitySet + EntitySetLink + MetricSet + Prometheus + DataLink + StorageLink)
//   - Slice 2: entity/relation writing through EntityStore
//   - Slice 3: DataLink PromQL plan generation
//   - Slice 4: topology + DataLink aggregate query
//   - Slice 5: AC-07 code.module implements bridge edge
func TestOpsWorldModelMVP(t *testing.T) {
	server := httptest.NewServer(bootstrap.NewMemoryApp(t.TempDir()).Handler())
	defer server.Close()

	// --- Setup: create workspace and import ops-world model pack ---
	e2ePost(t, server.URL+"/api/v1/workspaces", map[string]any{
		"id":   "ops-test",
		"name": "Ops World Model Test",
	})

	imported := e2ePost(t, server.URL+"/api/v1/samples/ops-test/ops-world-model:import", nil)
	t.Logf("import result: entities=%v relations=%v umodel_imported=%v",
		imported["entity_count"], imported["relation_count"],
		imported["umodel"].(map[string]any)["imported"])

	// --- Slice 1: verify model pack loaded ---
	// Check entity_sets
	esRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".umodel with(kind='entity_set') | project domain,name,kind | sort domain,name | limit 20",
	})
	items := e2eRows(t, esRows)
	if len(items) < 5 {
		t.Fatalf("Slice 1: expected >= 5 entity_sets, got %d: %+v", len(items), items)
	}

	// Check data_link
	dlRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".umodel with(kind='data_link') | project domain,name,kind | limit 10",
	})
	dlItems := e2eRows(t, dlRows)
	if len(dlItems) < 1 {
		t.Fatalf("Slice 1: expected >= 1 data_link, got %d", len(dlItems))
	}

	// Check metric_set
	msRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".umodel with(kind='metric_set') | project domain,name,kind | limit 10",
	})
	msItems := e2eRows(t, msRows)
	if len(msItems) < 1 {
		t.Fatalf("Slice 1: expected >= 1 metric_set, got %d", len(msItems))
	}

	// Check prometheus
	promRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".umodel with(kind='prometheus') | project domain,name,kind | limit 10",
	})
	promItems := e2eRows(t, promRows)
	if len(promItems) < 1 {
		t.Fatalf("Slice 1: expected >= 1 prometheus storage, got %d", len(promItems))
	}
	t.Logf("Slice 1 PASS: %d entity_sets, %d data_links, %d metric_sets, %d prometheus",
		len(items), len(dlItems), len(msItems), len(promItems))

	// --- Slice 2: verify entities written through EntityStore ---
	svcRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".entity with(domain='apm', name='apm.service') | project __entity_id__,display_name,status,owner,environment | sort display_name | limit 10",
	})
	svcItems := e2eRows(t, svcRows)
	if len(svcItems) != 3 {
		t.Fatalf("Slice 2: expected 3 apm.service entities, got %d: %+v", len(svcItems), svcItems)
	}

	hostRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".entity with(domain='infra', name='infra.host') | project __entity_id__,display_name,ip_address | sort display_name | limit 10",
	})
	hostItems := e2eRows(t, hostRows)
	if len(hostItems) != 2 {
		t.Fatalf("Slice 2: expected 2 infra.host entities, got %d: %+v", len(hostItems), hostItems)
	}

	envRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".entity with(domain='ops', name='ops.environment') | project __entity_id__,display_name,env_type | sort display_name | limit 10",
	})
	envItems := e2eRows(t, envRows)
	if len(envItems) != 2 {
		t.Fatalf("Slice 2: expected 2 ops.environment entities, got %d: %+v", len(envItems), envItems)
	}

	// Verify relations written
	topoRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".topo | project src,relation,dest,__relation_type__ | sort __relation_type__ | limit 20",
	})
	topoItems := e2eRows(t, topoRows)
	if len(topoItems) < 9 {
		t.Fatalf("Slice 2: expected >= 9 relations, got %d: %+v", len(topoItems), topoItems)
	}

	// Verify idempotency: re-import should succeed
	reimported := e2ePost(t, server.URL+"/api/v1/samples/ops-test/ops-world-model:import", nil)
	t.Logf("re-import (idempotency): entities=%v relations=%v",
		reimported["entity_count"], reimported["relation_count"])

	t.Logf("Slice 2 PASS: 3 services, 2 hosts, 2 environments, %d relations", len(topoItems))

	// --- Slice 3: DataLink PromQL plan ---
	orderSvcID := findEntityID(t, svcItems, "order-service")
	if orderSvcID == "" {
		t.Fatal("Slice 3: could not find order-service entity ID")
	}

	// format=agent returns the plan as a top-level JSON object
	metricPlan := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query":  ".entity_set with(domain='apm', name='apm.service', ids=['" + orderSvcID + "']) | entity-call get_metrics('apm', 'apm.metric.service', 'request_count', step='30s')",
		"format": "agent",
	})
	t.Logf("metric plan mode=%v operation=%v", metricPlan["mode"], metricPlan["operation"])

	if metricPlan["mode"] != "plan" {
		t.Fatalf("Slice 3: expected mode=plan, got %v", metricPlan["mode"])
	}
	if metricPlan["operation"] != "get_metrics" {
		t.Fatalf("Slice 3: expected operation=get_metrics, got %v", metricPlan["operation"])
	}

	// Verify PromQL
	querySection, ok := metricPlan["query"].(map[string]any)
	if !ok {
		t.Fatalf("Slice 3: expected query section, got %T", metricPlan["query"])
	}
	queries, ok := querySection["queries"].([]any)
	if !ok || len(queries) == 0 {
		t.Fatalf("Slice 3: expected queries array, got %T or empty", querySection["queries"])
	}
	firstQuery := queries[0].(map[string]any)
	promql, ok := firstQuery["promql"].(string)
	if !ok || promql == "" {
		t.Fatalf("Slice 3: expected non-empty promql, got %v", firstQuery["promql"])
	}
	if !strings.Contains(promql, orderSvcID) {
		t.Fatalf("Slice 3: promql should contain service_id %s, got: %s", orderSvcID, promql)
	}
	if !strings.Contains(promql, "http_requests_total") {
		t.Fatalf("Slice 3: promql should reference http_requests_total, got: %s", promql)
	}

	// Verify all 3 metrics
	for _, metricName := range []string{"request_count", "error_count", "latency_p99_ms"} {
		mp := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
			"query":  ".entity_set with(domain='apm', name='apm.service', ids=['" + orderSvcID + "']) | entity-call get_metrics('apm', 'apm.metric.service', '" + metricName + "', step='1m')",
			"format": "agent",
		})
		if mp["mode"] != "plan" {
			t.Fatalf("Slice 3: metric %s returned unexpected: mode=%v", metricName, mp["mode"])
		}
	}
	t.Logf("Slice 3 PASS: promql=%s", promql)

	// --- Slice 4: topology + DataLink aggregate query ---
	// 4a: getDirectRelations for order-service
	topoQuery := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".topo | graph-call getDirectRelations([(:\"apm@apm.service\" {__entity_id__: '" + orderSvcID + "'})]) | project src,relation,dest,__relation_type__ | limit 20",
	})
	topoRelRows := e2eRows(t, topoQuery)
	if len(topoRelRows) < 2 {
		t.Fatalf("Slice 4a: expected >= 2 direct relations, got %d: %+v", len(topoRelRows), topoRelRows)
	}
	relTypes := make(map[string]bool)
	for _, r := range topoRelRows {
		if rt, ok := r["__relation_type__"].(string); ok {
			relTypes[rt] = true
		}
	}
	if !relTypes["runs_on"] {
		t.Fatalf("Slice 4a: missing runs_on relation: %+v", topoRelRows)
	}
	if !relTypes["deployed_in"] {
		t.Fatalf("Slice 4a: missing deployed_in relation: %+v", topoRelRows)
	}

	// 4b: 3-hop chain verification: service -> host -> environment
	// Step 1: service -> host (via runs_on)
	svcHostRels := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".topo | graph-call getDirectRelations([(:\"apm@apm.service\" {__entity_id__: '" + orderSvcID + "'})]) | project src,dest,__relation_type__ | limit 20",
	})
	svcHostItems := e2eRows(t, svcHostRels)
	var hostID string
	for _, r := range svcHostItems {
		if rt, ok := r["__relation_type__"].(string); ok && rt == "runs_on" {
			if dest, ok := r["dest"].(string); ok {
				// dest format: domain/entity_type/entity_id
				parts := strings.Split(dest, "/")
				if len(parts) == 3 {
					hostID = parts[2]
				}
			}
			break
		}
	}
	if hostID == "" {
		t.Fatalf("Slice 4b: could not find host from runs_on relation: %+v", svcHostItems)
	}

	// Step 2: host -> environment (via host_in_environment)
	hostEnvRels := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".topo | graph-call getDirectRelations([(:\"infra@infra.host\" {__entity_id__: '" + hostID + "'})]) | project src,dest,__relation_type__ | limit 10",
	})
	hostEnvItems := e2eRows(t, hostEnvRels)
	foundHostInEnv := false
	for _, r := range hostEnvItems {
		if rt, ok := r["__relation_type__"].(string); ok && rt == "host_in_environment" {
			foundHostInEnv = true
			break
		}
	}
	if !foundHostInEnv {
		t.Fatalf("Slice 4b: host_in_environment not found via host %s: %+v", hostID, hostEnvItems)
	}
	t.Logf("Slice 4 PASS: %d direct relations, 3-hop chain service->host->environment verified via host %s",
		len(topoRelRows), hostID)

	// --- Slice 5: AC-07 code.module implements bridge edge ---
	codeRows := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".entity with(domain='code', name='code.module') | project __entity_id__,display_name | limit 10",
	})
	codeItems := e2eRows(t, codeRows)
	if len(codeItems) < 1 {
		t.Fatalf("Slice 5: expected >= 1 code.module, got %d", len(codeItems))
	}
	codeModID := findEntityID(t, codeItems, "order-module")
	if codeModID == "" {
		t.Fatal("Slice 5: could not find order-module")
	}

	implQuery := e2ePost(t, server.URL+"/api/v1/query/ops-test/execute", map[string]any{
		"query": ".topo | graph-call getDirectRelations([(:\"code@code.module\" {__entity_id__: '" + codeModID + "'})]) | project src,relation,dest,__relation_type__ | limit 10",
	})
	implRows := e2eRows(t, implQuery)
	if len(implRows) < 1 {
		t.Fatalf("Slice 5: expected >= 1 implements relation, got %d: %+v", len(implRows), implRows)
	}
	foundImplements := false
	for _, r := range implRows {
		if rt, ok := r["__relation_type__"].(string); ok && rt == "implements" {
			foundImplements = true
			break
		}
	}
	if !foundImplements {
		t.Fatalf("Slice 5: implements relation not found: %+v", implRows)
	}
	t.Logf("Slice 5 PASS: code.module -> apm.service implements verified")
}

// findEntityID returns the __entity_id__ of the entity with the given display_name.
func findEntityID(t *testing.T, rows []map[string]any, displayName string) string {
	t.Helper()
	for _, r := range rows {
		if dn, ok := r["display_name"].(string); ok && dn == displayName {
			if id, ok := r["__entity_id__"].(string); ok {
				return id
			}
		}
	}
	return ""
}

// extractPlanJSON parses the plan from a query result row.
func extractPlanJSON(t *testing.T, row map[string]any) map[string]any {
	t.Helper()
	if row["mode"] == "plan" {
		return row
	}
	if q, ok := row["query"].(string); ok {
		var plan map[string]any
		if err := json.Unmarshal([]byte(q), &plan); err == nil {
			return plan
		}
	}
	if rt, ok := row["responseType"].(float64); ok && rt == 2 {
		if q, ok := row["query"].(string); ok && q != "" {
			var plan map[string]any
			if err := json.Unmarshal([]byte(q), &plan); err == nil {
				return plan
			}
		}
	}
	t.Fatalf("could not extract plan JSON from row: %+v", row)
	return nil
}
