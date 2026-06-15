//go:build age

package age

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/alibaba/UnifiedModel/internal/cypher"
	"github.com/alibaba/UnifiedModel/internal/graphstore"
	"github.com/alibaba/UnifiedModel/pkg/contract"
	"github.com/alibaba/UnifiedModel/pkg/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxGraphFetch bounds how many entities/edges are pulled when building an
// in-memory graph for controlled Cypher. Kept independent of the public
// MaxLimit capability so tightening the latter does not starve graph traversal.
const maxGraphFetch = 1000

// Provider implements contract.GraphStore using PostgreSQL AGE extension.
type Provider struct {
	dsnPrefix    string
	graphPrefix  string
	maxOpenConns int
	mu           sync.Mutex
	workspaces   map[string]*workspaceHandle
	pool         *pgxpool.Pool // shared connection pool
}

type workspaceHandle struct {
	graphName string
}

const (
	defaultGraphPrefix  = "ws_"
	defaultMaxOpenConns = 20
)

func NewProvider(config graphstore.ProviderConfig) (*Provider, error) {
	dsn := config.Options["dsn"]
	if dsn == "" {
		return nil, fmt.Errorf("postgres.age provider requires 'dsn' option")
	}
	graphPrefix := config.Options["graph_prefix"]
	if graphPrefix == "" {
		graphPrefix = defaultGraphPrefix
	}
	maxOpenConns := defaultMaxOpenConns
	if v := config.Options["max_open_conns"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxOpenConns = n
		}
	}
	return &Provider{
		dsnPrefix:    dsn,
		graphPrefix:  graphPrefix,
		maxOpenConns: maxOpenConns,
		workspaces:   make(map[string]*workspaceHandle),
	}, nil
}

func init() {
	graphstore.RegisterProvider(graphstore.ProviderTypePostgresAge, func(config graphstore.ProviderConfig) (contract.GraphStore, error) {
		return NewProvider(config)
	})
}

func (p *Provider) OpenWorkspace(ctx context.Context, workspace model.WorkspaceMetadata) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.workspaces[workspace.ID]; ok {
		return nil
	}
	return p.openWorkspaceLocked(ctx, workspace)
}

func (p *Provider) DiscoverWorkspaces(ctx context.Context) ([]string, error) {
	p.mu.Lock()
	pool := p.pool
	p.mu.Unlock()

	if pool == nil {
		// Temporary pool for discovery if not already open
		poolConfig, err := pgxpool.ParseConfig(p.dsnPrefix)
		if err != nil {
			return nil, fmt.Errorf("parse dsn: %w", err)
		}
		if poolConfig.ConnConfig.RuntimeParams == nil {
			poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
		}
		poolConfig.ConnConfig.RuntimeParams["search_path"] = "public,ag_catalog"
		// AGE's cypher() function is not compatible with the extended/prepared
		// query protocol; concurrent calls otherwise fail with
		// "unhandled cypher(cstring) function call". Force simple protocol.
		poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

		tempPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			return nil, fmt.Errorf("create discovery pool: %w", err)
		}
		defer tempPool.Close()
		pool = tempPool
	}

	rows, err := pool.Query(ctx, "SELECT name FROM ag_catalog.ag_graph")
	if err != nil {
		return nil, fmt.Errorf("query ag_graph: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if strings.HasPrefix(name, p.graphPrefix) {
			id := strings.TrimPrefix(name, p.graphPrefix)
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (p *Provider) openWorkspaceLocked(ctx context.Context, workspace model.WorkspaceMetadata) error {
	// Initialize shared pool if needed
	if p.pool == nil {
		poolConfig, err := pgxpool.ParseConfig(p.dsnPrefix)
		if err != nil {
			return fmt.Errorf("parse dsn: %w", err)
		}
		poolConfig.MaxConns = int32(p.maxOpenConns)

		// Set search_path to include ag_catalog for AGE functions
		if poolConfig.ConnConfig.RuntimeParams == nil {
			poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
		}
		// Preserve existing search_path if any, otherwise default to public
		currentPath := poolConfig.ConnConfig.RuntimeParams["search_path"]
		if currentPath == "" {
			currentPath = "public"
		}
		poolConfig.ConnConfig.RuntimeParams["search_path"] = currentPath + ",ag_catalog"
		// AGE's cypher() function is not compatible with the extended/prepared
		// query protocol; concurrent calls otherwise fail with
		// "unhandled cypher(cstring) function call". Force simple protocol.
		poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

		pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			return fmt.Errorf("create pool: %w", err)
		}
		p.pool = pool
	}

	graphName := p.graphPrefix + sanitizeGraphName(workspace.ID)

	// Idempotent: check if graph already exists
	var exists bool
	err := p.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM ag_catalog.ag_graph WHERE name = $1)`,
		graphName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check graph existence: %w", err)
	}

	if !exists {
		// Create graph using safe string interpolation (not parameterized)
		createSQL := fmt.Sprintf(`SELECT * FROM ag_catalog.create_graph('%s')`, pgEscape(graphName))
		if _, err := p.pool.Exec(ctx, createSQL); err != nil {
			if !isDuplicateError(err) {
				return fmt.Errorf("create graph: %w", err)
			}
		}
	}

	p.workspaces[workspace.ID] = &workspaceHandle{graphName: graphName}
	return p.ensureSchemaLocked(ctx, graphName)
}

func (p *Provider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.pool != nil {
		p.pool.Close()
		p.pool = nil
	}
	for workspace := range p.workspaces {
		delete(p.workspaces, workspace)
	}
}

func (p *Provider) EnsureSchema(ctx context.Context, workspace string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	handle := p.workspaces[workspace]
	if handle == nil {
		return fmt.Errorf("workspace %q is not open", workspace)
	}
	return p.ensureSchemaLocked(ctx, handle.graphName)
}

func (p *Provider) ensureSchemaLocked(ctx context.Context, graphName string) error {
	// Create labels by creating and immediately deleting a node for each label
	// This is idempotent - if labels already exist, the operations still succeed
	for _, label := range []string{"umodel_node", "entity"} {
		// Create a temporary node to establish the label
		createQuery := fmt.Sprintf(
			`SELECT * FROM ag_catalog.cypher('%s', $$ CREATE (n:%s {_temp: true}) RETURN n $$) AS (v agtype)`,
			pgEscape(graphName), label)
		rows, err := p.pool.Query(ctx, createQuery)
		if err != nil {
			if !isDuplicateError(err) {
				return fmt.Errorf("create label %s: %w", label, err)
			}
			continue
		}
		rows.Close()

		// Delete the temporary node
		deleteQuery := fmt.Sprintf(
			`SELECT * FROM ag_catalog.cypher('%s', $$ MATCH (n:%s) WHERE n._temp = true DELETE n $$) AS (v agtype)`,
			pgEscape(graphName), label)
		rows2, err := p.pool.Query(ctx, deleteQuery)
		if err != nil {
			return fmt.Errorf("cleanup label %s: %w", label, err)
		}
		rows2.Close()
	}
	return nil
}

func (p *Provider) PutUModelElements(ctx context.Context, batch model.UModelElementBatch) (model.WriteResult, error) {
	handle, err := p.handle(batch.Workspace)
	if err != nil {
		return model.WriteResult{}, err
	}

	items := make([]model.BatchItemResult, 0, len(batch.Elements))
	for _, element := range batch.Elements {
		key := model.UModelElementKey(element)
		spec, _ := json.Marshal(element.Spec)

		// Use safe string interpolation for Cypher parameters
		query := fmt.Sprintf(
			`MERGE (n:umodel_node {key: '%s'}) SET n.kind = '%s', n.domain = '%s', n.name = '%s', n.version = '%s', n.spec = '%s'`,
			pgEscape(key),
			pgEscape(element.Kind),
			pgEscape(element.Domain),
			pgEscape(element.Name),
			pgEscape(element.Version),
			pgEscape(string(spec)))

		cypherSQL := fmt.Sprintf(
			`SELECT * FROM ag_catalog.cypher('%s', $$ %s $$) AS (v agtype)`,
			pgEscape(handle.graphName), query)

		if _, err := p.pool.Exec(ctx, cypherSQL); err != nil {
			return model.WriteResult{}, fmt.Errorf("put umodel element %s: %w", key, err)
		}
		items = append(items, model.BatchItemResult{ID: key, OK: true})
	}
	return model.WriteResult{Accepted: len(batch.Elements), Items: items}, nil
}

func (p *Provider) GetUModelSnapshot(ctx context.Context, req model.UModelSnapshotRequest) (model.UModelSnapshot, error) {
	handle, err := p.handle(req.Workspace)
	if err != nil {
		return model.UModelSnapshot{}, err
	}

	// Return full vertex, extract properties in Go
	query := fmt.Sprintf(
		`SELECT * FROM ag_catalog.cypher('%s', $$ MATCH (n:umodel_node) RETURN n ORDER BY n.key $$) AS (v agtype)`,
		pgEscape(handle.graphName))

	rows, err := p.cypherQuery(ctx, query)
	if err != nil {
		return model.UModelSnapshot{}, err
	}

	elements := make([]model.UModelElement, 0, len(rows))
	for _, row := range rows {
		props := extractProperties(row["v"])
		spec := map[string]any{}
		specStr := asString(props["spec"])
		if specStr != "" {
			_ = json.Unmarshal([]byte(specStr), &spec)
		}
		elements = append(elements, model.UModelElement{
			Kind:    asString(props["kind"]),
			Domain:  asString(props["domain"]),
			Name:    asString(props["name"]),
			Version: asString(props["version"]),
			Spec:    spec,
		})
	}

	version := req.Version
	if version == "" {
		version = graphstore.ProviderTypePostgresAge
	}
	return model.UModelSnapshot{Workspace: req.Workspace, Version: version, Elements: elements}, nil
}

func (p *Provider) WriteEntities(ctx context.Context, batch model.EntityWriteBatch) (model.WriteResult, error) {
	handle, err := p.handle(batch.Workspace)
	if err != nil {
		return model.WriteResult{}, err
	}

	items := make([]model.BatchItemResult, 0, len(batch.Entities))
	for _, payload := range batch.Entities {
		key := graphstore.EntityKey(payload)
		if err := p.executeEntityUpsert(ctx, handle, payload); err != nil {
			return model.WriteResult{}, err
		}
		items = append(items, model.BatchItemResult{ID: key, OK: true})
	}
	return model.WriteResult{Accepted: len(batch.Entities), Items: items}, nil
}

func (p *Provider) WriteRelations(ctx context.Context, batch model.RelationWriteBatch) (model.WriteResult, error) {
	handle, err := p.handle(batch.Workspace)
	if err != nil {
		return model.WriteResult{}, err
	}

	items := make([]model.BatchItemResult, 0, len(batch.Relations))
	for _, payload := range batch.Relations {
		src := relationEndpoint(payload, "src")
		dest := relationEndpoint(payload, "dest")

		// Ensure source entity exists
		if err := p.ensureEntityNode(ctx, handle, src); err != nil {
			return model.WriteResult{}, err
		}
		// Ensure dest entity exists
		if err := p.ensureEntityNode(ctx, handle, dest); err != nil {
			return model.WriteResult{}, err
		}

		key := graphstore.RelationKey(payload)
		relationType := asString(payload["__relation_type__"])
		method := methodOf(payload)
		firstObserved := asInt64(payload["__first_observed_time__"])
		lastObserved := asInt64(payload["__last_observed_time__"])
		keepAlive := asInt64(payload["__keep_alive_seconds__"])
		deleted := isDeletedMethod(method)
		properties, _ := json.Marshal(payload)
		srcKey := graphstore.EntityKey(src)
		destKey := graphstore.EntityKey(dest)

		// Use safe string interpolation
		query := fmt.Sprintf(
			`MATCH (s:entity {entity_key: '%s'}), (d:entity {entity_key: '%s'}) CREATE (s)-[r:topo {relation_key: '%s', relation_type: '%s', method: '%s', first_observed_time: %d, last_observed_time: %d, keep_alive_seconds: %d, deleted: %t, properties: agtype_in('%s')}]->(d) RETURN r`,
			pgEscape(srcKey),
			pgEscape(destKey),
			pgEscape(key),
			pgEscape(relationType),
			pgEscape(method),
			firstObserved,
			lastObserved,
			keepAlive,
			deleted,
			pgEscape(string(properties)))

		cypherSQL := fmt.Sprintf(
			`SELECT * FROM ag_catalog.cypher('%s', $$ %s $$) AS (v agtype)`,
			pgEscape(handle.graphName), query)

		if _, err := p.pool.Exec(ctx, cypherSQL); err != nil {
			return model.WriteResult{}, fmt.Errorf("write relation %s: %w", key, err)
		}
		items = append(items, model.BatchItemResult{ID: key, OK: true})
	}
	return model.WriteResult{Accepted: len(batch.Relations), Items: items}, nil
}

func (p *Provider) QueryEntities(ctx context.Context, plan model.EntityQueryPlan) (model.QueryResult, error) {
	handle, err := p.handle(plan.Workspace)
	if err != nil {
		return model.QueryResult{}, err
	}

	// Build WHERE clause with server-side filtering where possible
	whereClauses := []string{"e.deleted = false"}
	if domain := asString(plan.Filters["domain"]); domain != "" && domain != "*" {
		if strings.HasSuffix(domain, "*") {
			whereClauses = append(whereClauses, fmt.Sprintf("e.domain STARTS WITH '%s'", pgEscape(strings.TrimSuffix(domain, "*"))))
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf("e.domain = '%s'", pgEscape(domain)))
		}
	}
	if entityType := asString(plan.Filters["name"]); entityType != "" && entityType != "*" {
		if strings.HasSuffix(entityType, "*") {
			whereClauses = append(whereClauses, fmt.Sprintf("e.entity_type STARTS WITH '%s'", pgEscape(strings.TrimSuffix(entityType, "*"))))
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf("e.entity_type = '%s'", pgEscape(entityType)))
		}
	}

	whereClause := strings.Join(whereClauses, " AND ")
	limit := boundedLimit(plan.Limit)

	// Return full vertex, extract properties in Go
	query := fmt.Sprintf(
		`SELECT * FROM ag_catalog.cypher('%s', $$ MATCH (e:entity) WHERE %s RETURN e LIMIT %d $$) AS (v agtype)`,
		pgEscape(handle.graphName), whereClause, limit)

	rows, err := p.cypherQuery(ctx, query)
	if err != nil {
		return model.QueryResult{}, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		payload := entityPayloadFromAgtype(row["v"])
		if !entityMatches(payload, plan) {
			continue
		}
		out = append(out, entityRow(payload))
		if len(out) == limit {
			break
		}
	}
	return model.QueryResult{Columns: []string{"__domain__", "__entity_type__", "__entity_id__", "__method__", "__deleted__"}, Rows: out, Page: model.PageRequest{Limit: limit}}, nil
}

func (p *Provider) QueryTopo(ctx context.Context, plan model.TopoQueryPlan) (model.QueryResult, error) {
	handle, err := p.handle(plan.Workspace)
	if err != nil {
		return model.QueryResult{}, err
	}

	if plan.GraphCall != nil && plan.GraphCall.Name == "cypher" {
		return p.queryControlledCypher(ctx, handle, plan)
	}

	limit := boundedLimit(plan.Limit)

	// Return full edge and endpoint vertices
	query := fmt.Sprintf(
		`SELECT * FROM ag_catalog.cypher('%s', $$ MATCH (s:entity)-[r:topo]->(d:entity) WHERE r.deleted = false RETURN s, r, d LIMIT %d $$) AS (src agtype, edge agtype, dest agtype)`,
		pgEscape(handle.graphName), limit)

	rows, err := p.cypherQuery(ctx, query)
	if err != nil {
		return model.QueryResult{}, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		payload := relationPayloadFromAgtype(row["src"], row["edge"], row["dest"])
		if !relationMatches(payload, plan) {
			continue
		}
		out = append(out, relationRow(payload))
		if len(out) == limit {
			break
		}
	}
	return model.QueryResult{Columns: []string{"src", "relation", "dest", "__relation_type__", "__deleted__"}, Rows: out, Page: model.PageRequest{Limit: limit}}, nil
}

func (p *Provider) queryControlledCypher(ctx context.Context, handle *workspaceHandle, plan model.TopoQueryPlan) (model.QueryResult, error) {
	if err := cypher.ValidateReadOnly(plan.GraphCall.Cypher); err != nil {
		return model.QueryResult{}, err
	}

	graph, err := p.cypherGraph(ctx, handle, plan)
	if err != nil {
		return model.QueryResult{}, err
	}

	limit := boundedLimit(plan.Limit)
	result, err := cypher.Execute(plan.GraphCall.Cypher, graph, plan.Params, cypher.Options{Limit: limit})
	if err != nil {
		return model.QueryResult{}, err
	}
	return model.QueryResult{
		Columns: result.Columns,
		Rows:    result.Rows,
		Page:    model.PageRequest{Limit: result.Limit},
	}, nil
}

func (p *Provider) cypherGraph(ctx context.Context, handle *workspaceHandle, plan model.TopoQueryPlan) (cypher.Graph, error) {
	entityQuery := fmt.Sprintf(
		`SELECT * FROM ag_catalog.cypher('%s', $$ MATCH (e:entity) WHERE e.deleted = false RETURN e LIMIT %d $$) AS (v agtype)`,
		pgEscape(handle.graphName), maxGraphFetch)

	entityRows, err := p.cypherQuery(ctx, entityQuery)
	if err != nil {
		return cypher.Graph{}, err
	}

	nodes := map[string]cypher.Node{}
	for _, row := range entityRows {
		payload := entityPayloadFromAgtype(row["v"])
		if !entityMatches(payload, plan) {
			continue
		}
		key := graphstore.EntityKey(payload)
		nodes[key] = cypher.Node{
			ID:         key,
			Labels:     entityLabels(payload),
			Properties: cloneMap(map[string]any(payload)),
		}
	}

	relationQuery := fmt.Sprintf(
		`SELECT * FROM ag_catalog.cypher('%s', $$ MATCH (s:entity)-[r:topo]->(d:entity) WHERE r.deleted = false RETURN s, r, d LIMIT %d $$) AS (src agtype, edge agtype, dest agtype)`,
		pgEscape(handle.graphName), maxGraphFetch)

	relationRows, err := p.cypherQuery(ctx, relationQuery)
	if err != nil {
		return cypher.Graph{}, err
	}

	edges := []cypher.Edge{}
	for _, row := range relationRows {
		payload := relationPayloadFromAgtype(row["src"], row["edge"], row["dest"])
		if !relationMatches(payload, plan) {
			continue
		}
		src := relationEndpoint(payload, "src")
		dest := relationEndpoint(payload, "dest")
		srcKey := graphstore.EntityKey(src)
		destKey := graphstore.EntityKey(dest)
		if _, ok := nodes[srcKey]; !ok {
			nodes[srcKey] = cypher.Node{ID: srcKey, Labels: entityLabels(src), Properties: cloneMap(map[string]any(src))}
		}
		if _, ok := nodes[destKey]; !ok {
			nodes[destKey] = cypher.Node{ID: destKey, Labels: entityLabels(dest), Properties: cloneMap(map[string]any(dest))}
		}
		edges = append(edges, cypher.Edge{
			ID:         graphstore.RelationKey(payload),
			From:       srcKey,
			To:         destKey,
			Type:       asString(payload["__relation_type__"]),
			Properties: cloneMap(map[string]any(payload)),
		})
	}
	return cypher.Graph{Nodes: nodes, Edges: edges}, nil
}

func (p *Provider) Capabilities(ctx context.Context) (model.GraphStoreCapabilities, error) {
	return ageCapabilities(), nil
}

func (p *Provider) Health(ctx context.Context) (model.GraphStoreHealth, error) {
	p.mu.Lock()
	pool := p.pool
	p.mu.Unlock()

	if pool == nil {
		return model.GraphStoreHealth{Provider: graphstore.ProviderTypePostgresAge, Status: "not_initialized"}, nil
	}
	if err := pool.Ping(ctx); err != nil {
		return model.GraphStoreHealth{Provider: graphstore.ProviderTypePostgresAge, Status: "error", Message: err.Error()}, nil
	}
	return model.GraphStoreHealth{Provider: graphstore.ProviderTypePostgresAge, Status: "ok"}, nil
}

func (p *Provider) handle(workspace string) (*workspaceHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	handle := p.workspaces[workspace]
	if handle == nil {
		return nil, fmt.Errorf("workspace %q is not open", workspace)
	}
	return handle, nil
}

func (p *Provider) executeEntityUpsert(ctx context.Context, handle *workspaceHandle, payload model.EntityPayload) error {
	key := graphstore.EntityKey(payload)
	domain := asString(payload["__domain__"])
	entityType := asString(payload["__entity_type__"])
	entityID := asString(payload["__entity_id__"])
	method := methodOf(payload)
	firstObserved := asInt64(payload["__first_observed_time__"])
	lastObserved := asInt64(payload["__last_observed_time__"])
	keepAlive := asInt64(payload["__keep_alive_seconds__"])
	deleted := isDeletedMethod(method)
	properties, _ := json.Marshal(payload)

	// Use safe string interpolation for Cypher parameters
	query := fmt.Sprintf(
		`MERGE (e:entity {entity_key: '%s'}) SET e.domain = '%s', e.entity_type = '%s', e.entity_id = '%s', e.method = '%s', e.first_observed_time = %d, e.last_observed_time = %d, e.keep_alive_seconds = %d, e.deleted = %t, e.properties = agtype_in('%s')`,
		pgEscape(key),
		pgEscape(domain),
		pgEscape(entityType),
		pgEscape(entityID),
		pgEscape(method),
		firstObserved,
		lastObserved,
		keepAlive,
		deleted,
		pgEscape(string(properties)))

	cypherSQL := fmt.Sprintf(
		`SELECT * FROM ag_catalog.cypher('%s', $$ %s $$) AS (v agtype)`,
		pgEscape(handle.graphName), query)

	if _, err := p.pool.Exec(ctx, cypherSQL); err != nil {
		return fmt.Errorf("upsert entity %s: %w", key, err)
	}
	return nil
}

func (p *Provider) ensureEntityNode(ctx context.Context, handle *workspaceHandle, payload model.EntityPayload) error {
	key := graphstore.EntityKey(payload)

	// Check if entity exists
	checkQuery := fmt.Sprintf(
		`SELECT * FROM ag_catalog.cypher('%s', $$ MATCH (e:entity {entity_key: '%s'}) RETURN e $$) AS (v agtype)`,
		pgEscape(handle.graphName), pgEscape(key))

	rows, err := p.cypherQuery(ctx, checkQuery)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return nil
	}
	return p.executeEntityUpsert(ctx, handle, payload)
}

// cypherExec executes a Cypher query via AGE SQL function.
// Uses safe string interpolation, NOT pgx parameterized queries.
func (p *Provider) cypherExec(ctx context.Context, query string) error {
	_, err := p.pool.Exec(ctx, query)
	return err
}

// cypherQuery executes a Cypher query and returns rows as maps.
// Uses safe string interpolation, NOT pgx parameterized queries.
func (p *Provider) cypherQuery(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := p.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]any
	fieldDescriptions := rows.FieldDescriptions()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(fieldDescriptions))
		for i, fd := range fieldDescriptions {
			if i < len(values) {
				row[string(fd.Name)] = values[i]
			}
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// parseAgtype parses AGE's agtype format into Go values.
// Supports map, list, scalar, and type suffixes (::vertex, ::edge, ::path).
func parseAgtype(val string) any {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}

	// Strip type suffix like ::vertex, ::edge, ::path
	if idx := strings.LastIndex(val, "::"); idx > 0 {
		suffix := val[idx+2:]
		if suffix == "vertex" || suffix == "edge" || suffix == "path" {
			val = strings.TrimSpace(val[:idx])
		}
	}

	// Parse based on first character
	if len(val) == 0 {
		return nil
	}

	switch val[0] {
	case '{':
		return parseAgtypeMap(val)
	case '[':
		return parseAgtypeList(val)
	case '"':
		return parseAgtypeString(val)
	case 't':
		if val == "true" {
			return true
		}
	case 'f':
		if val == "false" {
			return false
		}
	case 'n':
		if val == "null" || val == "NULL" {
			return nil
		}
	}

	// Try number
	if n, err := strconv.ParseFloat(val, 64); err == nil {
		return n
	}

	return val
}

// parseAgtypeMap parses an agtype map like {key: value, ...}
func parseAgtypeMap(val string) map[string]any {
	result := map[string]any{}
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "{") || !strings.HasSuffix(val, "}") {
		return result
	}
	val = val[1 : len(val)-1]
	if strings.TrimSpace(val) == "" {
		return result
	}

	pairs := splitAgtypePairs(val)
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		colonIdx := strings.Index(pair, ":")
		if colonIdx < 0 {
			continue
		}
		key := strings.TrimSpace(pair[:colonIdx])
		value := strings.TrimSpace(pair[colonIdx+1:])
		// Remove quotes from key
		key = strings.Trim(key, "'\"")
		result[key] = parseAgtype(value)
	}
	return result
}

// parseAgtypeList parses an agtype list like [val1, val2, ...]
func parseAgtypeList(val string) []any {
	var result []any
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return result
	}
	val = val[1 : len(val)-1]
	if strings.TrimSpace(val) == "" {
		return result
	}

	items := splitAgtypePairs(val)
	for _, item := range items {
		result = append(result, parseAgtype(strings.TrimSpace(item)))
	}
	return result
}

// parseAgtypeString parses a quoted string
func parseAgtypeString(val string) string {
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		return val[1 : len(val)-1]
	}
	return val
}

// splitAgtypePairs splits agtype map/list entries by comma, respecting nested structures.
func splitAgtypePairs(val string) []string {
	var pairs []string
	depth := 0
	inString := false
	escape := false
	start := 0

	for i, r := range val {
		if escape {
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if r == '"' || r == '\'' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if r == '{' || r == '[' || r == '(' {
			depth++
		} else if r == '}' || r == ']' || r == ')' {
			depth--
		} else if r == ',' && depth == 0 {
			pairs = append(pairs, val[start:i])
			start = i + 1
		}
	}
	if start < len(val) {
		pairs = append(pairs, val[start:])
	}
	return pairs
}

// extractProperties extracts properties from an agtype vertex/edge value.
func extractProperties(val any) map[string]any {
	s := asString(val)
	if s == "" {
		return map[string]any{}
	}

	parsed := parseAgtype(s)
	switch v := parsed.(type) {
	case map[string]any:
		// If it's a vertex/edge, properties might be nested
		if props, ok := v["properties"]; ok {
			if p, ok := props.(map[string]any); ok {
				return p
			}
			if s, ok := props.(string); ok && s != "" {
				var p map[string]any
				if err := json.Unmarshal([]byte(s), &p); err == nil {
					return p
				}
			}
		}
		return v
	default:
		return map[string]any{}
	}
}

// entityPayloadFromAgtype extracts entity payload from an agtype vertex.
//
// At write time the complete entity payload (all flattened business fields plus
// the unified __domain__/__entity_type__/... fields) is JSON-marshaled into the
// vertex "properties" column. Reading it back and unpacking that nested map
// reproduces exactly the payload the Memory provider stores, so the serialized
// rows expose the same flattened header set instead of the raw storage columns
// (domain/entity_type/entity_id/properties).
func entityPayloadFromAgtype(val any) model.EntityPayload {
	props := extractProperties(val)
	payload := model.EntityPayload{}

	// Prefer the full original payload from the nested "properties" JSON.
	for k, v := range nestedPayload(props["properties"]) {
		payload[k] = v
	}

	// Fallback: map raw storage columns to unified fields when the nested
	// payload is absent (e.g. nodes created directly in AGE / recovery).
	setIfNil(payload, "__domain__", props["domain"])
	setIfNil(payload, "__entity_type__", props["entity_type"])
	setIfNil(payload, "__entity_id__", props["entity_id"])

	// Ensure required fields have defaults
	if payload["__method__"] == nil {
		payload["__method__"] = "Update"
	}
	if payload["__deleted__"] == nil {
		payload["__deleted__"] = false
	}
	return payload
}

// relationPayloadFromAgtype extracts relation payload from agtype src, edge, dest.
//
// Like entities, the full original relation payload (flattened fields plus the
// unified __relation_type__/__src_*__/__dest_*__ fields) is stored as JSON in
// the edge "properties" column. Unpacking it reproduces the Memory provider's
// flattened row shape.
func relationPayloadFromAgtype(srcVal, edgeVal, destVal any) model.RelationPayload {
	edgeProps := extractProperties(edgeVal)

	payload := model.RelationPayload{}
	// Prefer the full original payload from the nested "properties" JSON.
	for k, v := range nestedPayload(edgeProps["properties"]) {
		payload[k] = v
	}

	// Fallback: resolve endpoint identity from the vertices when the nested
	// payload does not already carry it (e.g. edges created directly in AGE).
	if payload["__src_domain__"] == nil || payload["__dest_domain__"] == nil {
		srcPayload := entityPayloadFromAgtype(srcVal)
		destPayload := entityPayloadFromAgtype(destVal)
		setIfNil(payload, "__src_domain__", srcPayload["__domain__"])
		setIfNil(payload, "__src_entity_type__", srcPayload["__entity_type__"])
		setIfNil(payload, "__src_entity_id__", srcPayload["__entity_id__"])
		setIfNil(payload, "__dest_domain__", destPayload["__domain__"])
		setIfNil(payload, "__dest_entity_type__", destPayload["__entity_type__"])
		setIfNil(payload, "__dest_entity_id__", destPayload["__entity_id__"])
	}

	setIfNil(payload, "__relation_type__", edgeProps["relation_type"])
	setIfNil(payload, "__relation_key__", edgeProps["relation_key"])
	if payload["__method__"] == nil {
		payload["__method__"] = "Update"
	}
	if payload["__deleted__"] == nil {
		payload["__deleted__"] = false
	}
	return payload
}

// nestedPayload decodes the JSON payload stored in a vertex/edge "properties"
// column. AGE may return it already parsed into a map, or as a raw JSON string.
func nestedPayload(val any) map[string]any {
	switch v := val.(type) {
	case map[string]any:
		return v
	case string:
		if v == "" {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			return m
		}
	}
	return nil
}

// setIfNil sets key to value only when the key is currently absent/nil and the
// provided value is non-nil.
func setIfNil(payload map[string]any, key string, value any) {
	if value == nil {
		return
	}
	if payload[key] == nil {
		payload[key] = value
	}
}

// Helper functions

func relationEndpoint(payload model.RelationPayload, side string) model.EntityPayload {
	entity := model.EntityPayload{
		"__method__":                  "Update",
		"__first_observed_time__":     payload["__first_observed_time__"],
		"__last_observed_time__":      payload["__last_observed_time__"],
		"__keep_alive_seconds__":      payload["__keep_alive_seconds__"],
		"__placeholder_from_topo__":   true,
		"__placeholder_relation__":    asString(payload["__relation_type__"]),
		"__placeholder_endpoint__":    side,
		"__placeholder_entity_role__": side,
	}
	entity["__domain__"] = payload["__"+side+"_domain__"]
	entity["__entity_type__"] = payload["__"+side+"_entity_type__"]
	entity["__entity_id__"] = payload["__"+side+"_entity_id__"]
	return entity
}

func entityLabels(payload model.EntityPayload) []string {
	domain := asString(payload["__domain__"])
	entityType := asString(payload["__entity_type__"])
	labels := []string{}
	if entityType != "" {
		labels = append(labels, entityType)
	}
	if domain != "" && entityType != "" {
		labels = append(labels, domain+"@"+entityType)
	}
	return labels
}

func entityRow(payload model.EntityPayload) map[string]any {
	row := cloneMap(map[string]any(payload))
	row["__deleted__"] = asBool(payload["__deleted__"])
	return row
}

func relationRow(payload model.RelationPayload) map[string]any {
	row := cloneMap(map[string]any(payload))
	if asString(row["src"]) == "" {
		row["src"] = strings.Join([]string{
			asString(payload["__src_domain__"]),
			asString(payload["__src_entity_type__"]),
			asString(payload["__src_entity_id__"]),
		}, "/")
	}
	if asString(row["dest"]) == "" {
		row["dest"] = strings.Join([]string{
			asString(payload["__dest_domain__"]),
			asString(payload["__dest_entity_type__"]),
			asString(payload["__dest_entity_id__"]),
		}, "/")
	}
	row["relation"] = asString(payload["__relation_type__"])
	row["__deleted__"] = asBool(payload["__deleted__"])
	return row
}

func entityMatches(payload model.EntityPayload, plan model.QueryPlan) bool {
	if asBool(payload["__deleted__"]) {
		return false
	}
	if !matchesFilter(asString(payload["__domain__"]), plan.Filters["domain"]) {
		return false
	}
	if !matchesFilter(asString(payload["__entity_type__"]), plan.Filters["name"]) {
		return false
	}
	if !matchesIDs(asString(payload["__entity_id__"]), plan.Filters["ids"]) {
		return false
	}
	if !matchesSearch(map[string]any(payload), plan.Filters["query"]) {
		return false
	}
	return visibleInRange(map[string]any(payload), plan.TimeRange)
}

func relationMatches(payload model.RelationPayload, plan model.QueryPlan) bool {
	if asBool(payload["__deleted__"]) && !hasTimeRange(plan.TimeRange) {
		return false
	}
	if !matchesFilter(asString(payload["__relation_type__"]), firstFilter(plan.Filters["relation_type"], plan.Filters["type"])) {
		return false
	}
	if !matchesFilter(relationEndpointKey(payload, "src"), plan.Filters["src"]) {
		return false
	}
	if !matchesFilter(relationEndpointKey(payload, "dest"), plan.Filters["dest"]) {
		return false
	}
	if plan.GraphCall != nil && len(plan.GraphCall.SeedIDs) > 0 {
		srcID := asString(payload["__src_entity_id__"])
		destID := asString(payload["__dest_entity_id__"])
		if !containsID(plan.GraphCall.SeedIDs, srcID) && !containsID(plan.GraphCall.SeedIDs, destID) {
			return false
		}
	}
	if !matchesSearch(map[string]any(payload), plan.Filters["query"]) {
		return false
	}
	return visibleInRange(map[string]any(payload), plan.TimeRange)
}

func relationEndpointKey(payload model.RelationPayload, side string) string {
	return strings.Join([]string{
		asString(payload["__"+side+"_domain__"]),
		asString(payload["__"+side+"_entity_type__"]),
		asString(payload["__"+side+"_entity_id__"]),
	}, "/")
}

func hasTimeRange(timeRange model.TimeRange) bool {
	return timeRange.From != nil || timeRange.To != nil
}

func visibleInRange(payload map[string]any, timeRange model.TimeRange) bool {
	if timeRange.From == nil && timeRange.To == nil {
		return true
	}

	first, hasFirst := int64Value(payload["__first_observed_time__"])
	last, hasLast := int64Value(payload["__last_observed_time__"])
	if !hasFirst || !hasLast {
		return true
	}

	keepAlive, _ := int64Value(payload["__keep_alive_seconds__"])
	from := int64(0)
	if timeRange.From != nil {
		from = timeRange.From.Unix()
	}
	to := time.Now().Add(100 * 365 * 24 * time.Hour).Unix()
	if timeRange.To != nil {
		to = timeRange.To.Unix()
	}
	if first >= to {
		return false
	}
	if asBool(payload["__deleted__"]) {
		return last > from
	}
	return last+keepAlive > from
}

func matchesFilter(value string, filter any) bool {
	if filter == nil || asString(filter) == "" || asString(filter) == "*" {
		return true
	}
	pattern := asString(filter)
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	return value == pattern
}

func matchesIDs(value string, filter any) bool {
	if filter == nil {
		return true
	}
	switch ids := filter.(type) {
	case []string:
		for _, id := range ids {
			if id == value {
				return true
			}
		}
		return false
	case []any:
		for _, id := range ids {
			if asString(id) == value {
				return true
			}
		}
		return false
	default:
		return asString(filter) == "" || asString(filter) == value
	}
}

func matchesSearch(payload map[string]any, filter any) bool {
	query := strings.ToLower(asString(filter))
	if query == "" {
		return true
	}
	for _, value := range payload {
		if strings.Contains(strings.ToLower(asString(value)), query) {
			return true
		}
	}
	return false
}

func firstFilter(values ...any) any {
	for _, value := range values {
		if asString(value) != "" {
			return value
		}
	}
	return nil
}

func containsID(ids []string, value string) bool {
	for _, id := range ids {
		if id == value {
			return true
		}
	}
	return false
}

func methodOf(payload map[string]any) string {
	method := asString(payload["__method__"])
	if method == "" {
		return "Update"
	}
	return method
}

func isDeletedMethod(method string) bool {
	return method == "Expire" || method == "Delete"
}

func int64Value(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case string:
		if typed == "" {
			return 0, false
		}
		var n int64
		_, err := fmt.Sscan(typed, &n)
		return n, err == nil
	default:
		return 0, false
	}
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func asInt64(value any) int64 {
	n, _ := int64Value(value)
	return n
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

// pgEscape escapes single quotes for safe string interpolation in SQL/Cypher.
func pgEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// sanitizeGraphName removes invalid characters from graph name.
func sanitizeGraphName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// isDuplicateError checks if an error is a "duplicate" error.
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "42710") // PostgreSQL duplicate_object
}

func boundedLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func ageCapabilities() model.GraphStoreCapabilities {
	return model.GraphStoreCapabilities{
		EntitySearch:       true,
		GraphMatch:         true,
		GraphCallNeighbors: true,
		ControlledCypher:   true,
		TimeVisibility:     true,
		ServerSideFilter:   true,
		MaxDepth:           10,
		MaxLimit:           100,
		Timeout:            "60s",
	}
}
