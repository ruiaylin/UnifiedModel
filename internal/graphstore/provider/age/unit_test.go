//go:build age

package age

import (
	"testing"
	"time"

	"github.com/alibaba/UnifiedModel/pkg/model"
)

func TestParseAgtypeMap(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantLen  int
		checkKey string
		checkVal any
	}{
		{
			name:     "simple map",
			input:    `{key: "value", num: 42}`,
			wantLen:  2,
			checkKey: "key",
			checkVal: "value",
		},
		{
			name:    "empty map",
			input:   `{}`,
			wantLen: 0,
		},
		{
			name:     "with type suffix",
			input:    `{key: "value"}::vertex`,
			wantLen:  1,
			checkKey: "key",
			checkVal: "value",
		},
		{
			name:     "boolean values",
			input:    `{active: true, deleted: false}`,
			wantLen:  2,
			checkKey: "active",
			checkVal: true,
		},
		{
			name:     "null value",
			input:    `{value: null}`,
			wantLen:  1,
			checkKey: "value",
			checkVal: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAgtype(tt.input)
			m, ok := result.(map[string]any)
			if !ok {
				t.Fatalf("expected map, got %T", result)
			}
			if len(m) != tt.wantLen {
				t.Errorf("expected map length %d, got %d", tt.wantLen, len(m))
			}
			if tt.checkKey != "" {
				if m[tt.checkKey] != tt.checkVal {
					t.Errorf("key %q: got %v, want %v", tt.checkKey, m[tt.checkKey], tt.checkVal)
				}
			}
		})
	}
}

func TestParseAgtypeList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int // expected length
	}{
		{
			name:  "simple list",
			input: `[1, 2, 3]`,
			want:  3,
		},
		{
			name:  "empty list",
			input: `[]`,
			want:  0,
		},
		{
			name:  "mixed types",
			input: `["text", 42, true, null]`,
			want:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAgtype(tt.input)
			list, ok := result.([]any)
			if !ok {
				t.Fatalf("expected list, got %T: %v", result, result)
			}
			if len(list) != tt.want {
				t.Errorf("expected length %d, got %d", tt.want, len(list))
			}
		})
	}
}

func TestParseAgtypeScalar(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  any
	}{
		{name: "string", input: `"hello"`, want: "hello"},
		{name: "number", input: `42`, want: float64(42)},
		{name: "float", input: `3.14`, want: 3.14},
		{name: "true", input: `true`, want: true},
		{name: "false", input: `false`, want: false},
		{name: "null", input: `null`, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAgtype(tt.input)
			if result != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", result, result, tt.want, tt.want)
			}
		})
	}
}

func TestParseAgtypeTypeSuffixes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "vertex suffix", input: `{id: 1}::vertex`},
		{name: "edge suffix", input: `{id: 1}::edge`},
		{name: "path suffix", input: `[{id: 1}]::path`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAgtype(tt.input)
			if result == nil {
				t.Error("expected non-nil result")
			}
		})
	}
}

func TestEntityMatches(t *testing.T) {
	payload := model.EntityPayload{
		"__domain__":              "infra",
		"__entity_type__":         "host",
		"__entity_id__":           "host-001",
		"__deleted__":             false,
		"__first_observed_time__": time.Now().Unix(),
		"__last_observed_time__":  time.Now().Unix(),
		"__keep_alive_seconds__":  3600,
	}

	tests := []struct {
		name   string
		plan   model.QueryPlan
		expect bool
	}{
		{
			name:   "match all",
			plan:   model.QueryPlan{Filters: map[string]any{}},
			expect: true,
		},
		{
			name:   "match domain",
			plan:   model.QueryPlan{Filters: map[string]any{"domain": "infra"}},
			expect: true,
		},
		{
			name:   "match domain wildcard",
			plan:   model.QueryPlan{Filters: map[string]any{"domain": "inf*"}},
			expect: true,
		},
		{
			name:   "no match domain",
			plan:   model.QueryPlan{Filters: map[string]any{"domain": "other"}},
			expect: false,
		},
		{
			name:   "match name (entity_type)",
			plan:   model.QueryPlan{Filters: map[string]any{"name": "host"}},
			expect: true,
		},
		{
			name:   "match ids",
			plan:   model.QueryPlan{Filters: map[string]any{"ids": []string{"host-001"}}},
			expect: true,
		},
		{
			name:   "no match ids",
			plan:   model.QueryPlan{Filters: map[string]any{"ids": []string{"other"}}},
			expect: false,
		},
		{
			name:   "deleted entity",
			plan:   model.QueryPlan{Filters: map[string]any{}},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := cloneMap(payload)
			if tt.name == "deleted entity" {
				p["__deleted__"] = true
			}
			got := entityMatches(p, tt.plan)
			if got != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, got)
			}
		})
	}
}

func TestRelationMatches(t *testing.T) {
	payload := model.RelationPayload{
		"__relation_type__":       "depends_on",
		"__deleted__":             false,
		"__src_domain__":          "infra",
		"__src_entity_type__":     "host",
		"__src_entity_id__":       "host-001",
		"__dest_domain__":         "infra",
		"__dest_entity_type__":    "service",
		"__dest_entity_id__":      "svc-001",
		"__first_observed_time__": time.Now().Unix(),
		"__last_observed_time__":  time.Now().Unix(),
		"__keep_alive_seconds__":  3600,
	}

	tests := []struct {
		name   string
		plan   model.QueryPlan
		expect bool
	}{
		{
			name:   "match all",
			plan:   model.QueryPlan{Filters: map[string]any{}},
			expect: true,
		},
		{
			name:   "match relation_type",
			plan:   model.QueryPlan{Filters: map[string]any{"relation_type": "depends_on"}},
			expect: true,
		},
		{
			name:   "no match relation_type",
			plan:   model.QueryPlan{Filters: map[string]any{"relation_type": "other"}},
			expect: false,
		},
		{
			name:   "match src",
			plan:   model.QueryPlan{Filters: map[string]any{"src": "infra/host/host-001"}},
			expect: true,
		},
		{
			name:   "match dest",
			plan:   model.QueryPlan{Filters: map[string]any{"dest": "infra/service/svc-001"}},
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := cloneMapRelation(payload)
			if tt.name == "deleted relation" {
				p["__deleted__"] = true
			}
			got := relationMatches(p, tt.plan)
			if got != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, got)
			}
		})
	}
}

func TestVisibleInRange(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"__first_observed_time__": now.Add(-1 * time.Hour).Unix(),
		"__last_observed_time__":  now.Unix(),
		"__keep_alive_seconds__":  int64(3600),
		"__deleted__":             false,
	}

	tests := []struct {
		name      string
		timeRange model.TimeRange
		expect    bool
	}{
		{
			name:      "no range",
			timeRange: model.TimeRange{},
			expect:    true,
		},
		{
			name:      "within range",
			timeRange: model.TimeRange{From: timePtr(now.Add(-2 * time.Hour)), To: timePtr(now.Add(1 * time.Hour))},
			expect:    true,
		},
		{
			name:      "before range",
			timeRange: model.TimeRange{From: timePtr(now.Add(2 * time.Hour))},
			expect:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := visibleInRange(payload, tt.timeRange)
			if got != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, got)
			}
		})
	}
}

func TestMatchesFilter(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		filter any
		expect bool
	}{
		{name: "nil filter", value: "test", filter: nil, expect: true},
		{name: "empty filter", value: "test", filter: "", expect: true},
		{name: "wildcard", value: "test", filter: "*", expect: true},
		{name: "exact match", value: "test", filter: "test", expect: true},
		{name: "exact no match", value: "test", filter: "other", expect: false},
		{name: "prefix wildcard", value: "testing", filter: "test*", expect: true},
		{name: "prefix wildcard no match", value: "other", filter: "test*", expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesFilter(tt.value, tt.filter)
			if got != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, got)
			}
		})
	}
}

func TestPgEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"a''b", "a''''b"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := pgEscape(tt.input)
			if got != tt.want {
				t.Errorf("pgEscape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeGraphName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"abc123", "abc123"},
		{"abc-123", "abc-123"},
		{"abc_123", "abc_123"},
		{"abc.123", "abc_123"},
		{"abc 123", "abc_123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeGraphName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeGraphName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSplitAgtypePairs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"simple", "a: 1, b: 2", 2},
		{"nested", "a: {x: 1}, b: 2", 2},
		{"single", "a: 1", 1},
		{"empty", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAgtypePairs(tt.input)
			if len(got) != tt.want {
				t.Errorf("expected %d pairs, got %d: %v", tt.want, len(got), got)
			}
		})
	}
}

func TestExtractProperties(t *testing.T) {
	// Test with vertex format
	vertex := `{entity_key: "test", domain: "infra", __domain__: "infra"}::vertex`
	props := extractProperties(vertex)
	if props["entity_key"] != "test" {
		t.Errorf("expected entity_key=test, got %v", props["entity_key"])
	}
}

// TestEntityPayloadFlattening verifies the read path expands the full original
// payload stored in the nested "properties" JSON (flattened business fields plus
// unified identity fields) and does not leak raw storage columns.
func TestEntityPayloadFlattening(t *testing.T) {
	vertex := `{id: 1, label: "entity", properties: {` +
		`entity_key: "infra/host/h1", domain: "infra", entity_type: "host", entity_id: "h1", deleted: false, ` +
		`properties: {__domain__: "infra", __entity_type__: "host", __entity_id__: "h1", __category__: "compute", display_name: "Host 1", __method__: "Update", __deleted__: false}` +
		`}}::vertex`

	payload := entityPayloadFromAgtype(vertex)

	if payload["display_name"] != "Host 1" {
		t.Errorf("expected flattened display_name=Host 1, got %v", payload["display_name"])
	}
	if payload["__category__"] != "compute" {
		t.Errorf("expected flattened __category__=compute, got %v", payload["__category__"])
	}
	if payload["__domain__"] != "infra" || payload["__entity_type__"] != "host" || payload["__entity_id__"] != "h1" {
		t.Errorf("expected unified identity fields, got %v", map[string]any(payload))
	}
	for _, raw := range []string{"entity_key", "domain", "entity_type", "entity_id", "properties"} {
		if _, ok := payload[raw]; ok {
			t.Errorf("raw storage column %q leaked into payload", raw)
		}
	}
}

// TestEntityPayloadFlatteningEscapedProperties mirrors the exact agtype AGE
// returns from a live graph: the nested business payload is serialized as an
// escaped JSON *string* (not an inline agtype map). The read path must JSON-
// unescape it so the full flattened header set survives instead of collapsing
// to the 5 fallback identity fields.
func TestEntityPayloadFlatteningEscapedProperties(t *testing.T) {
	vertex := `{"id": 1125899906842631, "label": "entity", "properties": {"domain": "devops", "method": "Update", "deleted": false, "entity_id": "h1", "entity_key": "devops/devops.service/h1", ` +
		`"properties": "{\"__category__\":\"entity\",\"__domain__\":\"devops\",\"__entity_type__\":\"devops.service\",\"__entity_id__\":\"h1\",\"display_name\":\"delivery-service\",\"owner\":\"commerce-engineering\",\"__method__\":\"Update\",\"__deleted__\":false}", ` +
		`"entity_type": "devops.service"}}::vertex`

	payload := entityPayloadFromAgtype(vertex)

	if payload["display_name"] != "delivery-service" {
		t.Errorf("expected flattened display_name=delivery-service, got %v", payload["display_name"])
	}
	if payload["__category__"] != "entity" {
		t.Errorf("expected flattened __category__=entity, got %v", payload["__category__"])
	}
	if payload["owner"] != "commerce-engineering" {
		t.Errorf("expected flattened owner, got %v", payload["owner"])
	}
	if payload["__domain__"] != "devops" || payload["__entity_type__"] != "devops.service" || payload["__entity_id__"] != "h1" {
		t.Errorf("expected unified identity fields, got %v", map[string]any(payload))
	}
	for _, raw := range []string{"entity_key", "domain", "entity_type", "entity_id", "properties"} {
		if _, ok := payload[raw]; ok {
			t.Errorf("raw storage column %q leaked into payload", raw)
		}
	}
}

// TestParseAgtypeStringUnescape verifies quoted agtype strings are JSON-
// unescaped so embedded JSON payloads decode cleanly.
func TestParseAgtypeStringUnescape(t *testing.T) {
	in := `"{\"k\":\"v\"}"`
	got := parseAgtypeString(in)
	if got != `{"k":"v"}` {
		t.Errorf("expected unescaped JSON, got %q", got)
	}
	if parseAgtypeString(`"plain"`) != "plain" {
		t.Errorf("expected plain string passthrough")
	}
}

// TestRelationPayloadFlattening verifies relations expand the nested payload and
// resolve endpoint identity fields.
func TestRelationPayloadFlattening(t *testing.T) {
	src := `{id: 1, label: "entity", properties: {properties: {__domain__: "infra", __entity_type__: "host", __entity_id__: "h1"}}}::vertex`
	dest := `{id: 2, label: "entity", properties: {properties: {__domain__: "infra", __entity_type__: "svc", __entity_id__: "s1"}}}::vertex`
	edge := `{id: 3, label: "topo", properties: {relation_type: "runs_on", relation_key: "rk", deleted: false, ` +
		`properties: {__relation_type__: "runs_on", __src_domain__: "infra", __src_entity_type__: "host", __src_entity_id__: "h1", __dest_domain__: "infra", __dest_entity_type__: "svc", __dest_entity_id__: "s1", weight: "5"}}}::edge`

	payload := relationPayloadFromAgtype(src, edge, dest)

	if payload["weight"] != "5" {
		t.Errorf("expected flattened weight=5, got %v", payload["weight"])
	}
	if payload["__src_domain__"] != "infra" || payload["__dest_entity_id__"] != "s1" {
		t.Errorf("expected endpoint identity fields, got %v", map[string]any(payload))
	}
	if payload["__relation_type__"] != "runs_on" {
		t.Errorf("expected __relation_type__=runs_on, got %v", payload["__relation_type__"])
	}
	// relation_key is internal AGE edge storage metadata and must not leak into
	// the flattened payload: surfacing it adds an extra .topo header column that
	// the Memory provider does not have, breaking contract parity.
	if _, ok := payload["__relation_key__"]; ok {
		t.Errorf("did not expect __relation_key__ in flattened payload, got %v", payload["__relation_key__"])
	}

	row := relationRow(payload)
	if row["src"] != "infra/host/h1" || row["dest"] != "infra/svc/s1" {
		t.Errorf("expected built src/dest, got src=%v dest=%v", row["src"], row["dest"])
	}
	if _, ok := row["__relation_key__"]; ok {
		t.Errorf("did not expect __relation_key__ in topo row, got %v", row["__relation_key__"])
	}
}

// TestNestedPayload covers both the already-parsed map case and the raw JSON
// string case returned by AGE.
func TestNestedPayload(t *testing.T) {
	fromMap := nestedPayload(map[string]any{"a": "1"})
	if fromMap["a"] != "1" {
		t.Errorf("expected map passthrough, got %v", fromMap)
	}
	fromString := nestedPayload(`{"a":"1","b":2}`)
	if fromString["a"] != "1" {
		t.Errorf("expected JSON string decode, got %v", fromString)
	}
	if nestedPayload("") != nil || nestedPayload(nil) != nil {
		t.Errorf("expected nil for empty/nil input")
	}
}

// Helper functions

func cloneMapRelation(source model.RelationPayload) model.RelationPayload {
	target := make(model.RelationPayload, len(source))
	for k, v := range source {
		target[k] = v
	}
	return target
}

func timePtr(t time.Time) *time.Time {
	return &t
}
