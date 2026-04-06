// Copyright 2026 Florin Balint
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crud

import (
	"testing"

	gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"
)

// ---------------------------------------------------------------------------
// Proto builder helpers (used by TestQueryHash)
// ---------------------------------------------------------------------------

func condFilter(field string, op gocrudv1.Operator, v *gocrudv1.Value) *gocrudv1.Filter {
	return &gocrudv1.Filter{Filter: &gocrudv1.Filter_Condition{
		Condition: &gocrudv1.Condition{Field: field, Op: op, Operand: &gocrudv1.Condition_Value{Value: v}},
	}}
}

func andFilter(filters ...*gocrudv1.Filter) *gocrudv1.Filter {
	return &gocrudv1.Filter{Filter: &gocrudv1.Filter_Composite{
		Composite: &gocrudv1.CompositeFilter{Op: gocrudv1.CompositeFilter_AND, Filters: filters},
	}}
}

func strVal(s string) *gocrudv1.Value {
	return &gocrudv1.Value{Kind: &gocrudv1.Value_StringValue{StringValue: s}}
}

func intVal(i int64) *gocrudv1.Value {
	return &gocrudv1.Value{Kind: &gocrudv1.Value_IntValue{IntValue: i}}
}

func orderBy(field string, dir gocrudv1.OrderBy_Direction) *gocrudv1.OrderBy {
	return &gocrudv1.OrderBy{Field: field, Direction: dir}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestToken(t *testing.T) {
	tests := []struct {
		name    string
		token   offsetToken
		wantErr bool
	}{
		{
			name:  "round-trip simple",
			token: offsetToken{Offset: 42, QueryHash: "abc123"},
		},
		{
			name:  "round-trip zero offset",
			token: offsetToken{Offset: 0, QueryHash: "xyz"},
		},
		{
			name:  "round-trip large offset",
			token: offsetToken{Offset: 1_000_000, QueryHash: "deadbeef"},
		},
		{
			name:  "round-trip empty hash",
			token: offsetToken{Offset: 10, QueryHash: ""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := encodeToken(tc.token)
			got, err := decodeToken(encoded)
			if err != nil {
				t.Fatalf("decodeToken: %v", err)
			}
			if got.Offset != tc.token.Offset {
				t.Errorf("got offset %d, want %d", got.Offset, tc.token.Offset)
			}
			if got.QueryHash != tc.token.QueryHash {
				t.Errorf("got QueryHash %q, want %q", got.QueryHash, tc.token.QueryHash)
			}
		})
	}
}

func TestDecodeToken_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "bad base64", input: "not-valid-base64!!!"},
		{name: "valid base64 but not JSON", input: "aGVsbG8="},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeToken(tc.input)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestNextPageToken(t *testing.T) {
	const hash = "hashval"
	tests := []struct {
		name          string
		currentOffset int64
		pageSize      int32
		hasNext       bool
		wantEmpty     bool
		wantOffset    int64
	}{
		{
			name:          "has next",
			currentOffset: 0,
			pageSize:      20,
			hasNext:       true,
			wantEmpty:     false,
			wantOffset:    20,
		},
		{
			name:          "no next",
			currentOffset: 0,
			pageSize:      20,
			hasNext:       false,
			wantEmpty:     true,
		},
		{
			name:          "second page has next",
			currentOffset: 20,
			pageSize:      20,
			hasNext:       true,
			wantEmpty:     false,
			wantOffset:    40,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tok := nextPageToken(tc.currentOffset, tc.pageSize, tc.hasNext, hash)
			if tc.wantEmpty {
				if tok != "" {
					t.Errorf("expected empty token, got %q", tok)
				}
				return
			}
			if tok == "" {
				t.Fatal("expected non-empty token")
			}
			decoded, err := decodeToken(tok)
			if err != nil {
				t.Fatalf("decodeToken: %v", err)
			}
			if decoded.Offset != tc.wantOffset {
				t.Errorf("got offset %d, want %d", decoded.Offset, tc.wantOffset)
			}
			if decoded.QueryHash != hash {
				t.Errorf("got QueryHash %q, want %q", decoded.QueryHash, hash)
			}
		})
	}
}

func TestPrevPageToken(t *testing.T) {
	const hash = "hashval"
	tests := []struct {
		name          string
		currentOffset int64
		pageSize      int32
		wantEmpty     bool
		wantOffset    int64
	}{
		{
			name:          "first page",
			currentOffset: 0,
			pageSize:      20,
			wantEmpty:     true,
		},
		{
			name:          "second page",
			currentOffset: 20,
			pageSize:      20,
			wantEmpty:     false,
			wantOffset:    0,
		},
		{
			name:          "third page",
			currentOffset: 40,
			pageSize:      20,
			wantEmpty:     false,
			wantOffset:    20,
		},
		{
			name:          "partial first page offset",
			currentOffset: 10,
			pageSize:      20,
			wantEmpty:     false,
			wantOffset:    0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tok := prevPageToken(tc.currentOffset, tc.pageSize, hash)
			if tc.wantEmpty {
				if tok != "" {
					t.Errorf("expected empty token, got %q", tok)
				}
				return
			}
			if tok == "" {
				t.Fatal("expected non-empty token")
			}
			decoded, err := decodeToken(tok)
			if err != nil {
				t.Fatalf("decodeToken: %v", err)
			}
			if decoded.Offset != tc.wantOffset {
				t.Errorf("got offset %d, want %d", decoded.Offset, tc.wantOffset)
			}
		})
	}
}

func TestQueryHash(t *testing.T) {
	filter := condFilter("age", gocrudv1.Operator_GREATER_THAN, intVal(18))
	obs := []*gocrudv1.OrderBy{orderBy("age", gocrudv1.OrderBy_ASC)}

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "nil filter no order_by produces non-empty hash",
			run: func(t *testing.T) {
				h, err := queryHash(nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if h == "" {
					t.Error("expected non-empty hash")
				}
			},
		},
		{
			name: "hash is deterministic",
			run: func(t *testing.T) {
				h1, err := queryHash(filter, obs)
				if err != nil {
					t.Fatal(err)
				}
				h2, err := queryHash(filter, obs)
				if err != nil {
					t.Fatal(err)
				}
				if h1 != h2 {
					t.Errorf("hash is not deterministic: %q != %q", h1, h2)
				}
			},
		},
		{
			name: "filter-only and orderBy-only hashes differ",
			run: func(t *testing.T) {
				// hash(filter=X, orderBy=[]) must differ from hash(filter=nil, orderBy=[X])
				// to prevent separator-collision false positives.
				f := andFilter(condFilter("name", gocrudv1.Operator_EQUAL, strVal("alice")))
				ob := []*gocrudv1.OrderBy{orderBy("name", gocrudv1.OrderBy_ASC)}

				h1, _ := queryHash(f, nil)
				h2, _ := queryHash(nil, ob)
				if h1 == h2 {
					t.Error("hash collision: filter-only and orderBy-only produced the same hash")
				}
			},
		},
		{
			name: "different filters produce different hashes",
			run: func(t *testing.T) {
				h1, _ := queryHash(condFilter("age", gocrudv1.Operator_EQUAL, intVal(18)), nil)
				h2, _ := queryHash(condFilter("age", gocrudv1.Operator_EQUAL, intVal(19)), nil)
				if h1 == h2 {
					t.Error("different filters produced same hash")
				}
			},
		},
		{
			name: "different order_by produce different hashes",
			run: func(t *testing.T) {
				h1, _ := queryHash(nil, []*gocrudv1.OrderBy{orderBy("age", gocrudv1.OrderBy_ASC)})
				h2, _ := queryHash(nil, []*gocrudv1.OrderBy{orderBy("age", gocrudv1.OrderBy_DESC)})
				if h1 == h2 {
					t.Error("different order_by produced same hash")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func TestResolvePageParams(t *testing.T) {
	const defaultPS = int32(10)
	cfg := Config{DefaultPageSize: defaultPS}

	matchHash := "samehash"
	validTok := encodeToken(offsetToken{Offset: 20, QueryHash: matchHash})

	tests := []struct {
		name       string
		pagination *gocrudv1.Pagination
		hash       string
		cfg        Config
		wantOffset int64
		wantPS     int32
		wantErr    bool
	}{
		{
			name:       "nil pagination returns defaults",
			pagination: nil,
			hash:       "anyhash",
			cfg:        cfg,
			wantOffset: 0,
			wantPS:     defaultPS,
		},
		{
			name:       "empty pagination returns defaults",
			pagination: &gocrudv1.Pagination{},
			hash:       "anyhash",
			cfg:        cfg,
			wantOffset: 0,
			wantPS:     defaultPS,
		},
		{
			name:       "explicit page_size is respected",
			pagination: &gocrudv1.Pagination{PageSize: 5},
			hash:       "anyhash",
			cfg:        cfg,
			wantOffset: 0,
			wantPS:     5,
		},
		{
			name:       "page_size capped by MaxPageSize",
			pagination: &gocrudv1.Pagination{PageSize: 100},
			hash:       "anyhash",
			cfg:        Config{DefaultPageSize: 10, MaxPageSize: 50},
			wantOffset: 0,
			wantPS:     50,
		},
		{
			name:       "valid token with matching hash",
			pagination: &gocrudv1.Pagination{PageToken: validTok},
			hash:       matchHash,
			cfg:        cfg,
			wantOffset: 20,
			wantPS:     defaultPS,
		},
		{
			name:       "token with mismatched hash returns error",
			pagination: &gocrudv1.Pagination{PageToken: validTok},
			hash:       "differenthash",
			cfg:        cfg,
			wantErr:    true,
		},
		{
			name:       "invalid token returns error",
			pagination: &gocrudv1.Pagination{PageToken: "not-valid-base64!!!"},
			hash:       "anyhash",
			cfg:        cfg,
			wantErr:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			offset, ps, err := resolvePageParams(tc.pagination, tc.hash, tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if offset != tc.wantOffset {
				t.Errorf("got offset %d, want %d", offset, tc.wantOffset)
			}
			if ps != tc.wantPS {
				t.Errorf("got pageSize %d, want %d", ps, tc.wantPS)
			}
		})
	}
}
