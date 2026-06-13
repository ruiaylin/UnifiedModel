package entitystore

import (
	"context"
	"testing"

	"github.com/alibaba/UnifiedModel/internal/graphstore"
	"github.com/alibaba/UnifiedModel/internal/umodel"
	apperrors "github.com/alibaba/UnifiedModel/pkg/errors"
	"github.com/alibaba/UnifiedModel/pkg/model"
)

func newTestServiceWithUModel() (*Service, *graphstore.MemoryStore) {
	graph := graphstore.NewMemoryStore()
	svc := NewService(graph, umodel.NewService(graph), WithUModelStore(graph))
	return svc, graph
}

func seedEntitySet(t *testing.T, graph *graphstore.MemoryStore, workspace, domain, name string) {
	t.Helper()
	_, err := graph.PutUModelElements(context.Background(), model.UModelElementBatch{
		Workspace: workspace,
		Elements: []model.UModelElement{{
			Kind:   "entity_set",
			Domain: domain,
			Name:   name,
			Spec: map[string]any{
				"fields": map[string]any{
					"id":          "string",
					"environment": "string",
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("seed entity_set %s/%s: %v", domain, name, err)
	}
}

func seedMetricSet(t *testing.T, graph *graphstore.MemoryStore, workspace, domain, name string) {
	t.Helper()
	_, err := graph.PutUModelElements(context.Background(), model.UModelElementBatch{
		Workspace: workspace,
		Elements: []model.UModelElement{{
			Kind:   "metric_set",
			Domain: domain,
			Name:   name,
			Spec: map[string]any{
				"labels": map[string]any{
					"service_id":   "string",
					"environment":  "string",
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("seed metric_set %s/%s: %v", domain, name, err)
	}
}

func seedStorageLink(t *testing.T, graph *graphstore.MemoryStore, workspace, srcDomain, srcName, destDomain, destName string) {
	t.Helper()
	_, err := graph.PutUModelElements(context.Background(), model.UModelElementBatch{
		Workspace: workspace,
		Elements: []model.UModelElement{{
			Kind:   "storage_link",
			Domain: srcDomain,
			Name:   srcName + "_to_" + destDomain + "." + destName,
			Spec: map[string]any{
				"src":  map[string]any{"domain": srcDomain, "kind": "metric_set", "name": srcName},
				"dest": map[string]any{"domain": destDomain, "kind": "prometheus", "name": destName},
			},
		}},
	})
	if err != nil {
		t.Fatalf("seed storage_link: %v", err)
	}
}

func dataLinkInput(domain, name, srcDomain, srcName, destDomain, destName, destKind string) DataLinkInput {
	return DataLinkInput{
		Kind: "data_link",
		Metadata: struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		}{Name: name, Domain: domain},
		Spec: map[string]any{
			"src": map[string]any{
				"domain": srcDomain,
				"kind":   "entity_set",
				"name":   srcName,
			},
			"dest": map[string]any{
				"domain": destDomain,
				"kind":   destKind,
				"name":   destName,
			},
			"fields_mapping": map[string]any{
				"id":          "service_id",
				"environment": "environment",
			},
			"data_link_type": "related_to",
		},
	}
}

func TestWriteAndGetDataLinks(t *testing.T) {
	ctx := context.Background()
	svc, graph := newTestServiceWithUModel()
	ws := "demo"

	// Seed referenced entities.
	seedEntitySet(t, graph, ws, "apm", "apm.service")
	seedMetricSet(t, graph, ws, "apm", "apm.metric.service")

	dl := dataLinkInput("apm", "apm.service_related_to_apm.metric.service",
		"apm", "apm.service", "apm", "apm.metric.service", "metric_set")

	writeResp, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{
		DataLinks: []DataLinkInput{dl},
	})
	if err != nil {
		t.Fatalf("write datalink: %v", err)
	}
	if writeResp.Status != "ok" {
		t.Fatalf("expected status ok, got %s", writeResp.Status)
	}
	if len(writeResp.Results) != 1 || writeResp.Results[0].Status != "created" {
		t.Fatalf("expected created result, got %+v", writeResp.Results)
	}

	// Query back.
	queryResp, err := svc.GetDataLinks(ctx, ws, DataLinkQueryRequest{})
	if err != nil {
		t.Fatalf("get datalinks: %v", err)
	}
	if queryResp.Total != 1 {
		t.Fatalf("expected 1 datalink, got %d", queryResp.Total)
	}
	dlResult := queryResp.DataLinks[0]
	if dlResult.Domain != "apm" || dlResult.Name != "apm.service_related_to_apm.metric.service" {
		t.Fatalf("unexpected datalink: %+v", dlResult)
	}
	if dlResult.LinkedDataset == nil || dlResult.LinkedDataset.Kind != "metric_set" {
		t.Fatalf("expected linked metric_set dataset, got %+v", dlResult.LinkedDataset)
	}
	if dlResult.LinkedDataset.QueryType != "prom" {
		t.Fatalf("expected query_type 'prom', got %s", dlResult.LinkedDataset.QueryType)
	}
}

func TestWriteDataLinksIdempotency(t *testing.T) {
	ctx := context.Background()
	svc, graph := newTestServiceWithUModel()
	ws := "demo"

	seedEntitySet(t, graph, ws, "apm", "apm.service")
	seedMetricSet(t, graph, ws, "apm", "apm.metric.service")

	dl := dataLinkInput("apm", "apm.service_related_to_apm.metric.service",
		"apm", "apm.service", "apm", "apm.metric.service", "metric_set")

	req := DataLinkWriteRequest{
		DataLinks:      []DataLinkInput{dl},
		IdempotencyKey: "test-idem-key-001",
	}

	first, err := svc.WriteDataLinks(ctx, ws, req)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	second, err := svc.WriteDataLinks(ctx, ws, req)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	if len(first.Results) != 1 || first.Results[0].Status != "created" {
		t.Fatalf("first write result: %+v", first.Results)
	}
	if len(second.Results) != 1 || second.Results[0].Status != "created" {
		t.Fatalf("second (cached) write result: %+v", second.Results)
	}

	// Verify no duplicate entries.
	queryResp, err := svc.GetDataLinks(ctx, ws, DataLinkQueryRequest{})
	if err != nil {
		t.Fatalf("get datalinks: %v", err)
	}
	if queryResp.Total != 1 {
		t.Fatalf("expected 1 datalink after idempotent writes, got %d", queryResp.Total)
	}
}

func TestWriteDataLinksInvalidSrcKind(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestServiceWithUModel()
	ws := "demo"

	dl := DataLinkInput{
		Kind: "data_link",
		Metadata: struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		}{Name: "bad-src-kind", Domain: "apm"},
		Spec: map[string]any{
			"src":  map[string]any{"domain": "apm", "kind": "host", "name": "apm.host"},
			"dest": map[string]any{"domain": "apm", "kind": "metric_set", "name": "apm.metric"},
		},
	}

	resp, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if resp.Results[0].Status != "error" {
		t.Fatalf("expected error for bad src.kind, got %+v", resp.Results[0])
	}
}

func TestWriteDataLinksInvalidDestKind(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestServiceWithUModel()
	ws := "demo"

	dl := DataLinkInput{
		Kind: "data_link",
		Metadata: struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		}{Name: "bad-dest-kind", Domain: "apm"},
		Spec: map[string]any{
			"src":  map[string]any{"domain": "apm", "kind": "entity_set", "name": "apm.service"},
			"dest": map[string]any{"domain": "apm", "kind": "database", "name": "apm.db"},
		},
	}

	resp, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if resp.Results[0].Status != "error" {
		t.Fatalf("expected error for bad dest.kind, got %+v", resp.Results[0])
	}
}

func TestWriteDataLinksWarningForMissingSrcDest(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestServiceWithUModel()
	ws := "demo"

	// No entity_set or metric_set seeded — should produce warnings but still write.
	dl := dataLinkInput("apm", "apm.service_related_to_apm.metric.service",
		"apm", "apm.service", "apm", "apm.metric.service", "metric_set")

	resp, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if resp.Results[0].Status != "created" {
		t.Fatalf("expected created despite warnings, got %+v", resp.Results[0])
	}
	if len(resp.Warnings) < 2 {
		t.Fatalf("expected at least 2 warnings (missing src + missing dest), got %d: %+v", len(resp.Warnings), resp.Warnings)
	}
}

func TestWriteDataLinksWarningForFieldMapping(t *testing.T) {
	ctx := context.Background()
	svc, graph := newTestServiceWithUModel()
	ws := "demo"

	// Seed entity_set with limited fields.
	_, err := graph.PutUModelElements(ctx, model.UModelElementBatch{
		Workspace: ws,
		Elements: []model.UModelElement{{
			Kind:   "entity_set",
			Domain: "apm",
			Name:   "apm.service",
			Spec: map[string]any{
				"fields": map[string]any{"id": "string"},
				// No "environment" field.
			},
		}},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedMetricSet(t, graph, ws, "apm", "apm.metric.service")

	dl := dataLinkInput("apm", "apm.service_related_to_apm.metric.service",
		"apm", "apm.service", "apm", "apm.metric.service", "metric_set")

	resp, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if resp.Results[0].Status != "created" {
		t.Fatalf("expected created, got %+v", resp.Results[0])
	}
	// Should warn about "environment" not found in entity_set fields.
	found := false
	for _, w := range resp.Warnings {
		if w.Field == "data_links[0].fields_mapping" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected field_mapping warning, got warnings: %+v", resp.Warnings)
	}
}

func TestGetDataLinksFilterByDestKind(t *testing.T) {
	ctx := context.Background()
	svc, graph := newTestServiceWithUModel()
	ws := "demo"

	seedEntitySet(t, graph, ws, "apm", "apm.service")
	seedMetricSet(t, graph, ws, "apm", "apm.metric.service")

	// Write a metric_set link.
	dl := dataLinkInput("apm", "apm.service_related_to_apm.metric.service",
		"apm", "apm.service", "apm", "apm.metric.service", "metric_set")
	_, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Write a log_set link.
	dlLog := DataLinkInput{
		Kind: "data_link",
		Metadata: struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		}{Name: "apm.service_related_to_apm.log.service", Domain: "apm"},
		Spec: map[string]any{
			"src":           map[string]any{"domain": "apm", "kind": "entity_set", "name": "apm.service"},
			"dest":          map[string]any{"domain": "apm", "kind": "log_set", "name": "apm.log.service"},
			"data_link_type": "related_to",
		},
	}
	_, err = svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dlLog}})
	if err != nil {
		t.Fatalf("write log link: %v", err)
	}

	// Filter by dest_kind=metric_set.
	resp, err := svc.GetDataLinks(ctx, ws, DataLinkQueryRequest{
		Filter: DataLinkQuery{DestKind: "metric_set"},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 metric_set link, got %d", resp.Total)
	}
	if resp.DataLinks[0].LinkedDataset.Kind != "metric_set" {
		t.Fatalf("expected metric_set, got %s", resp.DataLinks[0].LinkedDataset.Kind)
	}

	// Filter by dest_kind=log_set.
	resp, err = svc.GetDataLinks(ctx, ws, DataLinkQueryRequest{
		Filter: DataLinkQuery{DestKind: "log_set"},
	})
	if err != nil {
		t.Fatalf("query log: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 log_set link, got %d", resp.Total)
	}

	// No filter — both returned.
	resp, err = svc.GetDataLinks(ctx, ws, DataLinkQueryRequest{})
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("expected 2 total links, got %d", resp.Total)
	}
}

func TestWriteDataLinksMissingSpec(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestServiceWithUModel()
	ws := "demo"

	dl := DataLinkInput{
		Kind: "data_link",
		Metadata: struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		}{Name: "no-spec", Domain: "apm"},
		Spec: nil,
	}

	resp, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if resp.Results[0].Status != "error" {
		t.Fatalf("expected error for missing spec, got %+v", resp.Results[0])
	}
}

func TestWriteDataLinksNotImplementedWithoutUModelStore(t *testing.T) {
	ctx := context.Background()
	graph := graphstore.NewMemoryStore()
	svc := NewService(graph, umodel.NewService(graph)) // no WithUModelStore

	dl := dataLinkInput("apm", "test", "apm", "apm.service", "apm", "apm.metric", "metric_set")
	_, err := svc.WriteDataLinks(ctx, "demo", DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if !apperrors.IsCode(err, apperrors.CodeNotImplemented) {
		t.Fatalf("expected NotImplemented, got %v", err)
	}
}

func TestGetDataLinksLinkedStorage(t *testing.T) {
	ctx := context.Background()
	svc, graph := newTestServiceWithUModel()
	ws := "demo"

	seedEntitySet(t, graph, ws, "apm", "apm.service")
	seedMetricSet(t, graph, ws, "apm", "apm.metric.service")
	seedStorageLink(t, graph, ws, "apm", "apm.metric.service", "apm", "apm.prometheus.core")

	dl := dataLinkInput("apm", "apm.service_related_to_apm.metric.service",
		"apm", "apm.service", "apm", "apm.metric.service", "metric_set")
	_, err := svc.WriteDataLinks(ctx, ws, DataLinkWriteRequest{DataLinks: []DataLinkInput{dl}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	resp, err := svc.GetDataLinks(ctx, ws, DataLinkQueryRequest{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1, got %d", resp.Total)
	}
	linked := resp.DataLinks[0].LinkedStorage
	if linked == nil {
		t.Fatal("expected linked storage, got nil")
	}
	if linked.Kind != "prometheus" || linked.Name != "apm.prometheus.core" {
		t.Fatalf("unexpected linked storage: %+v", linked)
	}
}
