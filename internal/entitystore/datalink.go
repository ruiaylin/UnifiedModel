package entitystore

import (
	"context"
	"fmt"

	apperrors "github.com/alibaba/UnifiedModel/pkg/errors"
	"github.com/alibaba/UnifiedModel/pkg/model"
)

// Valid destination kinds for DataLink telemetry types.
var validDestKinds = map[string]bool{
	"metric_set": true,
	"log_set":    true,
	"trace_set":  true,
	"event_set":  true,
}

// DataLinkWriteRequest is the request body for data_links:write.
// DataLinkInput uses a metadata envelope (name/domain) to match the REST
// contract; WriteDataLinks converts to flat UModelElement before storage.
type DataLinkWriteRequest struct {
	Workspace      string          `json:"workspace,omitempty"`
	DataLinks      []DataLinkInput `json:"data_links"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

// DataLinkInput is a single DataLink in the write request, matching the
// contract's JSON shape with a metadata envelope.
type DataLinkInput struct {
	Kind     string         `json:"kind"`
	Schema   map[string]any `json:"schema,omitempty"`
	Metadata struct {
		Name   string `json:"name"`
		Domain string `json:"domain"`
	} `json:"metadata"`
	Spec map[string]any `json:"spec,omitempty"`
}

// DataLinkWriteResponse is the response body for data_links:write.
type DataLinkWriteResponse struct {
	Status         string                `json:"status"`
	PartialSuccess bool                  `json:"partial_success"`
	Results        []DataLinkWriteResult `json:"results"`
	Warnings       []model.ErrorDetail   `json:"warnings,omitempty"`
}

// DataLinkWriteResult describes the outcome of a single DataLink write.
type DataLinkWriteResult struct {
	Index  int    `json:"index"`
	Status string `json:"status"` // created | updated | error
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Error  string `json:"error,omitempty"`
}

// DataLinkQueryRequest is the request body for data_links:get.
type DataLinkQueryRequest struct {
	Workspace string          `json:"workspace,omitempty"`
	Filter    DataLinkQuery   `json:"filter"`
}

// DataLinkQuery holds optional filter criteria for querying DataLinks.
type DataLinkQuery struct {
	SrcDomain    string `json:"src_domain,omitempty"`
	SrcKind      string `json:"src_kind,omitempty"`
	SrcName      string `json:"src_name,omitempty"`
	DestKind     string `json:"dest_kind,omitempty"`
	DestDomain   string `json:"dest_domain,omitempty"`
	DestName     string `json:"dest_name,omitempty"`
	DataLinkType string `json:"data_link_type,omitempty"`
}

// DataLinkQueryResponse is the response body for data_links:get.
type DataLinkQueryResponse struct {
	DataLinks []DataLinkWithDetails `json:"data_links"`
	Total     int                   `json:"total"`
}

// DataLinkWithDetails is a DataLink element enriched with linked dataset/storage info.
type DataLinkWithDetails struct {
	Kind          string           `json:"kind"`
	Domain        string           `json:"domain"`
	Name          string           `json:"name"`
	Version       string           `json:"version,omitempty"`
	Spec          map[string]any   `json:"spec,omitempty"`
	LinkedDataset *DatasetSummary  `json:"linked_dataset,omitempty"`
	LinkedStorage *StorageSummary  `json:"linked_storage,omitempty"`
}

// DatasetSummary is a compact reference to a linked telemetry dataset.
type DatasetSummary struct {
	Kind      string `json:"kind"`
	Domain    string `json:"domain"`
	Name      string `json:"name"`
	QueryType string `json:"query_type,omitempty"`
}

// StorageSummary is a compact reference to a linked storage backend.
type StorageSummary struct {
	Kind     string `json:"kind"`
	Domain   string `json:"domain"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint,omitempty"`
}

// WriteDataLinks writes DataLink UModelElements into the graph store.
// Validates structural constraints (src.kind, dest.kind) and emits warnings
// for non-blocking issues (missing src/dest, field mapping mismatches).
func (s *Service) WriteDataLinks(ctx context.Context, workspace string, req DataLinkWriteRequest) (DataLinkWriteResponse, error) {
	if s.umodel == nil {
		return DataLinkWriteResponse{}, apperrors.New(apperrors.CodeNotImplemented, "umodel store not configured")
	}
	if workspace == "" {
		return DataLinkWriteResponse{}, apperrors.New(apperrors.CodeInvalidArgument, "workspace is required")
	}
	if len(req.DataLinks) == 0 {
		return DataLinkWriteResponse{}, apperrors.New(apperrors.CodeInvalidArgument, "data_links is required")
	}

	// Check idempotency cache.
	if req.IdempotencyKey != "" {
		s.mu.Lock()
		cached, ok := s.datalinkIdempotency[idempotencyKey(workspace, req.IdempotencyKey)]
		s.mu.Unlock()
		if ok {
			return cached, nil
		}
	}

	// Load existing UModel snapshot for cross-reference validation.
	snapshot, err := s.umodel.GetUModelSnapshot(ctx, model.UModelSnapshotRequest{Workspace: workspace})
	if err != nil {
		return DataLinkWriteResponse{}, fmt.Errorf("get umodel snapshot: %w", err)
	}
	elementsByName := indexUModelElements(snapshot.Elements)

	var warnings []model.ErrorDetail
	validElements := make([]model.UModelElement, 0, len(req.DataLinks))
	results := make([]DataLinkWriteResult, len(req.DataLinks))

	for i, input := range req.DataLinks {
		name := input.Metadata.Name
		domain := input.Metadata.Domain
		results[i] = DataLinkWriteResult{
			Index:  i,
			Name:   name,
			Domain: domain,
		}

		// Convert DataLinkInput to UModelElement for storage.
		dl := model.UModelElement{
			Kind:   "data_link",
			Domain: domain,
			Name:   name,
			Spec:   input.Spec,
		}

		// Structural validation: require src and dest.
		spec := dl.Spec
		if spec == nil {
			results[i].Status = "error"
			results[i].Error = "spec is required"
			continue
		}
		src, _ := spec["src"].(map[string]any)
		dest, _ := spec["dest"].(map[string]any)
		if src == nil || dest == nil {
			results[i].Status = "error"
			results[i].Error = "spec.src and spec.dest are required"
			continue
		}

		// Validate src.kind == entity_set.
		srcKind, _ := src["kind"].(string)
		if srcKind != "entity_set" {
			results[i].Status = "error"
			results[i].Error = fmt.Sprintf("src.kind must be 'entity_set', got '%s'", srcKind)
			continue
		}

		// Validate dest.kind is a supported telemetry type.
		destKind, _ := dest["kind"].(string)
		if !validDestKinds[destKind] {
			results[i].Status = "error"
			results[i].Error = fmt.Sprintf("dest.kind must be one of [metric_set, log_set, trace_set, event_set], got '%s'", destKind)
			continue
		}

		// Warning checks (non-blocking).
		srcName, _ := src["name"].(string)
		destName, _ := dest["name"].(string)
		srcDomain, _ := src["domain"].(string)
		destDomain, _ := dest["domain"].(string)

		// Warn if src entity_set is not registered.
		if srcName != "" {
			srcKey := model.UModelElementRefKey(srcDomain, srcName, "entity_set")
			if _, found := elementsByName[srcKey]; !found {
				warnings = append(warnings, model.ErrorDetail{
					Field:  fmt.Sprintf("data_links[%d].src", i),
					Reason: fmt.Sprintf("entity_set '%s' not found in workspace (warning)", srcKey),
				})
			}
		}

		// Warn if dest dataset is not registered.
		if destName != "" {
			destKey := model.UModelElementRefKey(destDomain, destName, destKind)
			if _, found := elementsByName[destKey]; !found {
				warnings = append(warnings, model.ErrorDetail{
					Field:  fmt.Sprintf("data_links[%d].dest", i),
					Reason: fmt.Sprintf("%s '%s' not found in workspace (warning)", destKind, destKey),
				})
			}
		}

		// Warn if fields_mapping keys/values don't match known schemas.
		if fieldsMapping, ok := spec["fields_mapping"].(map[string]any); ok && len(fieldsMapping) > 0 {
			if srcName != "" {
				srcKey := model.UModelElementRefKey(srcDomain, srcName, "entity_set")
				if srcElem, found := elementsByName[srcKey]; found {
					for srcField := range fieldsMapping {
						if !fieldExistsInSpec(srcElem.Spec, srcField) {
							warnings = append(warnings, model.ErrorDetail{
								Field:  fmt.Sprintf("data_links[%d].fields_mapping", i),
								Reason: fmt.Sprintf("src field '%s' not found in entity_set '%s' (warning)", srcField, srcKey),
							})
						}
					}
				}
			}
		}

		validElements = append(validElements, dl)
		results[i].Status = "created"
	}

	// Write valid elements to GraphStore via PutUModelElements.
	if len(validElements) > 0 {
		_, err := s.umodel.PutUModelElements(ctx, model.UModelElementBatch{
			Workspace: workspace,
			Elements:  validElements,
		})
		if err != nil {
			return DataLinkWriteResponse{}, fmt.Errorf("put umodel elements: %w", err)
		}
	}

	response := DataLinkWriteResponse{
		Status:   "ok",
		Results:  results,
		Warnings: warnings,
	}

	// Cache idempotency result.
	if req.IdempotencyKey != "" {
		s.mu.Lock()
		s.datalinkIdempotency[idempotencyKey(workspace, req.IdempotencyKey)] = response
		s.mu.Unlock()
	}

	return response, nil
}

// GetDataLinks queries DataLink elements from the graph store with optional filters.
func (s *Service) GetDataLinks(ctx context.Context, workspace string, req DataLinkQueryRequest) (DataLinkQueryResponse, error) {
	if s.umodel == nil {
		return DataLinkQueryResponse{}, apperrors.New(apperrors.CodeNotImplemented, "umodel store not configured")
	}
	if workspace == "" {
		return DataLinkQueryResponse{}, apperrors.New(apperrors.CodeInvalidArgument, "workspace is required")
	}

	snapshot, err := s.umodel.GetUModelSnapshot(ctx, model.UModelSnapshotRequest{Workspace: workspace})
	if err != nil {
		return DataLinkQueryResponse{}, fmt.Errorf("get umodel snapshot: %w", err)
	}

	elementsByName := indexUModelElements(snapshot.Elements)

	filter := req.Filter
	var matched []DataLinkWithDetails

	for _, elem := range snapshot.Elements {
		if elem.Kind != "data_link" {
			continue
		}
		if !matchDataLinkFilter(elem, filter) {
			continue
		}

		detail := DataLinkWithDetails{
			Kind:    elem.Kind,
			Domain:  elem.Domain,
			Name:    elem.Name,
			Version: elem.Version,
			Spec:    elem.Spec,
		}

		// Enrich with linked dataset and storage info.
		if dest, ok := elem.Spec["dest"].(map[string]any); ok {
			destKind, _ := dest["kind"].(string)
			destDomain, _ := dest["domain"].(string)
			destName, _ := dest["name"].(string)

			detail.LinkedDataset = &DatasetSummary{
				Kind:   destKind,
				Domain: destDomain,
				Name:   destName,
			}

			// Resolve query_type from dataset schema.
			if queryType := resolveQueryType(destKind); queryType != "" {
				detail.LinkedDataset.QueryType = queryType
			}

			// Resolve linked storage via storage_link.
			if storage := resolveLinkedStorage(elementsByName, destDomain, destName, destKind); storage != nil {
				detail.LinkedStorage = storage
			}
		}

		matched = append(matched, detail)
	}

	return DataLinkQueryResponse{
		DataLinks: matched,
		Total:     len(matched),
	}, nil
}

// indexUModelElements builds a lookup map keyed by domain/name/kind.
func indexUModelElements(elements []model.UModelElement) map[string]model.UModelElement {
	m := make(map[string]model.UModelElement, len(elements))
	for _, e := range elements {
		key := model.UModelElementKey(e)
		if key != "" {
			m[key] = e
		}
	}
	return m
}

// matchDataLinkFilter checks whether a DataLink element matches the given filter criteria.
func matchDataLinkFilter(elem model.UModelElement, filter DataLinkQuery) bool {
	if filter.SrcDomain != "" || filter.SrcKind != "" || filter.SrcName != "" {
		src, _ := elem.Spec["src"].(map[string]any)
		if src == nil {
			return false
		}
		if filter.SrcDomain != "" && src["domain"] != filter.SrcDomain {
			return false
		}
		if filter.SrcKind != "" && src["kind"] != filter.SrcKind {
			return false
		}
		if filter.SrcName != "" && src["name"] != filter.SrcName {
			return false
		}
	}

	if filter.DestKind != "" || filter.DestDomain != "" || filter.DestName != "" {
		dest, _ := elem.Spec["dest"].(map[string]any)
		if dest == nil {
			return false
		}
		if filter.DestKind != "" && dest["kind"] != filter.DestKind {
			return false
		}
		if filter.DestDomain != "" && dest["domain"] != filter.DestDomain {
			return false
		}
		if filter.DestName != "" && dest["name"] != filter.DestName {
			return false
		}
	}

	if filter.DataLinkType != "" {
		if elem.Spec["data_link_type"] != filter.DataLinkType {
			return false
		}
	}

	return true
}

// resolveQueryType returns the canonical query type for a given dest kind.
func resolveQueryType(destKind string) string {
	switch destKind {
	case "metric_set":
		return "prom"
	case "log_set":
		return "logql"
	case "trace_set":
		return "traceql"
	case "event_set":
		return "sql"
	default:
		return ""
	}
}

// resolveLinkedStorage finds the storage_link for a given dataset and resolves the storage endpoint.
func resolveLinkedStorage(elements map[string]model.UModelElement, destDomain, destName, destKind string) *StorageSummary {
	for _, elem := range elements {
		if elem.Kind != "storage_link" {
			continue
		}
		src, _ := elem.Spec["src"].(map[string]any)
		if src == nil {
			continue
		}
		srcDomain, _ := src["domain"].(string)
		srcName, _ := src["name"].(string)
		if srcDomain != destDomain || srcName != destName {
			continue
		}
		dest, _ := elem.Spec["dest"].(map[string]any)
		if dest == nil {
			continue
		}
		storage := &StorageSummary{
			Kind:   dest["kind"].(string),
			Domain: dest["domain"].(string),
			Name:   dest["name"].(string),
		}
		if endpoint, ok := elem.Spec["endpoint"].(string); ok {
			storage.Endpoint = endpoint
		}
		return storage
	}
	return nil
}

// fieldExistsInSpec checks if a field name exists in the fields of a UModel spec.
func fieldExistsInSpec(spec map[string]any, fieldName string) bool {
	if spec == nil {
		return false
	}
	// Check spec.fields (entity_set schema defines fields here).
	if fields, ok := spec["fields"].(map[string]any); ok {
		if _, found := fields[fieldName]; found {
			return true
		}
	}
	// Also check spec.labels (metric_set uses labels).
	if labels, ok := spec["labels"].(map[string]any); ok {
		if _, found := labels[fieldName]; found {
			return true
		}
	}
	// Fallback: if no fields/labels defined, assume field exists (don't warn).
	return false
}
