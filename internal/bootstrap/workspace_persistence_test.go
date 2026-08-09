package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alibaba/UnifiedModel/internal/graphstore"
	"github.com/alibaba/UnifiedModel/pkg/model"
)

func TestFileMemoryAppPersistsWorkspaceMetadata(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	first := NewFileMemoryApp(root)
	if _, err := first.Workspace.CreateWorkspace(ctx, model.CreateWorkspaceRequest{ID: "demo", Name: "Demo"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	second := NewFileMemoryApp(root)
	page, err := second.Workspace.ListWorkspaces(ctx, model.WorkspaceListRequest{})
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "demo" || page.Items[0].Name != "Demo" {
		t.Fatalf("expected persisted demo workspace, got %+v", page.Items)
	}
}

func TestLadybugAppPersistsWorkspaceMetadata(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	first, err := NewAppWithGraphStore(root, graphstore.ProviderConfig{Type: graphstore.ProviderTypeLadybug, DataRoot: root})
	if err != nil {
		t.Fatalf("new ladybug app: %v", err)
	}
	if _, err := first.Workspace.CreateWorkspace(ctx, model.CreateWorkspaceRequest{ID: "demo", Name: "Demo"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	second, err := NewAppWithGraphStore(root, graphstore.ProviderConfig{Type: graphstore.ProviderTypeLadybug, DataRoot: root})
	if err != nil {
		t.Fatalf("reopen ladybug app: %v", err)
	}
	page, err := second.Workspace.ListWorkspaces(ctx, model.WorkspaceListRequest{})
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "demo" || page.Items[0].Name != "Demo" {
		t.Fatalf("expected persisted demo workspace, got %+v", page.Items)
	}
}

func TestWorkspaceMetadataPersistenceProviderSelection(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		want         bool
	}{
		{name: "memory", providerType: graphstore.ProviderTypeMemory, want: false},
		{name: "file memory", providerType: graphstore.ProviderTypeFileMemory, want: true},
		{name: "ladybug", providerType: graphstore.ProviderTypeLadybug, want: true},
		{name: "postgres age", providerType: graphstore.ProviderTypePostgresAge, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usesPersistentWorkspaceMetadata(tt.providerType); got != tt.want {
				t.Fatalf("usesPersistentWorkspaceMetadata(%q) = %t, want %t", tt.providerType, got, tt.want)
			}
		})
	}
}

func TestLadybugAppRecoversWorkspaceMetadataFromDataRoot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "instances", "demo", "storage", "graph", "local", "ladybug"), 0o755); err != nil {
		t.Fatalf("create ladybug workspace dir: %v", err)
	}

	app, err := NewAppWithGraphStore(root, graphstore.ProviderConfig{Type: graphstore.ProviderTypeLadybug, DataRoot: root})
	if err != nil {
		t.Fatalf("new ladybug app: %v", err)
	}
	page, err := app.Workspace.ListWorkspaces(ctx, model.WorkspaceListRequest{})
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "demo" || page.Items[0].Name != "demo" {
		t.Fatalf("expected recovered demo workspace, got %+v", page.Items)
	}
}
