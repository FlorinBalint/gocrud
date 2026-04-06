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

// Tests are in package sqldialect (not sqldialect_test) to access the
// unexported postgresBuilder and selectQuery types directly without going
// through the DB.
package sqldialect

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"
	datepb "google.golang.org/genproto/googleapis/type/date"
	todpb "google.golang.org/genproto/googleapis/type/timeofday"
)

// b is the builder under test.
var b = &postgresBuilder{}

// ---------------------------------------------------------------------------
// Proto builder helpers
// ---------------------------------------------------------------------------

func condFilter(field string, op gocrudv1.Operator, v *gocrudv1.Value) *gocrudv1.Filter {
	return &gocrudv1.Filter{Filter: &gocrudv1.Filter_Condition{
		Condition: &gocrudv1.Condition{Field: field, Op: op, Operand: &gocrudv1.Condition_Value{Value: v}},
	}}
}

func inFilter(field string, op gocrudv1.Operator, vals ...*gocrudv1.Value) *gocrudv1.Filter {
	return &gocrudv1.Filter{Filter: &gocrudv1.Filter_Condition{
		Condition: &gocrudv1.Condition{Field: field, Op: op,
			Operand: &gocrudv1.Condition_Values{Values: &gocrudv1.ValueList{Values: vals}},
		},
	}}
}

func nullFilter(field string, op gocrudv1.Operator) *gocrudv1.Filter {
	return &gocrudv1.Filter{Filter: &gocrudv1.Filter_Condition{
		Condition: &gocrudv1.Condition{Field: field, Op: op},
	}}
}

func andFilter(filters ...*gocrudv1.Filter) *gocrudv1.Filter {
	return &gocrudv1.Filter{Filter: &gocrudv1.Filter_Composite{
		Composite: &gocrudv1.CompositeFilter{Op: gocrudv1.CompositeFilter_AND, Filters: filters},
	}}
}

func orFilter(filters ...*gocrudv1.Filter) *gocrudv1.Filter {
	return &gocrudv1.Filter{Filter: &gocrudv1.Filter_Composite{
		Composite: &gocrudv1.CompositeFilter{Op: gocrudv1.CompositeFilter_OR, Filters: filters},
	}}
}

func strVal(s string) *gocrudv1.Value {
	return &gocrudv1.Value{Kind: &gocrudv1.Value_StringValue{StringValue: s}}
}

func intVal(i int64) *gocrudv1.Value {
	return &gocrudv1.Value{Kind: &gocrudv1.Value_IntValue{IntValue: i}}
}

func floatVal(f float64) *gocrudv1.Value {
	return &gocrudv1.Value{Kind: &gocrudv1.Value_DoubleValue{DoubleValue: f}}
}

func boolVal(v bool) *gocrudv1.Value {
	return &gocrudv1.Value{Kind: &gocrudv1.Value_BoolValue{BoolValue: v}}
}

func orderBy(field string, dir gocrudv1.OrderBy_Direction) *gocrudv1.OrderBy {
	return &gocrudv1.OrderBy{Field: field, Direction: dir}
}

// ---------------------------------------------------------------------------
// INSERT
// ---------------------------------------------------------------------------

func TestBuildInsert(t *testing.T) {
	tests := []struct {
		name     string
		q        insertQuery
		wantSQL  string
		wantArgs []any
		wantErr  bool
	}{
		{
			name: "all values",
			q: insertQuery{
				table:   "users",
				columns: []string{"name", "email", "age"},
				values:  []*gocrudv1.Value{strVal("alice"), strVal("alice@example.com"), intVal(30)},
			},
			wantSQL:  `INSERT INTO "users" ("name", "email", "age") VALUES ($1, $2, $3)`,
			wantArgs: []any{"alice", "alice@example.com", int64(30)},
		},
		{
			name: "with DEFAULT for one column",
			q: insertQuery{
				table:   "users",
				columns: []string{"id", "name"},
				values:  []*gocrudv1.Value{nil, strVal("bob")},
			},
			wantSQL:  `INSERT INTO "users" ("id", "name") VALUES (DEFAULT, $1)`,
			wantArgs: []any{"bob"},
		},
		{
			name: "all DEFAULT",
			q: insertQuery{
				table:   "users",
				columns: []string{"id", "created_at"},
				values:  []*gocrudv1.Value{nil, nil},
			},
			wantSQL:  `INSERT INTO "users" ("id", "created_at") VALUES (DEFAULT, DEFAULT)`,
			wantArgs: nil,
		},
		{
			name:    "empty columns",
			q:       insertQuery{table: "users"},
			wantErr: true,
		},
		{
			name: "columns values length mismatch",
			q: insertQuery{
				table:   "users",
				columns: []string{"name", "email"},
				values:  []*gocrudv1.Value{strVal("alice")},
			},
			wantErr: true,
		},
		{
			name: "invalid table name",
			q: insertQuery{
				table:   "bad-table",
				columns: []string{"name"},
				values:  []*gocrudv1.Value{strVal("x")},
			},
			wantErr: true,
		},
		{
			name: "invalid column name",
			q: insertQuery{
				table:   "users",
				columns: []string{"bad-col"},
				values:  []*gocrudv1.Value{strVal("x")},
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := b.BuildInsert(&tc.q)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertQuery(t, got, args, tc.wantSQL, tc.wantArgs)
		})
	}
}

// ---------------------------------------------------------------------------
// SELECT clause and identifier validation
// ---------------------------------------------------------------------------

func TestBuildSelect(t *testing.T) {
	tests := []struct {
		name    string
		q       selectQuery
		wantSQL string
		wantErr bool
	}{
		{
			name:    "star",
			q:       selectQuery{table: "users", limit: 10},
			wantSQL: `SELECT * FROM "users" LIMIT $1`,
		},
		{
			name:    "specific fields",
			q:       selectQuery{table: "users", fields: []string{"id", "name", "email"}, limit: 10},
			wantSQL: `SELECT "id", "name", "email" FROM "users" LIMIT $1`,
		},
		{
			name:    "include_total star",
			q:       selectQuery{table: "users", limit: 10, includeTotal: true},
			wantSQL: `SELECT *, COUNT(*) OVER() AS _total_count FROM "users" LIMIT $1`,
		},
		{
			name:    "include_total with fields",
			q:       selectQuery{table: "users", fields: []string{"id", "name"}, limit: 10, includeTotal: true},
			wantSQL: `SELECT "id", "name", COUNT(*) OVER() AS _total_count FROM "users" LIMIT $1`,
		},
		// Identifier validation
		{name: "empty table name", q: selectQuery{table: "", limit: 10}, wantErr: true},
		{name: "table with semicolon", q: selectQuery{table: "drop; table", limit: 10}, wantErr: true},
		{name: "table with hyphen", q: selectQuery{table: "my-table", limit: 10}, wantErr: true},
		{name: "table starts with digit", q: selectQuery{table: "123table", limit: 10}, wantErr: true},
		{name: "table with space", q: selectQuery{table: "tbl name", limit: 10}, wantErr: true},
		{
			name:    "field with SQL injection",
			q:       selectQuery{table: "users", fields: []string{"name; DROP TABLE users"}, limit: 10},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := b.BuildSelect(&tc.q)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantSQL {
				t.Errorf("SQL:\n  got  %q\n  want %q", got, tc.wantSQL)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WHERE clause
// ---------------------------------------------------------------------------

func TestBuildWhere(t *testing.T) {
	ts := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		q        selectQuery
		wantSQL  string
		wantArgs []any
		wantErr  bool
	}{
		// Scalar operators
		{
			name:     "equal int",
			q:        selectQuery{table: "users", filter: condFilter("age", gocrudv1.Operator_EQUAL, intVal(30)), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "age" = $1 LIMIT $2`,
			wantArgs: []any{int64(30), int64(10)},
		},
		{
			name:     "not equal string",
			q:        selectQuery{table: "products", filter: condFilter("status", gocrudv1.Operator_NOT_EQUAL, strVal("deleted")), limit: 5},
			wantSQL:  `SELECT * FROM "products" WHERE "status" != $1 LIMIT $2`,
			wantArgs: []any{"deleted", int64(5)},
		},
		{
			name:     "greater than float",
			q:        selectQuery{table: "orders", filter: condFilter("total", gocrudv1.Operator_GREATER_THAN, floatVal(99.99)), limit: 10},
			wantSQL:  `SELECT * FROM "orders" WHERE "total" > $1 LIMIT $2`,
			wantArgs: []any{99.99, int64(10)},
		},
		{
			name:     "greater than or equal",
			q:        selectQuery{table: "orders", filter: condFilter("total", gocrudv1.Operator_GREATER_THAN_OR_EQUAL, floatVal(100.0)), limit: 10},
			wantSQL:  `SELECT * FROM "orders" WHERE "total" >= $1 LIMIT $2`,
			wantArgs: []any{float64(100.0), int64(10)},
		},
		{
			name:     "less than",
			q:        selectQuery{table: "users", filter: condFilter("age", gocrudv1.Operator_LESS_THAN, intVal(18)), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "age" < $1 LIMIT $2`,
			wantArgs: []any{int64(18), int64(10)},
		},
		{
			name:     "less than or equal",
			q:        selectQuery{table: "users", filter: condFilter("age", gocrudv1.Operator_LESS_THAN_OR_EQUAL, intVal(65)), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "age" <= $1 LIMIT $2`,
			wantArgs: []any{int64(65), int64(10)},
		},
		{
			name:     "like",
			q:        selectQuery{table: "users", filter: condFilter("name", gocrudv1.Operator_LIKE, strVal("John%")), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "name" LIKE $1 LIMIT $2`,
			wantArgs: []any{"John%", int64(10)},
		},
		{
			name:     "not like",
			q:        selectQuery{table: "users", filter: condFilter("email", gocrudv1.Operator_NOT_LIKE, strVal("%@spam.com")), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "email" NOT LIKE $1 LIMIT $2`,
			wantArgs: []any{"%@spam.com", int64(10)},
		},
		{
			name:     "bool value",
			q:        selectQuery{table: "users", filter: condFilter("active", gocrudv1.Operator_EQUAL, boolVal(true)), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "active" = $1 LIMIT $2`,
			wantArgs: []any{true, int64(10)},
		},
		// Null checks (no value placeholder)
		{
			name:     "is null",
			q:        selectQuery{table: "users", filter: nullFilter("deleted_at", gocrudv1.Operator_IS_NULL), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "deleted_at" IS NULL LIMIT $1`,
			wantArgs: []any{int64(10)},
		},
		{
			name:     "is not null",
			q:        selectQuery{table: "users", filter: nullFilter("verified_at", gocrudv1.Operator_IS_NOT_NULL), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "verified_at" IS NOT NULL LIMIT $1`,
			wantArgs: []any{int64(10)},
		},
		// IN / NOT IN
		{
			name:     "in",
			q:        selectQuery{table: "users", filter: inFilter("status", gocrudv1.Operator_IN, strVal("active"), strVal("pending")), limit: 10},
			wantSQL:  `SELECT * FROM "users" WHERE "status" IN ($1, $2) LIMIT $3`,
			wantArgs: []any{"active", "pending", int64(10)},
		},
		{
			name:     "not in",
			q:        selectQuery{table: "users", filter: inFilter("role", gocrudv1.Operator_NOT_IN, strVal("admin"), strVal("superuser")), limit: 5},
			wantSQL:  `SELECT * FROM "users" WHERE "role" NOT IN ($1, $2) LIMIT $3`,
			wantArgs: []any{"admin", "superuser", int64(5)},
		},
		{
			name:    "in with empty values",
			q:       selectQuery{table: "users", filter: inFilter("status", gocrudv1.Operator_IN), limit: 10},
			wantErr: true,
		},
		// Composite filters
		{
			name: "and composite",
			q: selectQuery{
				table: "users",
				filter: andFilter(
					condFilter("age", gocrudv1.Operator_GREATER_THAN, intVal(18)),
					condFilter("active", gocrudv1.Operator_EQUAL, boolVal(true)),
				),
				limit: 10,
			},
			wantSQL:  `SELECT * FROM "users" WHERE ("age" > $1 AND "active" = $2) LIMIT $3`,
			wantArgs: []any{int64(18), true, int64(10)},
		},
		{
			name: "or composite",
			q: selectQuery{
				table: "users",
				filter: orFilter(
					condFilter("role", gocrudv1.Operator_EQUAL, strVal("admin")),
					condFilter("role", gocrudv1.Operator_EQUAL, strVal("moderator")),
				),
				limit: 10,
			},
			wantSQL:  `SELECT * FROM "users" WHERE ("role" = $1 OR "role" = $2) LIMIT $3`,
			wantArgs: []any{"admin", "moderator", int64(10)},
		},
		{
			name: "nested composite and-of-ors",
			q: selectQuery{
				table: "users",
				filter: orFilter(
					andFilter(
						condFilter("age", gocrudv1.Operator_GREATER_THAN, intVal(18)),
						condFilter("active", gocrudv1.Operator_EQUAL, boolVal(true)),
					),
					condFilter("role", gocrudv1.Operator_EQUAL, strVal("admin")),
				),
				limit: 10,
			},
			wantSQL:  `SELECT * FROM "users" WHERE (("age" > $1 AND "active" = $2) OR "role" = $3) LIMIT $4`,
			wantArgs: []any{int64(18), true, "admin", int64(10)},
		},
		{
			name: "single-element composite has no extra parens",
			q: selectQuery{
				table:  "users",
				filter: andFilter(condFilter("age", gocrudv1.Operator_GREATER_THAN, intVal(18))),
				limit:  10,
			},
			wantSQL:  `SELECT * FROM "users" WHERE "age" > $1 LIMIT $2`,
			wantArgs: []any{int64(18), int64(10)},
		},
		// Value kinds
		{
			name: "timestamp value",
			q: selectQuery{
				table: "events",
				filter: condFilter("created_at", gocrudv1.Operator_GREATER_THAN,
					&gocrudv1.Value{Kind: &gocrudv1.Value_TimestampValue{TimestampValue: timestamppb.New(ts)}}),
				limit: 10,
			},
			wantSQL:  `SELECT * FROM "events" WHERE "created_at" > $1 LIMIT $2`,
			wantArgs: []any{timestamppb.New(ts).AsTime(), int64(10)},
		},
		{
			name: "date value",
			q: selectQuery{
				table: "subscriptions",
				filter: condFilter("expires_on", gocrudv1.Operator_LESS_THAN,
					&gocrudv1.Value{Kind: &gocrudv1.Value_DateValue{DateValue: &datepb.Date{Year: 2025, Month: 12, Day: 31}}}),
				limit: 10,
			},
			wantSQL:  `SELECT * FROM "subscriptions" WHERE "expires_on" < $1 LIMIT $2`,
			wantArgs: []any{time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), int64(10)},
		},
		{
			name: "time of day value",
			q: selectQuery{
				table: "schedules",
				filter: condFilter("start_time", gocrudv1.Operator_GREATER_THAN_OR_EQUAL,
					&gocrudv1.Value{Kind: &gocrudv1.Value_TimeValue{TimeValue: &todpb.TimeOfDay{Hours: 9, Minutes: 30}}}),
				limit: 10,
			},
			wantSQL:  `SELECT * FROM "schedules" WHERE "start_time" >= $1 LIMIT $2`,
			wantArgs: []any{"09:30:00.000000000", int64(10)},
		},
		{
			name: "interval value",
			q: selectQuery{
				table: "sessions",
				filter: condFilter("duration", gocrudv1.Operator_LESS_THAN,
					&gocrudv1.Value{Kind: &gocrudv1.Value_IntervalValue{IntervalValue: durationpb.New(2*time.Hour + 30*time.Minute)}}),
				limit: 10,
			},
			wantSQL:  `SELECT * FROM "sessions" WHERE "duration" < $1 LIMIT $2`,
			wantArgs: []any{(2*time.Hour + 30*time.Minute).Microseconds(), int64(10)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := b.BuildSelect(&tc.q)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertQuery(t, got, args, tc.wantSQL, tc.wantArgs)
		})
	}
}

// ---------------------------------------------------------------------------
// ORDER BY clause
// ---------------------------------------------------------------------------

func TestBuildOrderBy(t *testing.T) {
	tests := []struct {
		name    string
		q       selectQuery
		wantSQL string
	}{
		{
			name:    "single ASC",
			q:       selectQuery{table: "users", orderBy: []*gocrudv1.OrderBy{orderBy("name", gocrudv1.OrderBy_ASC)}, limit: 10},
			wantSQL: `SELECT * FROM "users" ORDER BY "name" ASC LIMIT $1`,
		},
		{
			name:    "single DESC",
			q:       selectQuery{table: "users", orderBy: []*gocrudv1.OrderBy{orderBy("created_at", gocrudv1.OrderBy_DESC)}, limit: 10},
			wantSQL: `SELECT * FROM "users" ORDER BY "created_at" DESC LIMIT $1`,
		},
		{
			name: "multiple columns",
			q: selectQuery{
				table: "users",
				orderBy: []*gocrudv1.OrderBy{
					orderBy("last_name", gocrudv1.OrderBy_ASC),
					orderBy("first_name", gocrudv1.OrderBy_ASC),
					orderBy("created_at", gocrudv1.OrderBy_DESC),
				},
				limit: 10,
			},
			wantSQL: `SELECT * FROM "users" ORDER BY "last_name" ASC, "first_name" ASC, "created_at" DESC LIMIT $1`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := b.BuildSelect(&tc.q)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantSQL {
				t.Errorf("SQL:\n  got  %q\n  want %q", got, tc.wantSQL)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// LIMIT / OFFSET
// ---------------------------------------------------------------------------

func TestBuildLimitOffset(t *testing.T) {
	tests := []struct {
		name     string
		q        selectQuery
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "first page no offset",
			q:        selectQuery{table: "users", limit: 20, offset: 0},
			wantSQL:  `SELECT * FROM "users" LIMIT $1`,
			wantArgs: []any{int64(20)},
		},
		{
			name:     "subsequent page with offset",
			q:        selectQuery{table: "users", limit: 20, offset: 40},
			wantSQL:  `SELECT * FROM "users" LIMIT $1 OFFSET $2`,
			wantArgs: []any{int64(20), int64(40)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := b.BuildSelect(&tc.q)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertQuery(t, got, args, tc.wantSQL, tc.wantArgs)
		})
	}
}

// ---------------------------------------------------------------------------
// Full query
// ---------------------------------------------------------------------------

func TestBuildFull(t *testing.T) {
	tests := []struct {
		name     string
		q        selectQuery
		wantSQL  string
		wantArgs []any
	}{
		{
			name: "filter + order + pagination",
			q: selectQuery{
				table:  "products",
				fields: []string{"id", "name", "price"},
				filter: andFilter(
					condFilter("price", gocrudv1.Operator_GREATER_THAN, floatVal(10.0)),
					nullFilter("deleted_at", gocrudv1.Operator_IS_NULL),
				),
				orderBy: []*gocrudv1.OrderBy{
					orderBy("price", gocrudv1.OrderBy_ASC),
					orderBy("name", gocrudv1.OrderBy_ASC),
				},
				limit:  21, // pageSize=20, n+1
				offset: 40,
			},
			wantSQL: `SELECT "id", "name", "price" FROM "products"` +
				` WHERE ("price" > $1 AND "deleted_at" IS NULL)` +
				` ORDER BY "price" ASC, "name" ASC` +
				` LIMIT $2 OFFSET $3`,
			wantArgs: []any{float64(10.0), int64(21), int64(40)},
		},
		{
			name: "filter + order + pagination + include_total",
			q: selectQuery{
				table:  "orders",
				fields: []string{"id", "total"},
				filter: condFilter("total", gocrudv1.Operator_GREATER_THAN, floatVal(50.0)),
				orderBy: []*gocrudv1.OrderBy{
					orderBy("total", gocrudv1.OrderBy_DESC),
				},
				limit:        10, // exact pageSize, no n+1 when total is known
				offset:       20,
				includeTotal: true,
			},
			wantSQL: `SELECT "id", "total", COUNT(*) OVER() AS _total_count FROM "orders"` +
				` WHERE "total" > $1` +
				` ORDER BY "total" DESC` +
				` LIMIT $2 OFFSET $3`,
			wantArgs: []any{float64(50.0), int64(10), int64(20)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := b.BuildSelect(&tc.q)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertQuery(t, got, args, tc.wantSQL, tc.wantArgs)
		})
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func assertQuery(t *testing.T, gotSQL string, gotArgs []any, wantSQL string, wantArgs []any) {
	t.Helper()
	if gotSQL != wantSQL {
		t.Errorf("SQL:\n  got  %q\n  want %q", gotSQL, wantSQL)
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args:\n  got  %v\n  want %v", gotArgs, wantArgs)
	}
}
