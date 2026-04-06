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
	Fields() []string              // nil/empty → SELECT *
	Filter() *gocrudv1.Filter     // nil → no WHERE clause
	OrderBy() []*gocrudv1.OrderBy // nil/empty → no ORDER BY
	Limit() int32                 // rows to fetch (already includes n+1 for has-next detection)
	Offset() int64
	IncludeTotal() bool // when true, appends COUNT(*) OVER() as the last SELECT column
}

// Builder translates a SelectQuery into a SQL SELECT string and positional arguments.
// Implementations must:
//   - Validate all identifier names (table, columns) to prevent SQL injection.
//   - Use $1, $2, … positional placeholders for all values.
//   - Never interpolate values into the SQL string.
type Builder interface {
	BuildSelect(q SelectQuery) (sql string, args []any, err error)
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

// selectQuery is the concrete implementation of SelectQuery.
type selectQuery struct {
	table        string
	fields       []string
	filter       *gocrudv1.Filter
	orderBy      []*gocrudv1.OrderBy
	limit        int32
	offset       int64
	includeTotal bool
}

func (q *selectQuery) Table() string                   { return q.table }
func (q *selectQuery) Fields() []string                { return q.fields }
func (q *selectQuery) Filter() *gocrudv1.Filter        { return q.filter }
func (q *selectQuery) OrderBy() []*gocrudv1.OrderBy    { return q.orderBy }
func (q *selectQuery) Limit() int32                    { return q.limit }
func (q *selectQuery) Offset() int64                   { return q.offset }
func (q *selectQuery) IncludeTotal() bool              { return q.includeTotal }

// NewSelectQuery constructs a SelectQuery with the given parameters.
func NewSelectQuery(
	table string,
	fields []string,
	filter *gocrudv1.Filter,
	orderBy []*gocrudv1.OrderBy,
	limit int32,
	offset int64,
	includeTotal bool,
) SelectQuery {
	return &selectQuery{
		table:        table,
		fields:       fields,
		filter:       filter,
		orderBy:      orderBy,
		limit:        limit,
		offset:       offset,
		includeTotal: includeTotal,
	}
}
