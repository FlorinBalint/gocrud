package crud

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/FlorinBalint/gocrud/crud/testdata"
	"github.com/FlorinBalint/gocrud/sqldialect"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// Mock infrastructure
// ---------------------------------------------------------------------------

type mockResult struct {
	lastInsertId int64
}

func (m *mockResult) LastInsertId() (int64, error) {
	return m.lastInsertId, nil
}

func (m *mockResult) RowsAffected() (int64, error) {
	return 1, nil
}

type mockDB struct {
	lastQuery string
	lastArgs  []any

	mockID  int64
	execErr error
}

func (m *mockDB) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	m.lastQuery = query
	m.lastArgs = args
	if m.execErr != nil {
		return nil, m.execErr
	}
	return &mockResult{lastInsertId: m.mockID}, nil
}

func (m *mockDB) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	m.lastQuery = query
	m.lastArgs = args
	return nil, fmt.Errorf("mockDB: QueryContext not implemented")
}

// mockRowData implements sqlRows for unit-testing scanReturning and the
// full RETURNING Create path via queryFn.
type mockRowData struct {
	data   [][]any
	idx    int
	closed bool
	err    error
}

func (r *mockRowData) Next() bool {
	if r.idx < len(r.data) {
		r.idx++
		return true
	}
	return false
}

func (r *mockRowData) Scan(dest ...any) error {
	row := r.data[r.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan: expected %d destinations, got %d", len(row), len(dest))
	}
	for i, v := range row {
		switch d := dest[i].(type) {
		case *int64:
			*d = v.(int64)
		case *string:
			*d = v.(string)
		}
	}
	return nil
}

func (r *mockRowData) Close() error {
	r.closed = true
	return nil
}

func (r *mockRowData) Err() error {
	return r.err
}

// mockQueryFn returns a queryFn that records the query on db and returns the
// given rows or error. Pass nil rows with a non-nil queryErr to simulate a
// connection failure; pass rows with nil queryErr for a successful RETURNING.
func mockQueryFn(db *mockDB, rows *mockRowData, queryErr error) func(context.Context, string, ...any) (sqlRows, error) {
	return func(_ context.Context, query string, args ...any) (sqlRows, error) {
		db.lastQuery = query
		db.lastArgs = args
		if queryErr != nil {
			return nil, queryErr
		}
		return rows, nil
	}
}

// ---------------------------------------------------------------------------
// NewCreateHandler
// ---------------------------------------------------------------------------

func TestNewCreateHandler(t *testing.T) {
	h, err := NewCreateHandler[*testdata.TestEntity, *testdata.CreateTestEntityRequest](nil, sqldialect.Postgres)
	if err != nil {
		t.Fatalf("NewCreateHandler failed: %v", err)
	}

	if h.table != "my_entities" {
		t.Errorf("expected table 'my_entities', got %q", h.table)
	}

	wantCols := []string{"id", "full_name", "age"}
	if diff := cmp.Diff(wantCols, h.columns); diff != "" {
		t.Errorf("columns mismatch (-want +got):\n%s", diff)
	}

	if h.entityField == nil || string(h.entityField.Name()) != "entity" {
		t.Errorf("expected entityField 'entity', got %v", h.entityField)
	}

	if len(h.reqIDFields) != 1 || string(h.reqIDFields[0].Name()) != "id" {
		t.Errorf("expected reqIDFields to contain 'id', got %v", h.reqIDFields)
	}

	if h.autoGenPK != nil {
		t.Error("expected autoGenPK to be nil for TestEntity")
	}
}

func TestNewCreateHandler_AutoGenPK(t *testing.T) {
	h, err := NewCreateHandler[*testdata.NumericEntity, *testdata.CreateNumericEntityRequest](nil, sqldialect.Postgres)
	if err != nil {
		t.Fatalf("NewCreateHandler failed: %v", err)
	}

	if h.autoGenPK == nil || string(h.autoGenPK.Name()) != "id" {
		t.Errorf("expected autoGenPK 'id', got %v", h.autoGenPK)
	}
	if len(h.reqIDFields) != 0 {
		t.Errorf("expected no reqIDFields for auto-generated PK entity, got %v", h.reqIDFields)
	}
}

// ---------------------------------------------------------------------------
// Create — user-provided string PK (TestEntity)
// ---------------------------------------------------------------------------

func TestCreateHandler_StringPK(t *testing.T) {
	tests := []struct {
		name       string
		req        *testdata.CreateTestEntityRequest
		wantEntity *testdata.TestEntity
		wantErr    string
	}{
		{
			name: "success",
			req: &testdata.CreateTestEntityRequest{
				Id:     "user123",
				Entity: &testdata.TestEntity{Name: "Alice", Age: 30},
			},
			wantEntity: &testdata.TestEntity{Id: "user123", Name: "Alice", Age: 30},
		},
		{
			name:    "missing entity",
			req:     &testdata.CreateTestEntityRequest{Id: "123"},
			wantErr: "missing entity field in request",
		},
		{
			name:    "missing required ID",
			req:     &testdata.CreateTestEntityRequest{Entity: &testdata.TestEntity{Name: "Alice"}},
			wantErr: "missing required ID field: id",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &mockDB{}
			h, err := NewCreateHandler[*testdata.TestEntity, *testdata.CreateTestEntityRequest](db, sqldialect.Postgres)
			if err != nil {
				t.Fatalf("NewCreateHandler failed: %v", err)
			}

			got, err := h.Create(context.Background(), tc.req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			if !proto.Equal(got, tc.wantEntity) {
				t.Errorf("entity mismatch:\n  got  %v\n  want %v", got, tc.wantEntity)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Create — auto-generated numeric PK (NumericEntity)
// ---------------------------------------------------------------------------

func TestCreateHandler_AutoGenNumericPK(t *testing.T) {
	tests := []struct {
		name       string
		backend    sqldialect.BackendType
		db         *mockDB
		mockRows   *mockRowData
		queryErr   error
		req        *testdata.CreateNumericEntityRequest
		wantEntity *testdata.NumericEntity
		wantErr    string
	}{
		{
			name:       "RETURNING — Postgres",
			backend:    sqldialect.Postgres,
			db:         &mockDB{},
			mockRows:   &mockRowData{data: [][]any{{int64(77)}}},
			req:        &testdata.CreateNumericEntityRequest{Entity: &testdata.NumericEntity{Name: "Bob"}},
			wantEntity: &testdata.NumericEntity{Id: 77, Name: "Bob"},
		},
		{
			name:       "RETURNING — SQLite",
			backend:    sqldialect.SQLite,
			db:         &mockDB{},
			mockRows:   &mockRowData{data: [][]any{{int64(77)}}},
			req:        &testdata.CreateNumericEntityRequest{Entity: &testdata.NumericEntity{Name: "Bob"}},
			wantEntity: &testdata.NumericEntity{Id: 77, Name: "Bob"},
		},
		{
			name:       "LastInsertId — MySQL",
			backend:    sqldialect.MySQL,
			db:         &mockDB{mockID: 42},
			req:        &testdata.CreateNumericEntityRequest{Entity: &testdata.NumericEntity{Name: "Bob"}},
			wantEntity: &testdata.NumericEntity{Id: 42, Name: "Bob"},
		},
		{
			name:     "RETURNING — query error",
			backend:  sqldialect.Postgres,
			db:       &mockDB{},
			queryErr: fmt.Errorf("connection refused"),
			req:      &testdata.CreateNumericEntityRequest{Entity: &testdata.NumericEntity{Name: "Bob"}},
			wantErr:  "connection refused",
		},
		{
			name:     "RETURNING — no rows",
			backend:  sqldialect.Postgres,
			db:       &mockDB{},
			mockRows: &mockRowData{data: nil},
			req:      &testdata.CreateNumericEntityRequest{Entity: &testdata.NumericEntity{Name: "Bob"}},
			wantErr:  "insert did not return any rows",
		},
		{
			name:    "ExecContext error — MySQL",
			backend: sqldialect.MySQL,
			db:      &mockDB{execErr: context.DeadlineExceeded},
			req:     &testdata.CreateNumericEntityRequest{Entity: &testdata.NumericEntity{Name: "Bob"}},
			wantErr: "executing insert",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewCreateHandler[*testdata.NumericEntity, *testdata.CreateNumericEntityRequest](tc.db, tc.backend)
			if err != nil {
				t.Fatalf("NewCreateHandler failed: %v", err)
			}
			if tc.mockRows != nil || tc.queryErr != nil {
				h.queryFn = mockQueryFn(tc.db, tc.mockRows, tc.queryErr)
			}

			got, err := h.Create(context.Background(), tc.req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			if !proto.Equal(got, tc.wantEntity) {
				t.Errorf("entity mismatch:\n  got  %v\n  want %v", got, tc.wantEntity)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Create — auto-generated string PK (UUIDEntity)
// ---------------------------------------------------------------------------

func TestCreateHandler_AutoGenStringPK(t *testing.T) {
	wantUUID := "550e8400-e29b-41d4-a716-446655440000"

	tests := []struct {
		name       string
		mockRows   *mockRowData
		req        *testdata.CreateUUIDEntityRequest
		wantEntity *testdata.UUIDEntity
	}{
		{
			name:       "RETURNING populates UUID",
			mockRows:   &mockRowData{data: [][]any{{wantUUID}}},
			req:        &testdata.CreateUUIDEntityRequest{Entity: &testdata.UUIDEntity{Name: "Eve"}},
			wantEntity: &testdata.UUIDEntity{Id: wantUUID, Name: "Eve"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &mockDB{}
			h, err := NewCreateHandler[*testdata.UUIDEntity, *testdata.CreateUUIDEntityRequest](db, sqldialect.Postgres)
			if err != nil {
				t.Fatalf("NewCreateHandler failed: %v", err)
			}
			h.queryFn = mockQueryFn(db, tc.mockRows, nil)

			got, err := h.Create(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			if !proto.Equal(got, tc.wantEntity) {
				t.Errorf("entity mismatch:\n  got  %v\n  want %v", got, tc.wantEntity)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewCreateHandler — CompositeEntity metadata
// ---------------------------------------------------------------------------

func TestNewCreateHandler_CompositeEntity(t *testing.T) {
	h, err := NewCreateHandler[*testdata.CompositeEntity, *testdata.CreateCompositeEntityRequest](nil, sqldialect.Postgres)
	if err != nil {
		t.Fatalf("NewCreateHandler failed: %v", err)
	}

	if h.table != "composite_entities" {
		t.Errorf("expected table 'composite_entities', got %q", h.table)
	}

	wantCols := []string{"tenant_id", "id", "name", "version"}
	if diff := cmp.Diff(wantCols, h.columns); diff != "" {
		t.Errorf("columns mismatch (-want +got):\n%s", diff)
	}

	if h.autoGenPK == nil || string(h.autoGenPK.Name()) != "id" {
		t.Errorf("expected autoGenPK 'id', got %v", h.autoGenPK)
	}

	wantAutoGenCols := []string{"id", "version"}
	if diff := cmp.Diff(wantAutoGenCols, h.autoGenCols); diff != "" {
		t.Errorf("autoGenCols mismatch (-want +got):\n%s", diff)
	}

	if len(h.reqIDFields) != 1 || string(h.reqIDFields[0].Name()) != "tenant_id" {
		t.Errorf("expected reqIDFields [tenant_id], got %v", h.reqIDFields)
	}
}

// ---------------------------------------------------------------------------
// Create — composite PK + non-PK auto-generated column (CompositeEntity)
// ---------------------------------------------------------------------------

func TestCreateHandler_CompositeEntity(t *testing.T) {
	tests := []struct {
		name       string
		backend    sqldialect.BackendType
		db         *mockDB
		mockRows   *mockRowData
		queryErr   error
		req        *testdata.CreateCompositeEntityRequest
		wantEntity *testdata.CompositeEntity
		wantErr    string
	}{
		{
			name:     "RETURNING — Postgres",
			backend:  sqldialect.Postgres,
			db:       &mockDB{},
			mockRows: &mockRowData{data: [][]any{{int64(42), int64(1)}}},
			req: &testdata.CreateCompositeEntityRequest{
				TenantId: "t1",
				Entity:   &testdata.CompositeEntity{Name: "item"},
			},
			wantEntity: &testdata.CompositeEntity{TenantId: "t1", Id: 42, Name: "item", Version: 1},
		},
		{
			name:    "LastInsertId — MySQL",
			backend: sqldialect.MySQL,
			db:      &mockDB{mockID: 42},
			req: &testdata.CreateCompositeEntityRequest{
				TenantId: "t1",
				Entity:   &testdata.CompositeEntity{Name: "item"},
			},
			wantEntity: &testdata.CompositeEntity{TenantId: "t1", Id: 42, Name: "item", Version: 0},
		},
		{
			name:    "missing tenant_id",
			backend: sqldialect.Postgres,
			db:      &mockDB{},
			req: &testdata.CreateCompositeEntityRequest{
				Entity: &testdata.CompositeEntity{Name: "item"},
			},
			wantErr: "missing required ID field: tenant_id",
		},
		{
			name:    "missing entity",
			backend: sqldialect.Postgres,
			db:      &mockDB{},
			req:     &testdata.CreateCompositeEntityRequest{TenantId: "t1"},
			wantErr: "missing entity field in request",
		},
		{
			name:     "RETURNING — query error",
			backend:  sqldialect.Postgres,
			db:       &mockDB{},
			queryErr: fmt.Errorf("connection refused"),
			req: &testdata.CreateCompositeEntityRequest{
				TenantId: "t1",
				Entity:   &testdata.CompositeEntity{Name: "item"},
			},
			wantErr: "connection refused",
		},
		{
			name:     "RETURNING — no rows",
			backend:  sqldialect.Postgres,
			db:       &mockDB{},
			mockRows: &mockRowData{data: nil},
			req: &testdata.CreateCompositeEntityRequest{
				TenantId: "t1",
				Entity:   &testdata.CompositeEntity{Name: "item"},
			},
			wantErr: "insert did not return any rows",
		},
		{
			name:    "ExecContext error — MySQL",
			backend: sqldialect.MySQL,
			db:      &mockDB{execErr: context.DeadlineExceeded},
			req: &testdata.CreateCompositeEntityRequest{
				TenantId: "t1",
				Entity:   &testdata.CompositeEntity{Name: "item"},
			},
			wantErr: "executing insert",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, err := NewCreateHandler[*testdata.CompositeEntity, *testdata.CreateCompositeEntityRequest](tc.db, tc.backend)
			if err != nil {
				t.Fatalf("NewCreateHandler failed: %v", err)
			}
			if tc.mockRows != nil || tc.queryErr != nil {
				h.queryFn = mockQueryFn(tc.db, tc.mockRows, tc.queryErr)
			}

			got, err := h.Create(context.Background(), tc.req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			if !proto.Equal(got, tc.wantEntity) {
				t.Errorf("entity mismatch:\n  got  %v\n  want %v", got, tc.wantEntity)
			}
		})
	}
}
