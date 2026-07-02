//go:build ladybug

package ladybug

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/alibaba/UnifiedModel/internal/graphstore"
	"github.com/alibaba/UnifiedModel/pkg/model"
)

func TestLadybugProviderConformance(t *testing.T) {
	if os.Getenv("UMODEL_TEST_LADYBUG") != "1" {
		t.Skip("set UMODEL_TEST_LADYBUG=1 and provide liblbug to run local.ladybug conformance tests")
	}

	dataRoot := t.TempDir()
	provider, err := NewProvider(graphstore.ProviderConfig{DataRoot: dataRoot})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	if err := provider.OpenWorkspace(ctx, model.WorkspaceMetadata{ID: "demo"}); err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	if err := provider.EnsureSchema(ctx, "demo"); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if capabilities, err := provider.Capabilities(ctx); err != nil || !capabilities.ControlledCypher {
		t.Fatalf("capabilities: %+v err=%v", capabilities, err)
	}
	if health, err := provider.Health(ctx); err != nil || health.Provider != graphstore.ProviderTypeLadybug || health.Status != "ok" {
		t.Fatalf("health: %+v err=%v", health, err)
	}

	if _, err := provider.PutUModelElements(ctx, model.UModelElementBatch{
		Workspace: "demo",
		Elements: []model.UModelElement{{
			Kind:    "entity_set",
			Domain:  "apm",
			Name:    "apm.service",
			Version: "v1",
			Spec: map[string]any{
				"display_name": "APM Service",
			},
		}},
	}); err != nil {
		t.Fatalf("put umodel: %v", err)
	}
	snapshot, err := provider.GetUModelSnapshot(ctx, model.UModelSnapshotRequest{Workspace: "demo"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Elements) != 1 {
		t.Fatalf("expected one element, got %+v", snapshot.Elements)
	}
	if snapshot.Elements[0].Version != "v1" || snapshot.Elements[0].Spec["display_name"] != "APM Service" {
		t.Fatalf("unexpected umodel snapshot: %+v", snapshot.Elements[0])
	}

	if _, err := provider.WriteEntities(ctx, model.EntityWriteBatch{
		Workspace: "demo",
		Entities: []model.EntityPayload{
			entity("54013ba69c196820e56801f1ef5aad54"),
			entity("177627f91af678a9b03e993f1a91917f"),
		},
	}); err != nil {
		t.Fatalf("write entity: %v", err)
	}

	from := time.Unix(150, 0)
	to := time.Unix(180, 0)
	entityRows, err := provider.QueryEntities(ctx, model.EntityQueryPlan{
		Workspace: "demo",
		Filters:   map[string]any{"domain": "apm", "name": "apm.*", "ids": []string{"54013ba69c196820e56801f1ef5aad54"}, "query": "cart service"},
		TimeRange: model.TimeRange{From: &from, To: &to},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("query entity: %v", err)
	}
	if len(entityRows.Rows) != 1 {
		t.Fatalf("expected one entity row, got %+v", entityRows.Rows)
	}
	if entityRows.Rows[0]["__entity_id__"] != "54013ba69c196820e56801f1ef5aad54" || entityRows.Rows[0]["display_name"] != "cart service" {
		t.Fatalf("unexpected entity row: %+v", entityRows.Rows[0])
	}

	future := time.Unix(1000, 0)
	futureRows, err := provider.QueryEntities(ctx, model.EntityQueryPlan{
		Workspace: "demo",
		TimeRange: model.TimeRange{From: &future},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("query future entity: %v", err)
	}
	if len(futureRows.Rows) != 0 {
		t.Fatalf("expected no future entity rows, got %+v", futureRows.Rows)
	}

	if _, err := provider.WriteRelations(ctx, model.RelationWriteBatch{
		Workspace: "demo",
		Relations: []model.RelationPayload{relation("54013ba69c196820e56801f1ef5aad54", "177627f91af678a9b03e993f1a91917f")},
	}); err != nil {
		t.Fatalf("write relation: %v", err)
	}
	topoRows, err := provider.QueryTopo(ctx, model.TopoQueryPlan{
		Workspace: "demo",
		Filters:   map[string]any{"relation_type": "calls"},
		TimeRange: model.TimeRange{From: &from, To: &to},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("query topo: %v", err)
	}
	if len(topoRows.Rows) != 1 {
		t.Fatalf("expected one topo row, got %+v", topoRows.Rows)
	}
	if topoRows.Rows[0]["src"] != "apm/apm.service/54013ba69c196820e56801f1ef5aad54" || topoRows.Rows[0]["dest"] != "apm/apm.service/177627f91af678a9b03e993f1a91917f" || topoRows.Rows[0]["relation"] != "calls" {
		t.Fatalf("unexpected topo row: %+v", topoRows.Rows[0])
	}
	if _, err := provider.WriteRelations(ctx, model.RelationWriteBatch{
		Workspace: "demo",
		Relations: []model.RelationPayload{relation("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "177627f91af678a9b03e993f1a91917f")},
	}); err != nil {
		t.Fatalf("write unrelated relation: %v", err)
	}
	seedTopoRows, err := provider.QueryTopo(ctx, model.TopoQueryPlan{
		Workspace: "demo",
		GraphCall: &model.GraphCallPlan{
			Name:    "getDirectRelations",
			SeedIDs: []string{"54013ba69c196820e56801f1ef5aad54"},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("query topo graph-call seed: %v", err)
	}
	if len(seedTopoRows.Rows) != 1 || seedTopoRows.Rows[0]["src"] != "apm/apm.service/54013ba69c196820e56801f1ef5aad54" {
		t.Fatalf("expected graph-call seed to filter unrelated relations, got %+v", seedTopoRows.Rows)
	}
	cypherRows, err := provider.QueryTopo(ctx, model.TopoQueryPlan{
		Workspace: "demo",
		GraphCall: &model.GraphCallPlan{
			Name:   "cypher",
			Cypher: "MATCH (src:`apm@apm.service` {__entity_id__: $src})-[r:calls]->(dest) RETURN properties(src) AS src, properties(r) AS relation, properties(dest) AS dest",
		},
		Params: map[string]any{"src": "54013ba69c196820e56801f1ef5aad54"},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("query cypher topo properties: %v", err)
	}
	if len(cypherRows.Rows) != 1 {
		t.Fatalf("expected one cypher row, got %+v", cypherRows.Rows)
	}
	cypherRow := cypherRows.Rows[0]
	src, ok := cypherRow["src"].(map[string]any)
	if !ok || src["display_name"] != "cart service" {
		t.Fatalf("unexpected cypher source properties: %#v", cypherRow["src"])
	}
	relation, ok := cypherRow["relation"].(map[string]any)
	if !ok || relation["weight"] != "critical" || relation["__relation_type__"] != "calls" {
		t.Fatalf("unexpected cypher relation properties: %#v", cypherRow["relation"])
	}
	dest, ok := cypherRow["dest"].(map[string]any)
	if !ok || dest["display_name"] != "177627f91af678a9b03e993f1a91917f service" {
		t.Fatalf("unexpected cypher destination properties: %#v", cypherRow["dest"])
	}

	provider.Close()
	reopened, err := NewProvider(graphstore.ProviderConfig{DataRoot: dataRoot})
	if err != nil {
		t.Fatalf("new reopened provider: %v", err)
	}
	defer reopened.Close()
	if err := reopened.OpenWorkspace(ctx, model.WorkspaceMetadata{ID: "demo"}); err != nil {
		t.Fatalf("reopen workspace: %v", err)
	}
	reopenedSnapshot, err := reopened.GetUModelSnapshot(ctx, model.UModelSnapshotRequest{Workspace: "demo"})
	if err != nil {
		t.Fatalf("reopened snapshot: %v", err)
	}
	if len(reopenedSnapshot.Elements) != 1 || reopenedSnapshot.Elements[0].Domain != "apm" || reopenedSnapshot.Elements[0].Name != "apm.service" || reopenedSnapshot.Elements[0].Kind != "entity_set" {
		t.Fatalf("expected persisted umodel element after reopen, got %+v", reopenedSnapshot.Elements)
	}
	reopenedRows, err := reopened.QueryEntities(ctx, model.EntityQueryPlan{
		Workspace: "demo",
		Filters:   map[string]any{"domain": "apm", "name": "apm.*", "ids": []string{"54013ba69c196820e56801f1ef5aad54"}},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("reopened query entity: %v", err)
	}
	if len(reopenedRows.Rows) != 1 {
		t.Fatalf("expected persisted entity after reopen, got %+v", reopenedRows.Rows)
	}
}

// TestEntityPayloadFromRowReadsOldAndNewFormat verifies AC-002: the read
// path produces identical results whether the properties JSON still carries
// the system keys (pre-fix rows) or has already had them trimmed (post-fix
// rows), since the dedicated row columns always take precedence.
func TestEntityPayloadFromRowReadsOldAndNewFormat(t *testing.T) {
	base := map[string]any{
		"domain":              "apm",
		"entity_type":         "apm.service",
		"entity_id":           "54013ba69c196820e56801f1ef5aad54",
		"method":              "Update",
		"first_observed_time": int64(100),
		"last_observed_time":  int64(200),
		"keep_alive_seconds":  int64(60),
		"deleted":             false,
	}

	oldRow := cloneMap(base)
	oldRow["properties"] = `{"__domain__":"apm","__entity_type__":"apm.service","__entity_id__":"54013ba69c196820e56801f1ef5aad54","__method__":"Update","__first_observed_time__":100,"__last_observed_time__":200,"__keep_alive_seconds__":60,"__deleted__":false,"display_name":"cart service"}`

	newRow := cloneMap(base)
	newRow["properties"] = `{"display_name":"cart service"}`

	oldPayload := entityPayloadFromRow(oldRow)
	newPayload := entityPayloadFromRow(newRow)

	for _, key := range entitySystemKeys {
		if oldPayload[key] != newPayload[key] {
			t.Fatalf("expected %s to match across old/new format, old=%v new=%v", key, oldPayload[key], newPayload[key])
		}
	}
	if oldPayload["display_name"] != "cart service" || newPayload["display_name"] != "cart service" {
		t.Fatalf("expected business property preserved, old=%+v new=%+v", oldPayload, newPayload)
	}
}

// TestRelationPayloadFromRowReadsOldAndNewFormat is the relation analogue of
// TestEntityPayloadFromRowReadsOldAndNewFormat, covering AC-002 for topo rows.
func TestRelationPayloadFromRowReadsOldAndNewFormat(t *testing.T) {
	base := map[string]any{
		"src":                 "apm/apm.service/54013ba69c196820e56801f1ef5aad54",
		"dest":                "apm/apm.service/177627f91af678a9b03e993f1a91917f",
		"relation":            "calls",
		"method":              "Update",
		"first_observed_time": int64(100),
		"last_observed_time":  int64(200),
		"keep_alive_seconds":  int64(60),
		"deleted":             false,
	}

	oldRow := cloneMap(base)
	oldRow["properties"] = `{"__relation_type__":"calls","__method__":"Update","__first_observed_time__":100,"__last_observed_time__":200,"__keep_alive_seconds__":60,"__deleted__":false,"weight":"critical"}`

	newRow := cloneMap(base)
	newRow["properties"] = `{"weight":"critical"}`

	oldPayload := relationPayloadFromRow(oldRow)
	newPayload := relationPayloadFromRow(newRow)

	for _, key := range relationSystemKeys {
		if oldPayload[key] != newPayload[key] {
			t.Fatalf("expected %s to match across old/new format, old=%v new=%v", key, oldPayload[key], newPayload[key])
		}
	}
	if oldPayload["weight"] != "critical" || newPayload["weight"] != "critical" {
		t.Fatalf("expected business property preserved, old=%+v new=%+v", oldPayload, newPayload)
	}
}

// TestWriteEntitiesStripsSystemKeysFromProperties verifies AC-001: the
// properties column written by WriteEntities no longer contains the system
// fields that already have a dedicated column, while business properties
// and the round-tripped QueryEntities result (AC-003) are unaffected.
func TestWriteEntitiesStripsSystemKeysFromProperties(t *testing.T) {
	if os.Getenv("UMODEL_TEST_LADYBUG") != "1" {
		t.Skip("set UMODEL_TEST_LADYBUG=1 and provide liblbug to run local.ladybug conformance tests")
	}

	dataRoot := t.TempDir()
	provider, err := NewProvider(graphstore.ProviderConfig{DataRoot: dataRoot})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	if err := provider.OpenWorkspace(ctx, model.WorkspaceMetadata{ID: "demo"}); err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	id := "54013ba69c196820e56801f1ef5aad54"
	if _, err := provider.WriteEntities(ctx, model.EntityWriteBatch{
		Workspace: "demo",
		Entities:  []model.EntityPayload{entity(id)},
	}); err != nil {
		t.Fatalf("write entity: %v", err)
	}

	conn, err := provider.conn("demo")
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	rows, err := runRows(conn, `MATCH (e:entity {entity_key: $key}) RETURN e.properties AS properties;`, map[string]any{
		"key": "apm/apm.service/" + id,
	})
	if err != nil {
		t.Fatalf("query raw properties: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one entity row, got %d", len(rows))
	}
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(asString(rows[0]["properties"])), &raw); err != nil {
		t.Fatalf("unmarshal raw properties: %v", err)
	}
	for _, key := range entitySystemKeys {
		if _, ok := raw[key]; ok {
			t.Fatalf("expected properties to not contain system key %s, got %+v", key, raw)
		}
	}
	if raw["display_name"] != "cart service" {
		t.Fatalf("expected business property preserved in properties, got %+v", raw)
	}

	// AC-003: WriteEntities -> QueryEntities round trip stays correct after stripping.
	result, err := provider.QueryEntities(ctx, model.EntityQueryPlan{
		Workspace: "demo",
		Filters:   map[string]any{"ids": []string{id}},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("query entities: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["display_name"] != "cart service" || result.Rows[0]["__entity_id__"] != id {
		t.Fatalf("unexpected round-trip query result: %+v", result.Rows)
	}
}

// TestWriteRelationsStripsSystemKeysFromProperties verifies AC-004: relation
// properties no longer carry the relation-table system fields, while the
// endpoint fields (which have no dedicated column) and business properties
// remain so QueryTopo continues to resolve src/dest correctly.
func TestWriteRelationsStripsSystemKeysFromProperties(t *testing.T) {
	if os.Getenv("UMODEL_TEST_LADYBUG") != "1" {
		t.Skip("set UMODEL_TEST_LADYBUG=1 and provide liblbug to run local.ladybug conformance tests")
	}

	dataRoot := t.TempDir()
	provider, err := NewProvider(graphstore.ProviderConfig{DataRoot: dataRoot})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	if err := provider.OpenWorkspace(ctx, model.WorkspaceMetadata{ID: "demo"}); err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	src, dest := "54013ba69c196820e56801f1ef5aad54", "177627f91af678a9b03e993f1a91917f"
	if _, err := provider.WriteEntities(ctx, model.EntityWriteBatch{
		Workspace: "demo",
		Entities:  []model.EntityPayload{entity(src), entity(dest)},
	}); err != nil {
		t.Fatalf("write entity: %v", err)
	}
	if _, err := provider.WriteRelations(ctx, model.RelationWriteBatch{
		Workspace: "demo",
		Relations: []model.RelationPayload{relation(src, dest)},
	}); err != nil {
		t.Fatalf("write relation: %v", err)
	}

	conn, err := provider.conn("demo")
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	rows, err := runRows(conn, `MATCH (:entity)-[r:topo]->(:entity) RETURN r.properties AS properties;`, nil)
	if err != nil {
		t.Fatalf("query raw properties: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one relation row, got %d", len(rows))
	}
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(asString(rows[0]["properties"])), &raw); err != nil {
		t.Fatalf("unmarshal raw properties: %v", err)
	}
	for _, key := range relationSystemKeys {
		if _, ok := raw[key]; ok {
			t.Fatalf("expected properties to not contain system key %s, got %+v", key, raw)
		}
	}
	if raw["weight"] != "critical" {
		t.Fatalf("expected business property preserved in properties, got %+v", raw)
	}
	if raw["__src_entity_id__"] != src || raw["__dest_entity_id__"] != dest {
		t.Fatalf("expected endpoint fields preserved in properties (no dedicated column), got %+v", raw)
	}

	result, err := provider.QueryTopo(ctx, model.TopoQueryPlan{Workspace: "demo", Limit: 10})
	if err != nil {
		t.Fatalf("query topo: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["src"] != "apm/apm.service/"+src || result.Rows[0]["dest"] != "apm/apm.service/"+dest {
		t.Fatalf("unexpected round-trip topo result: %+v", result.Rows)
	}
}

func entity(id string) model.EntityPayload {
	displayName := id + " service"
	if id == "54013ba69c196820e56801f1ef5aad54" {
		displayName = "cart service"
	}
	return model.EntityPayload{
		"__domain__":              "apm",
		"__entity_type__":         "apm.service",
		"__entity_id__":           id,
		"__method__":              "Update",
		"__first_observed_time__": int64(100),
		"__last_observed_time__":  int64(200),
		"__keep_alive_seconds__":  int64(60),
		"display_name":            displayName,
	}
}

func relation(src, dest string) model.RelationPayload {
	return model.RelationPayload{
		"__src_domain__":          "apm",
		"__src_entity_type__":     "apm.service",
		"__src_entity_id__":       src,
		"__dest_domain__":         "apm",
		"__dest_entity_type__":    "apm.service",
		"__dest_entity_id__":      dest,
		"__relation_type__":       "calls",
		"__method__":              "Update",
		"__first_observed_time__": int64(100),
		"__last_observed_time__":  int64(200),
		"__keep_alive_seconds__":  int64(60),
		"weight":                  "critical",
	}
}
