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

package sqldialect

import (
	"testing"

	gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"
)

// mb is the MySQL builder under test.
var mb = newMySQLBuilder()

// ---------------------------------------------------------------------------
// SELECT — verifies backtick quoting and ? placeholders
// ---------------------------------------------------------------------------

func TestMySQLBuildSelect(t *testing.T) {
	tests := []struct {
		name     string
		q        selectQuery
		wantSQL  string
		wantArgs []any
		wantErr  bool
	}{
		{
			name:     "star no filter",
			q:        selectQuery{table: "users", limit: 10},
			wantSQL:  "SELECT * FROM `users` LIMIT ?",
			wantArgs: []any{int64(10)},
		},
		{
			name:     "specific columns",
			q:        selectQuery{table: "users", columns: []string{"id", "name"}, limit: 5},
			wantSQL:  "SELECT `id`, `name` FROM `users` LIMIT ?",
			wantArgs: []any{int64(5)},
		},
		{
			name:     "include_total",
			q:        selectQuery{table: "orders", limit: 10, includeTotal: true},
			wantSQL:  "SELECT *, COUNT(*) OVER() AS _total_count FROM `orders` LIMIT ?",
			wantArgs: []any{int64(10)},
		},
		{
			name: "filter with ? placeholders",
			q: selectQuery{
				table:  "users",
				filter: condFilter("age", gocrudv1.Operator_GREATER_THAN, intVal(18)),
				limit:  10,
			},
			wantSQL:  "SELECT * FROM `users` WHERE `age` > ? LIMIT ?",
			wantArgs: []any{int64(18), int64(10)},
		},
		{
			name: "IN filter",
			q: selectQuery{
				table:  "users",
				filter: inFilter("status", gocrudv1.Operator_IN, strVal("active"), strVal("pending")),
				limit:  10,
			},
			wantSQL:  "SELECT * FROM `users` WHERE `status` IN (?, ?) LIMIT ?",
			wantArgs: []any{"active", "pending", int64(10)},
		},
		{
			name: "composite AND filter",
			q: selectQuery{
				table: "products",
				filter: andFilter(
					condFilter("price", gocrudv1.Operator_GREATER_THAN, floatVal(10.0)),
					nullFilter("deleted_at", gocrudv1.Operator_IS_NULL),
				),
				limit: 5,
			},
			wantSQL:  "SELECT * FROM `products` WHERE (`price` > ? AND `deleted_at` IS NULL) LIMIT ?",
			wantArgs: []any{float64(10.0), int64(5)},
		},
		{
			name: "order by and offset",
			q: selectQuery{
				table:   "users",
				orderBy: []*gocrudv1.OrderBy{orderBy("name", gocrudv1.OrderBy_ASC)},
				limit:   10,
				offset:  20,
			},
			wantSQL:  "SELECT * FROM `users` ORDER BY `name` ASC LIMIT ? OFFSET ?",
			wantArgs: []any{int64(10), int64(20)},
		},
		{
			name:    "invalid table name",
			q:       selectQuery{table: "bad-table", limit: 10},
			wantErr: true,
		},
		{
			name:    "invalid column name",
			q:       selectQuery{table: "users", columns: []string{"bad-col"}, limit: 10},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := mb.BuildSelect(&tc.q)
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
// INSERT
// ---------------------------------------------------------------------------

func TestMySQLBuildInsert(t *testing.T) {
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
			wantSQL:  "INSERT INTO `users` (`name`, `email`, `age`) VALUES (?, ?, ?)",
			wantArgs: []any{"alice", "alice@example.com", int64(30)},
		},
		{
			name: "with DEFAULT for one column",
			q: insertQuery{
				table:   "users",
				columns: []string{"id", "name"},
				values:  []*gocrudv1.Value{nil, strVal("bob")},
			},
			wantSQL:  "INSERT INTO `users` (`id`, `name`) VALUES (DEFAULT, ?)",
			wantArgs: []any{"bob"},
		},
		{
			name:    "empty columns",
			q:       insertQuery{table: "users"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := mb.BuildInsert(&tc.q)
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
// UPDATE
// ---------------------------------------------------------------------------

func TestMySQLBuildUpdate(t *testing.T) {
	tests := []struct {
		name     string
		q        updateQuery
		wantSQL  string
		wantArgs []any
		wantErr  bool
	}{
		{
			name: "single column no filter",
			q: updateQuery{
				table: "users",
				updates: []*gocrudv1.ColumnUpdate{
					{Column: "name", Assignment: &gocrudv1.ColumnUpdate_Value{Value: strVal("alice")}},
				},
			},
			wantSQL:  "UPDATE `users` SET `name` = ?",
			wantArgs: []any{"alice"},
		},
		{
			name: "multiple columns with filter",
			q: updateQuery{
				table: "users",
				updates: []*gocrudv1.ColumnUpdate{
					{Column: "name", Assignment: &gocrudv1.ColumnUpdate_Value{Value: strVal("bob")}},
					{Column: "age", Assignment: &gocrudv1.ColumnUpdate_Value{Value: intVal(30)}},
				},
				filter: condFilter("id", gocrudv1.Operator_EQUAL, intVal(42)),
			},
			wantSQL:  "UPDATE `users` SET `name` = ?, `age` = ? WHERE `id` = ?",
			wantArgs: []any{"bob", int64(30), int64(42)},
		},
		{
			name: "DEFAULT resets column",
			q: updateQuery{
				table: "users",
				updates: []*gocrudv1.ColumnUpdate{
					{Column: "verified_at", Assignment: &gocrudv1.ColumnUpdate_UseDefault{UseDefault: true}},
					{Column: "name", Assignment: &gocrudv1.ColumnUpdate_Value{Value: strVal("carol")}},
				},
				filter: condFilter("id", gocrudv1.Operator_EQUAL, intVal(7)),
			},
			wantSQL:  "UPDATE `users` SET `verified_at` = DEFAULT, `name` = ? WHERE `id` = ?",
			wantArgs: []any{"carol", int64(7)},
		},
		{
			name:    "empty updates",
			q:       updateQuery{table: "users"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := mb.BuildUpdate(&tc.q)
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
// DELETE
// ---------------------------------------------------------------------------

func TestMySQLBuildDelete(t *testing.T) {
	tests := []struct {
		name     string
		q        deleteQuery
		wantSQL  string
		wantArgs []any
		wantErr  bool
	}{
		{
			name:     "single condition",
			q:        deleteQuery{table: "users", filter: condFilter("id", gocrudv1.Operator_EQUAL, intVal(42))},
			wantSQL:  "DELETE FROM `users` WHERE `id` = ?",
			wantArgs: []any{int64(42)},
		},
		{
			name: "composite filter",
			q: deleteQuery{
				table: "sessions",
				filter: andFilter(
					condFilter("user_id", gocrudv1.Operator_EQUAL, intVal(7)),
					nullFilter("expired_at", gocrudv1.Operator_IS_NOT_NULL),
				),
			},
			wantSQL:  "DELETE FROM `sessions` WHERE (`user_id` = ? AND `expired_at` IS NOT NULL)",
			wantArgs: []any{int64(7)},
		},
		{
			name:    "nil filter is rejected",
			q:       deleteQuery{table: "users", filter: nil},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := mb.BuildDelete(&tc.q)
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
// UPSERT — MySQL-specific ON DUPLICATE KEY UPDATE / INSERT IGNORE syntax
// ---------------------------------------------------------------------------

func TestMySQLBuildUpsert(t *testing.T) {
	tests := []struct {
		name     string
		q        upsertQuery
		wantSQL  string
		wantArgs []any
		wantErr  bool
	}{
		{
			name: "single conflict column, others updated via row alias",
			q: upsertQuery{
				table:        "users",
				columns:      []string{"id", "name", "email"},
				values:       []*gocrudv1.Value{intVal(1), strVal("alice"), strVal("alice@example.com")},
				conflictCols: []string{"id"},
			},
			wantSQL:  "INSERT INTO `users` (`id`, `name`, `email`) VALUES (?, ?, ?) AS _new ON DUPLICATE KEY UPDATE `name` = _new.`name`, `email` = _new.`email`",
			wantArgs: []any{int64(1), "alice", "alice@example.com"},
		},
		{
			name: "multiple conflict columns",
			q: upsertQuery{
				table:        "memberships",
				columns:      []string{"user_id", "group_id", "role"},
				values:       []*gocrudv1.Value{intVal(7), intVal(3), strVal("admin")},
				conflictCols: []string{"user_id", "group_id"},
			},
			wantSQL:  "INSERT INTO `memberships` (`user_id`, `group_id`, `role`) VALUES (?, ?, ?) AS _new ON DUPLICATE KEY UPDATE `role` = _new.`role`",
			wantArgs: []any{int64(7), int64(3), "admin"},
		},
		{
			name: "DEFAULT value in insert",
			q: upsertQuery{
				table:        "users",
				columns:      []string{"id", "name", "created_at"},
				values:       []*gocrudv1.Value{intVal(5), strVal("bob"), nil},
				conflictCols: []string{"id"},
			},
			wantSQL:  "INSERT INTO `users` (`id`, `name`, `created_at`) VALUES (?, ?, DEFAULT) AS _new ON DUPLICATE KEY UPDATE `name` = _new.`name`, `created_at` = _new.`created_at`",
			wantArgs: []any{int64(5), "bob"},
		},
		{
			name: "all columns are conflict columns produces INSERT IGNORE",
			q: upsertQuery{
				table:        "users",
				columns:      []string{"id"},
				values:       []*gocrudv1.Value{intVal(1)},
				conflictCols: []string{"id"},
			},
			wantSQL:  "INSERT IGNORE INTO `users` (`id`) VALUES (?)",
			wantArgs: []any{int64(1)},
		},
		{
			name:    "empty columns",
			q:       upsertQuery{table: "users", conflictCols: []string{"id"}},
			wantErr: true,
		},
		{
			name:    "columns values length mismatch",
			q:       upsertQuery{table: "users", columns: []string{"id", "name"}, values: []*gocrudv1.Value{intVal(1)}, conflictCols: []string{"id"}},
			wantErr: true,
		},
		{
			name:    "empty conflict columns",
			q:       upsertQuery{table: "users", columns: []string{"id", "name"}, values: []*gocrudv1.Value{intVal(1), strVal("x")}},
			wantErr: true,
		},
		{
			name:    "invalid table name",
			q:       upsertQuery{table: "bad-table", columns: []string{"id"}, values: []*gocrudv1.Value{intVal(1)}, conflictCols: []string{"id"}},
			wantErr: true,
		},
		{
			name:    "invalid conflict column name",
			q:       upsertQuery{table: "users", columns: []string{"id", "name"}, values: []*gocrudv1.Value{intVal(1), strVal("x")}, conflictCols: []string{"bad-col"}},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := mb.BuildUpsert(&tc.q)
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
// ForBackend
// ---------------------------------------------------------------------------

func TestForBackendMySQL(t *testing.T) {
	b, err := ForBackend(MySQL)
	if err != nil {
		t.Fatalf("ForBackend(MySQL) unexpected error: %v", err)
	}
	if b == nil {
		t.Fatal("ForBackend(MySQL) returned nil builder")
	}
}
