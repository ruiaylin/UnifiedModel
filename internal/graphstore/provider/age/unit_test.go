//go:build age

package age

import (
	"testing"
	"time"

	"github.com/alibaba/UnifiedModel/pkg/model"
)

func TestParseAgtypeMap(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		checkKey string
		checkVal any
	}{
		{
			name:    "simple map",
			input:   `{key: "value", num: 42}`,
			wantLen: 2,
			checkKey: "key",
			checkVal: "value",
		},
		{
			name:    "empty map",
			input:   `{}`,
			wantLen: 0,
		},
		{
			name:    "with type suffix",
			input:   `{key: "value"}::vertex`,
			wantLen: 1,
			checkKey: "key",
			checkVal: "value",
		},
		{
			name:    "boolean values",
			input:   `{active: true, deleted: false}`,
			wantLen: 2,
			checkKey: "active",
			checkVal: true,
		},
		{
			name:    "null value",
			input:   `{value: null}`,
			wantLen: 1,
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
			name: "deleted entity",
			plan: model.QueryPlan{Filters: map[string]any{}},
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
