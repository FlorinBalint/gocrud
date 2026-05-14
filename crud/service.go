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

// Package list provides a dialect-agnostic list API that translates a
// proto [gocrudv1.ListRequest] into a SQL SELECT query and executes it
// against a configured database backend.
//
// Usage:
//
//	db, _ := sql.Open("pgx", dsn)
//	svc, _ := curd.NewService(db, crud.Config{
//	    Backend: sqldialect.Postgres,
//	    Table:   "users",
//	}, func(rows *sql.Rows) (*User, error) {
//	    u := &User{}
//	    return u, rows.Scan(&u.ID, &u.Name)
//	})
//	resp, _ := svc.List(ctx, req) // req is *gocrudv1.ListRequest
package crud

import (
	"context"
	"database/sql"
	"fmt"

	gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"
	"github.com/FlorinBalint/gocrud/sqldialect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

// Config configures a [Service].
type Config struct {
	// Backend is the SQL dialect to use (required).
	Backend sqldialect.BackendType

	// Table is the SQL table this service queries (required).
	// Must contain only letters, digits, and underscores.
	Table string

	// DefaultPageSize is the number of rows returned when the caller omits a
	// page size. 0 means use the package default (50).
	DefaultPageSize int32

	// MaxPageSize caps the page size a caller may request.
	// 0 means no server-side cap.
	MaxPageSize int32
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

// Rows is the interface passed to [RowScanner] for scanning a single database
// row. It is satisfied by *sql.Rows, which is what [Service.List] passes
// unless include_total is set (in which case a thin wrapper is used to capture
// the extra COUNT(*) OVER() column transparently).
type Rows interface {
	Scan(dest ...any) error
}

// RowScanner converts the current position of a [Rows] cursor into a value of T.
// The implementation calls rows.Scan with destination pointers for the columns
// it expects — do NOT include the _total_count column; that is handled internally.
type RowScanner[T any] func(rows Rows) (T, error)

// ListResponse is the result of a [Service.List] call.
type ListResponse[T any] struct {
	Items []T
	// NextPageToken is the opaque token for the next page; empty on the last page.
	NextPageToken string
	// PrevPageToken is the opaque token for the previous page; empty on the first page.
	PrevPageToken string
	// TotalSize is the total number of rows matching the filter, before pagination.
	// It is only set when the request had include_total = true; otherwise it is 0.
	TotalSize int64
}

// countRows wraps *sql.Rows when include_total is requested. It intercepts
// Scan to transparently capture the trailing _total_count window-function
// column so the caller's RowScanner does not need to know about it.
type countRows struct {
	*sql.Rows
	total int64
}

func (r *countRows) Scan(dest ...any) error {
	// Append our internal destination after the caller's destinations so that
	// the extra COUNT(*) OVER() column at the end of the result is consumed.
	return r.Rows.Scan(append(dest, &r.total)...)
}

// Service executes list queries against a SQL database for a single table.
// T is the application-level row type (e.g. a proto message or a plain struct).
//
// A binary listening on multiple tables or databases creates one Service per
// table and multiplexes at the call site. A mux abstraction may be added later.
//
// Create a Service with [NewService] and call [Service.List] to execute queries.
type Service[T any] struct {
	db      *sql.DB
	cfg     Config
	scanner RowScanner[T]
	b       sqldialect.Builder
}

// NewService constructs a Service for the given backend.
// db must be an open, ready-to-use *sql.DB; the service does not close it.
func NewService[T any](db *sql.DB, cfg Config, scanner RowScanner[T]) (*Service[T], error) {
	if db == nil {
		return nil, fmt.Errorf("list: db must not be nil")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("list: Config.Table must not be empty")
	}

	b, err := sqldialect.ForBackend(cfg.Backend)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	return &Service[T]{db: db, cfg: cfg, scanner: scanner, b: b}, nil
}

// List translates req into a SQL SELECT query, executes it against the
// configured database, and returns a page of results.
//
// Pagination is offset-based. Every page token encodes a fingerprint of the
// filter and order_by from the request that produced it. If a subsequent page
// request presents a token but has changed filter or order_by, List returns
// codes.InvalidArgument — the result set would be inconsistent across pages.
func (s *Service[T]) List(ctx context.Context, req *gocrudv1.ListRequest) (*ListResponse[T], error) {
	qHash, err := QueryHash(req.GetFilter(), req.GetOrderBy())
	if err != nil {
		return nil, fmt.Errorf("list: hashing request: %w", err)
	}

	offset, pageSize, err := ResolvePageParams(req.GetPagination(), qHash, s.cfg.DefaultPageSize, s.cfg.MaxPageSize)
	if err != nil {
		// All pagination resolution errors are caused by an invalid or
		// mismatched page token, which is caller error.
		return nil, status.Errorf(codes.InvalidArgument, "list: %v", err)
	}

	includeTotal := req.GetIncludeTotal()

	// When we know the total we can determine whether a next page exists from
	// offset + pageSize vs totalSize, so we fetch exactly pageSize rows.
	// Without the total we use the n+1 trick (fetch pageSize+1) to detect it.
	fetchLimit := pageSize
	if !includeTotal {
		fetchLimit = pageSize + 1
	}

	q := sqldialect.NewSelectQuery(
		s.cfg.Table,
		req.GetFields(),
		req.GetFilter(),
		req.GetOrderBy(),
		fetchLimit,
		offset,
		includeTotal,
	)

	sqlStr, args, err := s.b.BuildSelect(q)
	if err != nil {
		return nil, fmt.Errorf("list: building SQL: %w", err)
	}

	sqlRows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("list: executing query: %w", err)
	}
	defer sqlRows.Close()

	// When include_total is set, wrap the rows so that the trailing
	// COUNT(*) OVER() column is captured without the caller's RowScanner
	// needing to know about it.
	var cr *countRows
	var rowCursor Rows = sqlRows
	if includeTotal {
		cr = &countRows{Rows: sqlRows}
		rowCursor = cr
	}

	var items []T
	for sqlRows.Next() {
		item, err := s.scanner(rowCursor)
		if err != nil {
			return nil, fmt.Errorf("list: scanning row: %w", err)
		}
		items = append(items, item)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, fmt.Errorf("list: reading rows: %w", err)
	}

	var totalSize int64
	var hasNext bool
	if includeTotal && cr != nil {
		totalSize = cr.total
		hasNext = offset+int64(len(items)) < totalSize
	} else {
		// n+1 trick: the extra row signals that more pages exist.
		hasNext = len(items) == int(pageSize)+1
		if hasNext {
			items = items[:len(items)-1]
		}
	}

	return &ListResponse[T]{
		Items:         items,
		NextPageToken: NextPageToken(offset, pageSize, hasNext, qHash),
		PrevPageToken: PrevPageToken(offset, pageSize, qHash),
		TotalSize:     totalSize,
	}, nil
}
