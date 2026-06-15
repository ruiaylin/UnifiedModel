package bootstrap

import (
	"context"
	"strings"

	"github.com/alibaba/UnifiedModel/internal/sampledata"
	apperrors "github.com/alibaba/UnifiedModel/pkg/errors"
	"github.com/alibaba/UnifiedModel/pkg/model"
)

const (
	DefaultQuickStartWorkspaceID          = "demo"
	DefaultQuickStartWorkspaceName        = "Demo"
	DefaultQuickStartWorkspaceDescription = "Multi-domain quickstart demo"
	DefaultQuickStartSample               = sampledata.MultiDomainQuickStartSample
)

type QuickStartOptions struct {
	WorkspaceID          string
	WorkspaceName        string
	WorkspaceDescription string
	Sample               string
}

func (a *App) LoadQuickStart(ctx context.Context, opts QuickStartOptions) (model.SampleImportResult, error) {
	workspaceID := strings.TrimSpace(opts.WorkspaceID)
	if workspaceID == "" {
		workspaceID = DefaultQuickStartWorkspaceID
	}
	workspaceName := strings.TrimSpace(opts.WorkspaceName)
	if workspaceName == "" {
		workspaceName = DefaultQuickStartWorkspaceName
	}
	workspaceDescription := strings.TrimSpace(opts.WorkspaceDescription)
	if workspaceDescription == "" {
		workspaceDescription = DefaultQuickStartWorkspaceDescription
	}
	sample := strings.TrimSpace(opts.Sample)
	if sample == "" {
		sample = DefaultQuickStartSample
	}

	var metadata model.WorkspaceMetadata
	var err error
	if metadata, err = a.Workspace.GetWorkspace(ctx, workspaceID); err != nil {
		if !apperrors.IsCode(err, apperrors.CodeNotFound) {
			return model.SampleImportResult{}, err
		}
		if metadata, err = a.Workspace.CreateWorkspace(ctx, model.CreateWorkspaceRequest{
			ID:          workspaceID,
			Name:        workspaceName,
			Description: workspaceDescription,
			Labels: map[string]string{
				"umodel.io/quickstart": "true",
			},
		}); err != nil {
			if !apperrors.IsCode(err, apperrors.CodeConflict) {
				return model.SampleImportResult{}, err
			}
			// If conflict, get the existing one
			metadata, err = a.Workspace.GetWorkspace(ctx, workspaceID)
			if err != nil {
				return model.SampleImportResult{}, err
			}
		}
	} else if metadata.Labels["umodel.io/quickstart"] != "true" {
		// The workspace already exists but is missing the quickstart label. This
		// happens on graphstore backends (postgres.age, ladybug) where a graph
		// space pre-exists and is recovered into bare metadata (ID+Name only) at
		// startup, before LoadQuickStart runs. Without this, the label that the
		// create-path sets above would never appear, leaving the quickstart demo
		// workspace's labels out of parity with the in-memory backend. Merge the
		// label idempotently so list/detail responses match across providers.
		if metadata, err = a.Workspace.UpdateWorkspace(ctx, workspaceID, model.UpdateWorkspaceRequest{
			Labels: map[string]string{
				"umodel.io/quickstart": "true",
			},
		}); err != nil {
			return model.SampleImportResult{}, err
		}
	}

	if err := a.GraphStore.OpenWorkspace(ctx, metadata); err != nil {
		return model.SampleImportResult{}, err
	}

	return a.Samples.Import(ctx, workspaceID, sample)
}
