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

// dialectConfig holds the dialect-specific knobs for a SQL builder.
// All other query-building logic is shared across dialects via baseBuilder.
type dialectConfig struct {
	// quoteIdent validates name and returns it quoted as a SQL identifier.
	// e.g., PostgreSQL: `"name"`, MySQL: "`name`".
	quoteIdent func(name string) (string, error)
	// placeholder returns the SQL placeholder for the Nth argument (1-based).
	// e.g., PostgreSQL: "$1", "$2", …; MySQL/SQLite always return "?".
	placeholder func(idx int) string
	// validateValue, when non-nil, is called before converting a proto Value
	// to a driver-native type. Return a non-nil error to reject value kinds
	// that the backend does not support (e.g. SQLite rejects IntervalValue).
	validateValue func(v *gocrudv1.Value) error
}

// baseBuilder contains the shared query-building logic parameterised by a
// dialectConfig. Dialect-specific builders embed this struct and implement
// BuildUpsert themselves, since that syntax differs substantially between
// PostgreSQL (ON CONFLICT … DO UPDATE SET) and MySQL (ON DUPLICATE KEY UPDATE).
type baseBuilder struct {
	cfg dialectConfig
}

// totalCountExpr is appended as the last SELECT column when include_total is
// requested. COUNT(*) OVER() is a window function that returns the total number
// of rows matching the WHERE clause before LIMIT/OFFSET is applied, without
// requiring a separate query. Supported by PostgreSQL and MySQL 8.0+.
const totalCountExpr = "COUNT(*) OVER() AS _total_count"

// toNative runs the dialect's optional validateValue hook and then converts v
// to a driver-native Go type. Use this instead of calling valueToNative directly
// so that per-dialect type restrictions are enforced uniformly.
func (b *baseBuilder) toNative(v *gocrudv1.Value) (any, error) {
	if b.cfg.validateValue != nil {
		if err := b.cfg.validateValue(v); err != nil {
			return nil, err
		}
	}
	return valueToNative(v)
}

// BuildSelect implements [Builder].
func (b *baseBuilder) BuildSelect(q SelectQuery) (string, []any, error) {
	var sb strings.Builder
	var args []any
	idx := 1

	selectClause, err := b.buildSelectClause(q.Columns(), q.IncludeTotal())
	if err != nil {
		return "", nil, err
	}
	quotedTable, err := b.cfg.quoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}
	fmt.Fprintf(&sb, "SELECT %s FROM %s", selectClause, quotedTable)

	if q.Filter() != nil {
		where, whereArgs, nextIdx, err := b.buildFilter(q.Filter(), idx)
		if err != nil {
			return "", nil, err
		}
		if where != "" {
			fmt.Fprintf(&sb, " WHERE %s", where)
			args = append(args, whereArgs...)
			idx = nextIdx
		}
	}

	if len(q.OrderBy()) > 0 {
		order, err := b.buildOrderBy(q.OrderBy())
		if err != nil {
			return "", nil, err
		}
		fmt.Fprintf(&sb, " ORDER BY %s", order)
	}

	limitSQL, limitArgs := b.buildLimitOffset(q.Limit(), q.Offset(), idx)
	fmt.Fprintf(&sb, " %s", limitSQL)
	args = append(args, limitArgs...)

	return sb.String(), args, nil
}

// BuildInsert implements [Builder].
// A nil value entry produces the SQL keyword DEFAULT for that column.
func (b *baseBuilder) BuildInsert(q InsertQuery) (string, []any, error) {
	if len(q.Columns()) == 0 {
		return "", nil, fmt.Errorf("INSERT requires at least one column")
	}
	if len(q.Columns()) != len(q.Values()) {
		return "", nil, fmt.Errorf("columns and values length mismatch: %d columns, %d values",
			len(q.Columns()), len(q.Values()))
	}

	quotedTable, err := b.cfg.quoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	quotedCols := make([]string, len(q.Columns()))
	for i, col := range q.Columns() {
		qc, err := b.cfg.quoteIdent(col)
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
			native, err := b.toNative(v)
			if err != nil {
				return "", nil, fmt.Errorf("column %q: %w", q.Columns()[i], err)
			}
			placeholders[i] = b.cfg.placeholder(idx)
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

// BuildUpdate implements [Builder].
// Filter may be nil, which generates an UPDATE with no WHERE clause.
func (b *baseBuilder) BuildUpdate(q UpdateQuery) (string, []any, error) {
	if len(q.Updates()) == 0 {
		return "", nil, fmt.Errorf("UPDATE requires at least one column update")
	}

	quotedTable, err := b.cfg.quoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	setParts := make([]string, len(q.Updates()))
	var args []any
	idx := 1
	for i, u := range q.Updates() {
		qc, err := b.cfg.quoteIdent(u.GetColumn())
		if err != nil {
			return "", nil, fmt.Errorf("invalid column name: %w", err)
		}
		switch u.GetAssignment().(type) {
		case *gocrudv1.ColumnUpdate_UseDefault:
			setParts[i] = fmt.Sprintf("%s = DEFAULT", qc)
		case *gocrudv1.ColumnUpdate_Value:
			native, err := b.toNative(u.GetValue())
			if err != nil {
				return "", nil, fmt.Errorf("column %q: %w", u.GetColumn(), err)
			}
			setParts[i] = fmt.Sprintf("%s = %s", qc, b.cfg.placeholder(idx))
			args = append(args, native)
			idx++
		default:
			return "", nil, fmt.Errorf("column %q: no value or default specified", u.GetColumn())
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "UPDATE %s SET %s", quotedTable, strings.Join(setParts, ", "))

	if q.Filter() != nil {
		where, whereArgs, _, err := b.buildFilter(q.Filter(), idx)
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

// BuildDelete implements [Builder].
// Returns an error if Filter is nil or resolves to an empty WHERE clause, to
// prevent accidental full-table deletion.
func (b *baseBuilder) BuildDelete(q DeleteQuery) (string, []any, error) {
	if q.Filter() == nil {
		return "", nil, fmt.Errorf("DELETE requires a WHERE clause to prevent accidental full-table deletion")
	}

	quotedTable, err := b.cfg.quoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	where, args, _, err := b.buildFilter(q.Filter(), 1)
	if err != nil {
		return "", nil, err
	}
	if where == "" {
		return "", nil, fmt.Errorf("DELETE: filter resolved to an empty WHERE clause; refusing to delete all rows")
	}

	return fmt.Sprintf("DELETE FROM %s WHERE %s", quotedTable, where), args, nil
}

// ---------------------------------------------------------------------------
// SELECT clause
// ---------------------------------------------------------------------------

func (b *baseBuilder) buildSelectClause(columns []string, includeTotal bool) (string, error) {
	var base string
	if len(columns) == 0 {
		base = "*"
	} else {
		quoted := make([]string, len(columns))
		for i, c := range columns {
			q, err := b.cfg.quoteIdent(c)
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
// WHERE clause
// ---------------------------------------------------------------------------

func (b *baseBuilder) buildFilter(f *gocrudv1.Filter, idx int) (string, []any, int, error) {
	switch k := f.GetFilter().(type) {
	case *gocrudv1.Filter_Condition:
		return b.buildCondition(k.Condition, idx)
	case *gocrudv1.Filter_Composite:
		return b.buildComposite(k.Composite, idx)
	default:
		return "", nil, idx, nil
	}
}

func (b *baseBuilder) buildCondition(c *gocrudv1.Condition, idx int) (string, []any, int, error) {
	column, err := b.cfg.quoteIdent(c.GetColumn())
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
			native, err := b.toNative(v)
			if err != nil {
				return "", nil, idx, fmt.Errorf("column %q values[%d]: %w", c.GetColumn(), i, err)
			}
			placeholders[i] = b.cfg.placeholder(idx)
			setArgs = append(setArgs, native)
			idx++
		}
		op := "IN"
		if c.GetOp() == gocrudv1.Operator_NOT_IN {
			op = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", column, op, strings.Join(placeholders, ", ")), setArgs, idx, nil

	default:
		opStr, err := operatorSQL(c.GetOp())
		if err != nil {
			return "", nil, idx, err
		}
		v := c.GetValue()
		if v == nil {
			return "", nil, idx, fmt.Errorf("column %q: operator %v requires a value", c.GetColumn(), c.GetOp())
		}
		native, err := b.toNative(v)
		if err != nil {
			return "", nil, idx, fmt.Errorf("column %q: %w", c.GetColumn(), err)
		}
		placeholder := b.cfg.placeholder(idx)
		return fmt.Sprintf("%s %s %s", column, opStr, placeholder), []any{native}, idx + 1, nil
	}
}

func (b *baseBuilder) buildComposite(c *gocrudv1.CompositeFilter, idx int) (string, []any, int, error) {
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
		part, fArgs, nextIdx, err := b.buildFilter(f, idx)
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

func (b *baseBuilder) buildOrderBy(order []*gocrudv1.OrderBy) (string, error) {
	parts := make([]string, len(order))
	for i, o := range order {
		q, err := b.cfg.quoteIdent(o.GetColumn())
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

func (b *baseBuilder) buildLimitOffset(limit int32, offset int64, idx int) (string, []any) {
	sql := fmt.Sprintf("LIMIT %s", b.cfg.placeholder(idx))
	args := []any{int64(limit)}
	idx++
	if offset > 0 {
		sql += fmt.Sprintf(" OFFSET %s", b.cfg.placeholder(idx))
		args = append(args, offset)
	}
	return sql, args
}

// ---------------------------------------------------------------------------
// ON CONFLICT upsert (PostgreSQL / SQLite)
// ---------------------------------------------------------------------------

// buildOnConflictUpsert generates INSERT … ON CONFLICT (conflict_columns) DO UPDATE SET …
// using the dialect's quoting and placeholder style. Used by PostgreSQL and SQLite, which
// share this syntax. If all columns are conflict columns the statement uses DO NOTHING.
func (b *baseBuilder) buildOnConflictUpsert(q UpsertQuery) (string, []any, error) {
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

	quotedTable, err := b.cfg.quoteIdent(q.Table())
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}

	quotedCols := make([]string, len(q.Columns()))
	for i, col := range q.Columns() {
		qc, err := b.cfg.quoteIdent(col)
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
			native, err := b.toNative(v)
			if err != nil {
				return "", nil, fmt.Errorf("column %q: %w", q.Columns()[i], err)
			}
			placeholders[i] = b.cfg.placeholder(idx)
			args = append(args, native)
			idx++
		}
	}

	quotedConflict := make([]string, len(q.ConflictColumns()))
	for i, col := range q.ConflictColumns() {
		qc, err := b.cfg.quoteIdent(col)
		if err != nil {
			return "", nil, fmt.Errorf("invalid conflict column name: %w", err)
		}
		quotedConflict[i] = qc
	}

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

	colList := strings.Join(quotedCols, ", ")
	valList := strings.Join(placeholders, ", ")
	conflictList := strings.Join(quotedConflict, ", ")

	if len(setParts) == 0 {
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO NOTHING",
			quotedTable, colList, valList, conflictList), args, nil
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		quotedTable, colList, valList, conflictList, strings.Join(setParts, ", ")), args, nil
}

// ---------------------------------------------------------------------------
// Identifier validation
// ---------------------------------------------------------------------------

// validateIdent checks that name is a valid SQL identifier: non-empty,
// containing only letters, digits, and underscores, and not starting with a
// digit. This prevents SQL injection through table/column names, which cannot
// be parameterised.
func validateIdent(name string) error {
	if name == "" {
		return fmt.Errorf("identifier must not be empty")
	}
	for i, c := range name {
		if i == 0 && unicode.IsDigit(c) {
			return fmt.Errorf("identifier %q must not start with a digit", name)
		}
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' {
			return fmt.Errorf("identifier %q contains invalid character %q", name, c)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Operator mapping
// ---------------------------------------------------------------------------

func operatorSQL(op gocrudv1.Operator) (string, error) {
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

// rejectIntervalValue is a validateValue hook for backends that have no native
// INTERVAL column type (MySQL, SQLite). PostgreSQL supports INTERVAL natively
// so it does not set this hook.
func rejectIntervalValue(v *gocrudv1.Value) error {
	if _, ok := v.GetKind().(*gocrudv1.Value_IntervalValue); ok {
		return fmt.Errorf("this backend does not support INTERVAL values; " +
			"store the duration as an IntValue (microseconds) or StringValue instead")
	}
	return nil
}

// valueToNative converts a proto [gocrudv1.Value] to a Go type accepted by
// database/sql drivers as a query argument.
func valueToNative(v *gocrudv1.Value) (any, error) {
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
		// Pass as string; the driver / database casts it to NUMERIC/DECIMAL.
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
		// "HH:MM:SS.nnnnnnnnn" is accepted by PostgreSQL for TIME columns.
		// MySQL accepts up to microsecond precision; nanoseconds are truncated by the driver.
		return fmt.Sprintf("%02d:%02d:%02d.%09d",
			t.GetHours(), t.GetMinutes(), t.GetSeconds(), t.GetNanos()), nil
	case *gocrudv1.Value_IntervalValue:
		if k.IntervalValue == nil {
			return nil, fmt.Errorf("nil interval value")
		}
		// Pass as microseconds; PostgreSQL accepts integer microseconds for INTERVAL.
		return k.IntervalValue.AsDuration().Microseconds(), nil
	default:
		return nil, fmt.Errorf("value has no kind set (zero Value)")
	}
}
