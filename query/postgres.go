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

package query

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

// build implements the [builder] interface.
func (b *postgresBuilder) build(q selectQuery) (string, []any, error) {
	var sb strings.Builder
	var args []any
	idx := 1 // next $N placeholder index

	// SELECT
	selectClause, err := pgBuildSelect(q.fields, q.includeTotal)
	if err != nil {
		return "", nil, err
	}
	quotedTable, err := pgQuoteIdent(q.table)
	if err != nil {
		return "", nil, fmt.Errorf("invalid table name: %w", err)
	}
	fmt.Fprintf(&sb, "SELECT %s FROM %s", selectClause, quotedTable)

	// WHERE
	if q.filter != nil {
		where, whereArgs, nextIdx, err := pgBuildFilter(q.filter, idx)
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
	if len(q.orderBy) > 0 {
		order, err := pgBuildOrderBy(q.orderBy)
		if err != nil {
			return "", nil, err
		}
		fmt.Fprintf(&sb, " ORDER BY %s", order)
	}

	// LIMIT / OFFSET
	limitSQL, limitArgs := pgBuildLimitOffset(q.limit, q.offset, idx)
	fmt.Fprintf(&sb, " %s", limitSQL)
	args = append(args, limitArgs...)

	return sb.String(), args, nil
}

// ---------------------------------------------------------------------------
// SELECT
// ---------------------------------------------------------------------------

// totalCountExpr is appended as the last SELECT column when include_total is
// requested. COUNT(*) OVER() is a window function that returns the total number
// of rows matching the WHERE clause before LIMIT/OFFSET is applied, without
// requiring a separate query.
const totalCountExpr = "COUNT(*) OVER() AS _total_count"

func pgBuildSelect(fields []string, includeTotal bool) (string, error) {
	var base string
	if len(fields) == 0 {
		base = "*"
	} else {
		quoted := make([]string, len(fields))
		for i, f := range fields {
			q, err := pgQuoteIdent(f)
			if err != nil {
				return "", fmt.Errorf("invalid field name: %w", err)
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
	field, err := pgQuoteIdent(c.GetField())
	if err != nil {
		return "", nil, idx, fmt.Errorf("invalid field %q: %w", c.GetField(), err)
	}

	switch c.GetOp() {
	case gocrudv1.Operator_IS_NULL:
		return fmt.Sprintf("%s IS NULL", field), nil, idx, nil

	case gocrudv1.Operator_IS_NOT_NULL:
		return fmt.Sprintf("%s IS NOT NULL", field), nil, idx, nil

	case gocrudv1.Operator_IN, gocrudv1.Operator_NOT_IN:
		vl := c.GetValues()
		if vl == nil || len(vl.GetValues()) == 0 {
			return "", nil, idx, fmt.Errorf("operator %v requires a non-empty values list for field %q",
				c.GetOp(), c.GetField())
		}
		placeholders := make([]string, len(vl.GetValues()))
		var setArgs []any
		for i, v := range vl.GetValues() {
			native, err := pgValueToNative(v)
			if err != nil {
				return "", nil, idx, fmt.Errorf("field %q values[%d]: %w", c.GetField(), i, err)
			}
			placeholders[i] = fmt.Sprintf("$%d", idx)
			setArgs = append(setArgs, native)
			idx++
		}
		op := "IN"
		if c.GetOp() == gocrudv1.Operator_NOT_IN {
			op = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", field, op, strings.Join(placeholders, ", ")), setArgs, idx, nil

	default:
		opStr, err := pgOperatorSQL(c.GetOp())
		if err != nil {
			return "", nil, idx, err
		}
		v := c.GetValue()
		if v == nil {
			return "", nil, idx, fmt.Errorf("field %q: operator %v requires a value", c.GetField(), c.GetOp())
		}
		native, err := pgValueToNative(v)
		if err != nil {
			return "", nil, idx, fmt.Errorf("field %q: %w", c.GetField(), err)
		}
		placeholder := fmt.Sprintf("$%d", idx)
		return fmt.Sprintf("%s %s %s", field, opStr, placeholder), []any{native}, idx + 1, nil
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
		q, err := pgQuoteIdent(o.GetField())
		if err != nil {
			return "", fmt.Errorf("invalid order_by field: %w", err)
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