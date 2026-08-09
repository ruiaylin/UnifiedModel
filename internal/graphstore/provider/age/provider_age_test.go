//go:build age

package age

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alibaba/UnifiedModel/internal/graphstore"
	"github.com/alibaba/UnifiedModel/pkg/model"
)

// Integration tests - require UMODEL_TEST_AGE_DSN environment variable
// Example: UMODEL_TEST_AGE_DSN="postgres://user:pass@localhost:5432/dbname"

func skipIfNoDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("UMODEL_TEST_AGE_DSN")
	if dsn == "" {
		t.Skip("UMODEL_TEST_AGE_DSN not set, skipping integration test")
	}
	return dsn
}

func newTestProvider(t *testing.T, dsn string) *Provider {
	t.Helper()
	p, err := NewProvider(graphstore.ProviderConfig{
		Type: graphstore.ProviderTypePostgresAge,
		Options: map[string]string{
			"dsn": dsn,
		},
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return p
}

func TestProviderOpenWorkspace(t *testing.T) {
	dsn := skipIfNoDSN(t)
	p := newTestProvider(t, dsn)
	defer p.Close()

	ctx := context.Background()
	ws := model.WorkspaceMetadata{ID: "test-open-" + time.Now().Format("20060102150405")}

	// First open should succeed
	if err := p.OpenWorkspace(ctx, ws); err != nil {
		t.Fatalf("first open: %v", err)
	}

	// Second open should be idempotent
	if err := p.OpenWorkspace(ctx, ws); err != nil {
		t.Fatalf("second open (idempotent): %v", err)
	}
}

func TestProviderPutAndGetUModelElements(t *testing.T) {
	dsn := skipIfNoDSN(t)
	p := newTestProvider(t, dsn)
	defer p.Close()

	ctx := context.Background()
	ws := model.WorkspaceMetadata{ID: "test-umodel-" + time.Now().Format("20060102150405")}
	if err := p.OpenWorkspace(ctx, ws); err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	// Put elements
	batch := model.UModelElementBatch{
		Workspace: ws.ID,
		Elements: []model.UModelElement{
			{Kind: "entity_set", Domain: "test", Name: "host", Version: "v1", Spec: map[string]any{"key": "value"}},
		},
	}
	result, err := p.PutUModelElements(ctx, batch)
	if err != nil {
		t.Fatalf("put elements: %v", err)
	}
	if result.Accepted != 1 {
		t.Fatalf("expected 1 accepted, got %d", result.Accepted)
	}

	// Get snapshot
	snapshot, err := p.GetUModelSnapshot(ctx, model.UModelSnapshotRequest{Workspace: ws.ID})
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(snapshot.Elements))
	}
	if snapshot.Elements[0].Name != "host" {
		t.Fatalf("expected name 'host', got %q", snapshot.Elements[0].Name)
	}
}

func TestProviderWriteAndQueryEntities(t *testing.T) {
	dsn := skipIfNoDSN(t)
	p := newTestProvider(t, dsn)
	defer p.Close()

	ctx := context.Background()
	ws := model.WorkspaceMetadata{ID: "test-entity-" + time.Now().Format("20060102150405")}
	if err := p.OpenWorkspace(ctx, ws); err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	// Write entity
	batch := model.EntityWriteBatch{
		Workspace: ws.ID,
		Entities: []model.EntityPayload{
			{
				"__domain__":             "infra",
				"__entity_type__":        "host",
				"__entity_id__":          "host-001",
				"__method__":             "Update",
				"__first_observed_time__": time.Now().Unix(),
				"__last_observed_time__":  time.Now().Unix(),
				"__keep_alive_seconds__":  3600,
				"display_name":            "test host",
			},
		},
	}
	result, err := p.WriteEntities(ctx, batch)
	if err != nil {
		t.Fatalf("write entities: %v", err)
	}
	if result.Accepted != 1 {
		t.Fatalf("expected 1 accepted, got %d", result.Accepted)
	}

	// Query entities
	query := model.EntityQueryPlan{
		Workspace: ws.ID,
		Filters:   map[string]any{"domain": "infra"},
	}
	qr, err := p.QueryEntities(ctx, query)
	if err != nil {
		t.Fatalf("query entities: %v", err)
	}
	if len(qr.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(qr.Rows))
	}
	if got := qr.Rows[0]["display_name"]; got != "test host" {
		t.Fatalf("expected display_name to round-trip through AGE properties, got %v", got)
	}
}
