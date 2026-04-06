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
	"fmt"
	"strings"
	"time"
	"unicode"

	gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"
)

// postgresBuilder builds PostgreSQL SELECT queries using $N positional
// placeholders. All identifier names are validated before use; values are
// always passed as query arguments, never interpolated into SQL.
type postgresBuilder struct{}

var _ Builder = (*postgresBuilder)(nil)

// BuildSelect implements the [Builder] interface.
func (b *postgresBuilder) BuildSelect(q SelectQuery) (string, []any, error) {
	var sb strings.Builder
	var args []any
	idx := 1 // next $N placeholder index

	// SELECT
	selectClause, err := pgBuildSelect(q.Columns(), q.IncludeTotal())
	if err != nil {
		return "", nil, err
	}
	quotedTable, err := pgQuoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}
	fmt.Fprintf(&sb, "SELECT %s FROM %s", selectClause, quotedTable)

	// WHERE
	if q.Filter() != nil {
		where, whereArgs, nextIdx, err := pgBuildFilter(q.Filter(), idx)
		if err != nil {
			return "", nil, err
		}
		if where != "" {
			fmt.Fprintf(&sb, " WHERE %s", where)
			args = append(args, whereArgs...)
			idx = nextIdx
		}
	}

	// ORDER BY
	if len(q.OrderBy()) > 0 {
		order, err := pgBuildOrderBy(q.OrderBy())
		if err != nil {
			return "", nil, err
		}
		fmt.Fprintf(&sb, " ORDER BY %s", order)
	}

	// LIMIT / OFFSET
	limitSQL, limitArgs := pgBuildLimitOffset(q.Limit(), q.Offset(), idx)
	fmt.Fprintf(&sb, " %s", limitSQL)
	args = append(args, limitArgs...)

	return sb.String(), args, nil
}

// ---------------------------------------------------------------------------
// INSERT
// ---------------------------------------------------------------------------

// BuildInsert implements the [Builder] interface.
// A nil value entry produces the SQL keyword DEFAULT for that column, allowing
// the database to apply its column default (sequence, expression, etc.).
func (b *postgresBuilder) BuildInsert(q InsertQuery) (string, []any, error) {
	if len(q.Columns()) == 0 {
		return "", nil, fmt.Errorf("INSERT requires at least one column")
	}
	if len(q.Columns()) != len(q.Values()) {
		return "", nil, fmt.Errorf("columns and values length mismatch: %d columns, %d values",
			len(q.Columns()), len(q.Values()))
	}

	quotedTable, err := pgQuoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	quotedCols := make([]string, len(q.Columns()))
	for i, col := range q.Columns() {
		qc, err := pgQuoteIdent(col)
		if err != nil {
			return "", nil, fmt.Errorf("invalid column name: %w", err)
		}
		quotedCols[i] = qc
	}

	placeholders := make([]string, len(q.Values()))
	var args []any
	idx := 1
	for i, v := range q.Values() {
		if v == nil {
			placeholders[i] = "DEFAULT"
		} else {
			native, err := pgValueToNative(v)
			if err != nil {
				return "", nil, fmt.Errorf("column %q: %w", q.Columns()[i], err)
			}
			placeholders[i] = fmt.Sprintf("$%d", idx)
			args = append(args, native)
			idx++
		}
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quotedTable,
		strings.Join(quotedCols, ", "),
		strings.Join(placeholders, ", "),
	)
	return sql, args, nil
}

// ---------------------------------------------------------------------------
// UPDATE
// ---------------------------------------------------------------------------

// BuildUpdate implements the [Builder] interface.
// Filter may be nil, which generates an UPDATE with no WHERE clause.
func (b *postgresBuilder) BuildUpdate(q UpdateQuery) (string, []any, error) {
	if len(q.Updates()) == 0 {
		return "", nil, fmt.Errorf("UPDATE requires at least one column update")
	}

	quotedTable, err := pgQuoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	setParts := make([]string, len(q.Updates()))
	var args []any
	idx := 1
	for i, u := range q.Updates() {
		qc, err := pgQuoteIdent(u.GetColumn())
		if err != nil {
			return "", nil, fmt.Errorf("invalid column name: %w", err)
		}
		switch u.GetAssignment().(type) {
		case *gocrudv1.ColumnUpdate_UseDefault:
			setParts[i] = fmt.Sprintf("%s = DEFAULT", qc)
		case *gocrudv1.ColumnUpdate_Value:
			native, err := pgValueToNative(u.GetValue())
			if err != nil {
				return "", nil, fmt.Errorf("column %q: %w", u.GetColumn(), err)
			}
			setParts[i] = fmt.Sprintf("%s = $%d", qc, idx)
			args = append(args, native)
			idx++
		default:
			return "", nil, fmt.Errorf("column %q: no value or default specified", u.GetColumn())
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "UPDATE %s SET %s", quotedTable, strings.Join(setParts, ", "))

	if q.Filter() != nil {
		where, whereArgs, _, err := pgBuildFilter(q.Filter(), idx)
		if err != nil {
			return "", nil, err
		}
		if where != "" {
			fmt.Fprintf(&sb, " WHERE %s", where)
			args = append(args, whereArgs...)
		}
	}

	return sb.String(), args, nil
}

// ---------------------------------------------------------------------------
// UPSERT
// ---------------------------------------------------------------------------

// BuildUpsert implements the [Builder] interface.
// It generates INSERT … ON CONFLICT (conflict_columns) DO UPDATE SET … where
// the SET clause is derived automatically: every column that is not a conflict
// column is updated to its EXCLUDED value (the value from the rejected row).
// If all columns are conflict columns there is nothing to update, so the
// statement becomes INSERT … ON CONFLICT (conflict_columns) DO NOTHING —
// effectively a pure insert that silently no-ops on duplicate keys.
func (b *postgresBuilder) BuildUpsert(q UpsertQuery) (string, []any, error) {
	if len(q.Columns()) == 0 {
		return "", nil, fmt.Errorf("UPSERT requires at least one column")
	}
	if len(q.Columns()) != len(q.Values()) {
		return "", nil, fmt.Errorf("columns and values length mismatch: %d columns, %d values",
			len(q.Columns()), len(q.Values()))
	}
	if len(q.ConflictColumns()) == 0 {
		return "", nil, fmt.Errorf("UPSERT requires at least one conflict column")
	}

	quotedTable, err := pgQuoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	// Quote and validate insert columns.
	quotedCols := make([]string, len(q.Columns()))
	for i, col := range q.Columns() {
		qc, err := pgQuoteIdent(col)
		if err != nil {
			return "", nil, fmt.Errorf("invalid column name: %w", err)
		}
		quotedCols[i] = qc
	}

	// Build VALUES placeholders; $N index continues into the SET clause.
	placeholders := make([]string, len(q.Values()))
	var args []any
	idx := 1
	for i, v := range q.Values() {
		if v == nil {
			placeholders[i] = "DEFAULT"
		} else {
			native, err := pgValueToNative(v)
			if err != nil {
				return "", nil, fmt.Errorf("column %q: %w", q.Columns()[i], err)
			}
			placeholders[i] = fmt.Sprintf("$%d", idx)
			args = append(args, native)
			idx++
		}
	}

	// Quote and validate conflict columns.
	quotedConflict := make([]string, len(q.ConflictColumns()))
	for i, col := range q.ConflictColumns() {
		qc, err := pgQuoteIdent(col)
		if err != nil {
			return "", nil, fmt.Errorf("invalid conflict column name: %w", err)
		}
		quotedConflict[i] = qc
	}

	// Derive the SET clause: all non-conflict columns take their EXCLUDED value.
	conflictSet := make(map[string]bool, len(q.ConflictColumns()))
	for _, col := range q.ConflictColumns() {
		conflictSet[col] = true
	}
	var setParts []string
	for i, col := range q.Columns() {
		if !conflictSet[col] {
			setParts = append(setParts, fmt.Sprintf("%s = EXCLUDED.%s", quotedCols[i], quotedCols[i]))
		}
	}
	var sql string
	if len(setParts) == 0 {
		sql = fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO NOTHING",
			quotedTable,
			strings.Join(quotedCols, ", "),
			strings.Join(placeholders, ", "),
			strings.Join(quotedConflict, ", "),
		)
	} else {
		sql = fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
			quotedTable,
			strings.Join(quotedCols, ", "),
			strings.Join(placeholders, ", "),
			strings.Join(quotedConflict, ", "),
			strings.Join(setParts, ", "),
		)
	}
	return sql, args, nil
}

// ---------------------------------------------------------------------------
// DELETE
// ---------------------------------------------------------------------------

// BuildDelete implements the [Builder] interface.
// Returns an error if Filter is nil or resolves to an empty WHERE clause, to
// prevent accidental full-table deletion.
func (b *postgresBuilder) BuildDelete(q DeleteQuery) (string, []any, error) {
	if q.Filter() == nil {
		return "", nil, fmt.Errorf("DELETE requires a WHERE clause to prevent accidental full-table deletion")
	}

	quotedTable, err := pgQuoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	where, args, _, err := pgBuildFilter(q.Filter(), 1)
	if err != nil {
		return "", nil, err
	}
	if where == "" {
		return "", nil, fmt.Errorf("DELETE: filter resolved to an empty WHERE clause; refusing to delete all rows")
	}

	return fmt.Sprintf("DELETE FROM %s WHERE %s", quotedTable, where), args, nil
}

// ---------------------------------------------------------------------------
// SELECT
// ---------------------------------------------------------------------------

// totalCountExpr is appended as the last SELECT column when include_total is
// requested. COUNT(*) OVER() is a window function that returns the total number
// of rows matching the WHERE clause before LIMIT/OFFSET is applied, without
// requiring a separate query.
const totalCountExpr = "COUNT(*) OVER() AS _total_count"

func pgBuildSelect(columns []string, includeTotal bool) (string, error) {
	var base string
	if len(columns) == 0 {
		base = "*"
	} else {
		quoted := make([]string, len(columns))
		for i, c := range columns {
			q, err := pgQuoteIdent(c)
			if err != nil {
				return "", fmt.Errorf("invalid column name: %w", err)
			}
			quoted[i] = q
		}
		base = strings.Join(quoted, ", ")
	}
	if includeTotal {
		return base + ", " + totalCountExpr, nil
	}
	return base, nil
}

// ---------------------------------------------------------------------------
// WHERE
// ---------------------------------------------------------------------------

// pgBuildFilter recursively builds a WHERE-clause fragment.
// idx is the next $N index; returns the clause, args, and updated idx.
func pgBuildFilter(f *gocrudv1.Filter, idx int) (string, []any, int, error) {
	switch k := f.GetFilter().(type) {
	case *gocrudv1.Filter_Condition:
		return pgBuildCondition(k.Condition, idx)
	case *gocrudv1.Filter_Composite:
		return pgBuildComposite(k.Composite, idx)
	default:
		return "", nil, idx, nil
	}
}

func pgBuildCondition(c *gocrudv1.Condition, idx int) (string, []any, int, error) {
	column, err := pgQuoteIdent(c.GetColumn())
	if err != nil {
		return "", nil, idx, fmt.Errorf("invalid column %q: %w", c.GetColumn(), err)
	}

	switch c.GetOp() {
	case gocrudv1.Operator_IS_NULL:
		return fmt.Sprintf("%s IS NULL", column), nil, idx, nil

	case gocrudv1.Operator_IS_NOT_NULL:
		return fmt.Sprintf("%s IS NOT NULL", column), nil, idx, nil

	case gocrudv1.Operator_IN, gocrudv1.Operator_NOT_IN:
		vl := c.GetValues()
		if vl == nil || len(vl.GetValues()) == 0 {
			return "", nil, idx, fmt.Errorf("operator %v requires a non-empty values list for column %q",
				c.GetOp(), c.GetColumn())
		}
		placeholders := make([]string, len(vl.GetValues()))
		var setArgs []any
		for i, v := range vl.GetValues() {
			native, err := pgValueToNative(v)
			if err != nil {
				return "", nil, idx, fmt.Errorf("column %q values[%d]: %w", c.GetColumn(), i, err)
			}
			placeholders[i] = fmt.Sprintf("$%d", idx)
			setArgs = append(setArgs, native)
			idx++
		}
		op := "IN"
		if c.GetOp() == gocrudv1.Operator_NOT_IN {
			op = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", column, op, strings.Join(placeholders, ", ")), setArgs, idx, nil

	default:
		opStr, err := pgOperatorSQL(c.GetOp())
		if err != nil {
			return "", nil, idx, err
		}
		v := c.GetValue()
		if v == nil {
			return "", nil, idx, fmt.Errorf("column %q: operator %v requires a value", c.GetColumn(), c.GetOp())
		}
		native, err := pgValueToNative(v)
		if err != nil {
			return "", nil, idx, fmt.Errorf("column %q: %w", c.GetColumn(), err)
		}
		placeholder := fmt.Sprintf("$%d", idx)
		return fmt.Sprintf("%s %s %s", column, opStr, placeholder), []any{native}, idx + 1, nil
	}
}

func pgBuildComposite(c *gocrudv1.CompositeFilter, idx int) (string, []any, int, error) {
	if len(c.GetFilters()) == 0 {
		return "", nil, idx, nil
	}

	var logicOp string
	switch c.GetOp() {
	case gocrudv1.CompositeFilter_AND:
		logicOp = "AND"
	case gocrudv1.CompositeFilter_OR:
		logicOp = "OR"
	default:
		return "", nil, idx, fmt.Errorf("unsupported logic op %v", c.GetOp())
	}

	var parts []string
	var allArgs []any
	for _, f := range c.GetFilters() {
		part, fArgs, nextIdx, err := pgBuildFilter(f, idx)
		if err != nil {
			return "", nil, idx, err
		}
		if part == "" {
			continue
		}
		parts = append(parts, part)
		allArgs = append(allArgs, fArgs...)
		idx = nextIdx
	}

	switch len(parts) {
	case 0:
		return "", nil, idx, nil
	case 1:
		return parts[0], allArgs, idx, nil
	default:
		return "(" + strings.Join(parts, " "+logicOp+" ") + ")", allArgs, idx, nil
	}
}

// ---------------------------------------------------------------------------
// ORDER BY
// ---------------------------------------------------------------------------

func pgBuildOrderBy(order []*gocrudv1.OrderBy) (string, error) {
	parts := make([]string, len(order))
	for i, o := range order {
		q, err := pgQuoteIdent(o.GetColumn())
		if err != nil {
			return "", fmt.Errorf("invalid order_by column: %w", err)
		}
		dir := "ASC"
		if o.GetDirection() == gocrudv1.OrderBy_DESC {
			dir = "DESC"
		}
		parts[i] = fmt.Sprintf("%s %s", q, dir)
	}
	return strings.Join(parts, ", "), nil
}

// ---------------------------------------------------------------------------
// LIMIT / OFFSET
// ---------------------------------------------------------------------------

func pgBuildLimitOffset(limit int32, offset int64, idx int) (string, []any) {
	sql := fmt.Sprintf("LIMIT $%d", idx)
	args := []any{int64(limit)}
	idx++
	if offset > 0 {
		sql += fmt.Sprintf(" OFFSET $%d", idx)
		args = append(args, offset)
	}
	return sql, args
}

// ---------------------------------------------------------------------------
// Identifier validation and quoting
// ---------------------------------------------------------------------------

// pgQuoteIdent validates name and returns it double-quoted as a SQL identifier.
// Only letters, digits, and underscores are allowed; names must not start with
// a digit. This prevents SQL injection through table/column names, which cannot
// be parameterised.
func pgQuoteIdent(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("identifier must not be empty")
	}
	for i, c := range name {
		if i == 0 && unicode.IsDigit(c) {
			return "", fmt.Errorf("identifier %q must not start with a digit", name)
		}
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' {
			return "", fmt.Errorf("identifier %q contains invalid character %q", name, c)
		}
	}
	return `"` + name + `"`, nil
}

// ---------------------------------------------------------------------------
// Operator mapping
// ---------------------------------------------------------------------------

func pgOperatorSQL(op gocrudv1.Operator) (string, error) {
	switch op {
	case gocrudv1.Operator_EQUAL:
		return "=", nil
	case gocrudv1.Operator_NOT_EQUAL:
		return "!=", nil
	case gocrudv1.Operator_GREATER_THAN:
		return ">", nil
	case gocrudv1.Operator_GREATER_THAN_OR_EQUAL:
		return ">=", nil
	case gocrudv1.Operator_LESS_THAN:
		return "<", nil
	case gocrudv1.Operator_LESS_THAN_OR_EQUAL:
		return "<=", nil
	case gocrudv1.Operator_LIKE:
		return "LIKE", nil
	case gocrudv1.Operator_NOT_LIKE:
		return "NOT LIKE", nil
	default:
		return "", fmt.Errorf("unsupported operator %v", op)
	}
}

// ---------------------------------------------------------------------------
// Value → driver-native conversion
// ---------------------------------------------------------------------------

// pgValueToNative converts a proto [gocrudv1.Value] to a Go type accepted by
// database/sql drivers as a query argument.
func pgValueToNative(v *gocrudv1.Value) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("nil value")
	}
	switch k := v.GetKind().(type) {
	case *gocrudv1.Value_StringValue:
		return k.StringValue, nil
	case *gocrudv1.Value_IntValue:
		return k.IntValue, nil
	case *gocrudv1.Value_DoubleValue:
		return k.DoubleValue, nil
	case *gocrudv1.Value_BoolValue:
		return k.BoolValue, nil
	case *gocrudv1.Value_BytesValue:
		return k.BytesValue, nil
	case *gocrudv1.Value_DecimalValue:
		if k.DecimalValue == nil {
			return nil, fmt.Errorf("nil decimal value")
		}
		// Pass as string; the driver / Postgres cast it to NUMERIC.
		return k.DecimalValue.GetValue(), nil
	case *gocrudv1.Value_TimestampValue:
		if k.TimestampValue == nil {
			return nil, fmt.Errorf("nil timestamp value")
		}
		return k.TimestampValue.AsTime(), nil
	case *gocrudv1.Value_DateValue:
		if k.DateValue == nil {
			return nil, fmt.Errorf("nil date value")
		}
		d := k.DateValue
		return time.Date(int(d.GetYear()), time.Month(d.GetMonth()), int(d.GetDay()),
			0, 0, 0, 0, time.UTC), nil
	case *gocrudv1.Value_TimeValue:
		if k.TimeValue == nil {
			return nil, fmt.Errorf("nil time value")
		}
		t := k.TimeValue
		// "HH:MM:SS.nnnnnnnnn" is accepted by Postgres for TIME columns.
		return fmt.Sprintf("%02d:%02d:%02d.%09d",
			t.GetHours(), t.GetMinutes(), t.GetSeconds(), t.GetNanos()), nil
	case *gocrudv1.Value_IntervalValue:
		if k.IntervalValue == nil {
			return nil, fmt.Errorf("nil interval value")
		}
		// Pass as microseconds; Postgres accepts integer microseconds for INTERVAL.
		return k.IntervalValue.AsDuration().Microseconds(), nil
	default:
		return nil, fmt.Errorf("value has no kind set (zero Value)")
	}
}
