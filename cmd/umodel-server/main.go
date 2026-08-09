package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/alibaba/UnifiedModel/internal/bootstrap"
	"github.com/alibaba/UnifiedModel/internal/graphstore"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dataRoot := flag.String("data", "data", "UModel data root")
	provider := flag.String("graphstore", graphstore.DefaultProviderType, "GraphStore provider: local.ladybug, memory, or file.memory")
	uiDir := flag.String("ui-dir", "", "Optional directory containing built UModel web UI assets")
	quickStart := flag.Bool("quickstart", false, "Create a demo workspace and import bundled quickstart data before serving")
	quickStartWorkspace := flag.String("quickstart-workspace", bootstrap.DefaultQuickStartWorkspaceID, "Workspace id used by --quickstart")
	quickStartSample := flag.String("quickstart-sample", bootstrap.DefaultQuickStartSample, "Sample package imported by --quickstart")
	dsn := flag.String("dsn", os.Getenv("UMODEL_DSN"), "Data Source Name for providers that require it (e.g. postgres.age). Defaults to UMODEL_DSN env var.")
	graphPrefix := flag.String("graph-prefix", os.Getenv("UMODEL_GRAPH_PREFIX"), "Prefix for graph names (e.g. for postgres.age). Defaults to UMODEL_GRAPH_PREFIX env var.")
	flag.Parse()

	graphstoreExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "graphstore" {
			graphstoreExplicit = true
		}
	})
	*provider = resolveProviderForQuickStart(*provider, *quickStart, graphstoreExplicit)

	ctx := context.Background()
	config := graphstore.ProviderConfig{
		Type:     *provider,
		DataRoot: *dataRoot,
		Options:  make(map[string]string),
	}
	if *dsn != "" {
		config.Options["dsn"] = *dsn
	}
	if *graphPrefix != "" {
		config.Options["graph_prefix"] = *graphPrefix
	}

	app, err := bootstrap.NewAppWithGraphStore(*dataRoot, config)
	if err != nil {
		log.Fatal(err)
	}
	if health, err := app.GraphStore.Health(ctx); err != nil {
		log.Fatal(err)
	} else if health.Status == "unavailable" {
		log.Fatalf("graphstore provider %s unavailable: %s", health.Provider, health.Message)
	}
	if *quickStart {
		result, err := app.LoadQuickStart(ctx, bootstrap.QuickStartOptions{
			WorkspaceID: *quickStartWorkspace,
			Sample:      *quickStartSample,
		})
		if err != nil {
			log.Fatalf("quickstart import failed: %v", err)
		}
		log.Printf("quickstart loaded workspace=%s sample=%s umodel_imported=%d umodel_skipped=%d entities=%d relations=%d",
			result.Workspace,
			result.Sample,
			result.UModel.Imported,
			result.UModel.Skipped,
			result.EntityCount,
			result.RelationCount,
		)
	}
	log.Printf("umodel-server listening on %s", *addr)
	if *uiDir != "" {
		log.Printf("serving web UI from %s", *uiDir)
	}
	if err := http.ListenAndServe(*addr, app.HandlerWithUI(*uiDir)); err != nil {
		log.Fatal(err)
	}
}

func resolveProviderForQuickStart(provider string, quickStart bool, graphstoreExplicit bool) string {
	if quickStart && !graphstoreExplicit {
		return graphstore.ProviderTypeMemory
	}
	return provider
}
