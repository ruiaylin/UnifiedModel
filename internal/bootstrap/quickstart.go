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
	}

	if err := a.GraphStore.OpenWorkspace(ctx, metadata); err != nil {
		return model.SampleImportResult{}, err
	}

	return a.Samples.Import(ctx, workspaceID, sample)
}
