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

// Package sqldialect translates SelectQuery values into SQL strings and
// positional arguments for a specific database backend.
package sqldialect

import (
	"fmt"

	gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"
)

// BackendType identifies the SQL database backend.
type BackendType int

const (
	// Postgres selects the PostgreSQL query builder ($N placeholders).
	Postgres BackendType = iota + 1
	// Future backends (MySQL, SQLite, …) will be added here.
)

// SelectQuery is the fully-resolved internal representation of a SELECT that
// is built by the list service after resolving pagination, then handed to a Builder.
type SelectQuery interface {
	Table() string
	Columns() []string             // nil/empty → SELECT *
	Filter() *gocrudv1.Filter     // nil → no WHERE clause
	OrderBy() []*gocrudv1.OrderBy // nil/empty → no ORDER BY
	Limit() int32                 // rows to fetch (already includes n+1 for has-next detection)
	Offset() int64
	IncludeTotal() bool // when true, appends COUNT(*) OVER() as the last SELECT column
}

// Builder translates query values into SQL strings and positional arguments.
// Implementations must:
//   - Validate all identifier names (table, columns) to prevent SQL injection.
//   - Use $1, $2, … positional placeholders for all values.
//   - Never interpolate values into the SQL string.
type Builder interface {
	BuildSelect(q SelectQuery) (sql string, args []any, err error)
	BuildInsert(q InsertQuery) (sql string, args []any, err error)
	BuildUpdate(q UpdateQuery) (sql string, args []any, err error)
	BuildDelete(q DeleteQuery) (sql string, args []any, err error)
}

// ForBackend returns a Builder for the given backend dialect.
func ForBackend(b BackendType) (Builder, error) {
	switch b {
	case Postgres:
		return &postgresBuilder{}, nil
	default:
		return nil, fmt.Errorf("sqldialect: unsupported backend %v", b)
	}
}

// InsertQuery is the fully-resolved internal representation of an INSERT handed
// to a Builder by the crud service.
//
// A nil entry in Values() at index i means SQL DEFAULT for that column —
// the database will apply its column default (sequence, expression, etc.).
// len(Columns()) must equal len(Values()).
type InsertQuery interface {
	Table() string
	Columns() []string         // must be non-empty
	Values() []*gocrudv1.Value // nil entry → DEFAULT for that column
}

// insertQuery is the concrete implementation of InsertQuery.
type insertQuery struct {
	table   string
	columns []string
	values  []*gocrudv1.Value
}

func (q *insertQuery) Table() string             { return q.table }
func (q *insertQuery) Columns() []string         { return q.columns }
func (q *insertQuery) Values() []*gocrudv1.Value { return q.values }

// NewInsertQuery constructs an InsertQuery. Pass nil for a value to use SQL
// DEFAULT for that column. len(columns) must equal len(values).
func NewInsertQuery(table string, columns []string, values []*gocrudv1.Value) InsertQuery {
	return &insertQuery{table: table, columns: columns, values: values}
}

// UpdateQuery is the fully-resolved internal representation of an UPDATE
// handed to a Builder by the crud service.
//
// The sqldialect layer does not enforce a WHERE clause; that constraint is
// the caller's responsibility (e.g. the service requiring a primary-key filter).
type UpdateQuery interface {
	Table() string
	Updates() []*gocrudv1.ColumnUpdate // the SET clause; must be non-empty
	Filter() *gocrudv1.Filter          // nil → no WHERE clause (updates all rows)
}

// updateQuery is the concrete implementation of UpdateQuery.
type updateQuery struct {
	table   string
	updates []*gocrudv1.ColumnUpdate
	filter  *gocrudv1.Filter
}

func (q *updateQuery) Table() string                     { return q.table }
func (q *updateQuery) Updates() []*gocrudv1.ColumnUpdate { return q.updates }
func (q *updateQuery) Filter() *gocrudv1.Filter          { return q.filter }

// NewUpdateQuery constructs an UpdateQuery. Pass nil for filter to omit the
// WHERE clause. Use ColumnUpdate.use_default to apply SQL DEFAULT to a column.
func NewUpdateQuery(table string, updates []*gocrudv1.ColumnUpdate, filter *gocrudv1.Filter) UpdateQuery {
	return &updateQuery{table: table, updates: updates, filter: filter}
}

// DeleteQuery is the fully-resolved internal representation of a DELETE handed
// to a Builder by the crud service.
//
// A non-nil Filter is mandatory. Builders must refuse to execute a DELETE with
// no WHERE clause to prevent accidental full-table deletion.
type DeleteQuery interface {
	Table() string
	Filter() *gocrudv1.Filter // must be non-nil
}

// deleteQuery is the concrete implementation of DeleteQuery.
type deleteQuery struct {
	table  string
	filter *gocrudv1.Filter
}

func (q *deleteQuery) Table() string            { return q.table }
func (q *deleteQuery) Filter() *gocrudv1.Filter { return q.filter }

// NewDeleteQuery constructs a DeleteQuery. filter must be non-nil; BuildDelete
// will return an error if it is nil or produces an empty WHERE clause.
func NewDeleteQuery(table string, filter *gocrudv1.Filter) DeleteQuery {
	return &deleteQuery{table: table, filter: filter}
}

// selectQuery is the concrete implementation of SelectQuery.
type selectQuery struct {
	table        string
	columns      []string
	filter       *gocrudv1.Filter
	orderBy      []*gocrudv1.OrderBy
	limit        int32
	offset       int64
	includeTotal bool
}

func (q *selectQuery) Table() string                   { return q.table }
func (q *selectQuery) Columns() []string               { return q.columns }
func (q *selectQuery) Filter() *gocrudv1.Filter        { return q.filter }
func (q *selectQuery) OrderBy() []*gocrudv1.OrderBy    { return q.orderBy }
func (q *selectQuery) Limit() int32                    { return q.limit }
func (q *selectQuery) Offset() int64                   { return q.offset }
func (q *selectQuery) IncludeTotal() bool              { return q.includeTotal }

// NewSelectQuery constructs a SelectQuery with the given parameters.
func NewSelectQuery(
	table string,
	columns []string,
	filter *gocrudv1.Filter,
	orderBy []*gocrudv1.OrderBy,
	limit int32,
	offset int64,
	includeTotal bool,
) SelectQuery {
	return &selectQuery{
		table:        table,
		columns:      columns,
		filter:       filter,
		orderBy:      orderBy,
		limit:        limit,
		offset:       offset,
		includeTotal: includeTotal,
	}
}
