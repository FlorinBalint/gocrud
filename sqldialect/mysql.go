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
)

// mysqlConfig is the dialect configuration for MySQL: backtick-quoted
// identifiers, ? positional placeholders, and rejection of IntervalValue
// since MySQL has no native INTERVAL column type.
var mysqlConfig = dialectConfig{
	quoteIdent:    myQuoteIdent,
	placeholder:   func(_ int) string { return "?" },
	validateValue: rejectIntervalValue,
}

// mysqlBuilder builds MySQL queries. It delegates all shared logic to the
// embedded baseBuilder and implements BuildUpsert using MySQL's
// INSERT … ON DUPLICATE KEY UPDATE syntax.
type mysqlBuilder struct {
	baseBuilder
}

var _ Builder = (*mysqlBuilder)(nil)

func newMySQLBuilder() *mysqlBuilder {
	return &mysqlBuilder{baseBuilder: baseBuilder{cfg: mysqlConfig}}
}

// BuildUpsert implements [Builder] for MySQL.
// Generates INSERT … ON DUPLICATE KEY UPDATE col = VALUES(col) … for every
// non-conflict column. If all columns are conflict columns, generates
// INSERT IGNORE INTO … instead (no-op on duplicate key).
//
// Note: unlike PostgreSQL's ON CONFLICT (cols), MySQL's ON DUPLICATE KEY UPDATE
// fires on any unique-constraint violation, not a specific conflict target. The
// conflict columns are validated but are not emitted into the SQL.
func (b *mysqlBuilder) BuildUpsert(q UpsertQuery) (string, []any, error) {
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

	// Validate conflict columns (MySQL does not reference them in SQL directly,
	// but we validate eagerly to surface caller mistakes).
	for _, col := range q.ConflictColumns() {
		if _, err := b.cfg.quoteIdent(col); err != nil {
			return "", nil, fmt.Errorf("invalid conflict column name: %w", err)
		}
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

	conflictSet := make(map[string]bool, len(q.ConflictColumns()))
	for _, col := range q.ConflictColumns() {
		conflictSet[col] = true
	}
	var setParts []string
	for i, col := range q.Columns() {
		if !conflictSet[col] {
			// Use the row alias _new to reference the attempted-insert row.
			// This is the preferred syntax since MySQL 8.0.19; VALUES() was
			// deprecated in 8.0.20.
			setParts = append(setParts, fmt.Sprintf("%s = _new.%s", quotedCols[i], quotedCols[i]))
		}
	}

	if len(setParts) == 0 {
		// All columns are conflict columns: use INSERT IGNORE for a no-op on conflict.
		return fmt.Sprintf(
			"INSERT IGNORE INTO %s (%s) VALUES (%s)",
			quotedTable,
			strings.Join(quotedCols, ", "),
			strings.Join(placeholders, ", "),
		), args, nil
	}

	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) AS _new ON DUPLICATE KEY UPDATE %s",
		quotedTable,
		strings.Join(quotedCols, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(setParts, ", "),
	), args, nil
}

// myQuoteIdent validates name and returns it backtick-quoted as a MySQL identifier.
func myQuoteIdent(name string) (string, error) {
	if err := validateIdent(name); err != nil {
		return "", err
	}
	return "`" + name + "`", nil
}
