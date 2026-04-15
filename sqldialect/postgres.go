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

// postgresConfig is the dialect configuration for PostgreSQL: double-quoted
// identifiers and $N positional placeholders.
var postgresConfig = dialectConfig{
	quoteIdent:  pgQuoteIdent,
	placeholder: func(idx int) string { return fmt.Sprintf("$%d", idx) },
}

// postgresBuilder builds PostgreSQL queries. It delegates all shared logic to
// the embedded baseBuilder and implements BuildUpsert using PostgreSQL's
// INSERT … ON CONFLICT syntax.
type postgresBuilder struct {
	baseBuilder
}

var _ Builder = (*postgresBuilder)(nil)

func newPostgresBuilder() *postgresBuilder {
	return &postgresBuilder{baseBuilder: baseBuilder{cfg: postgresConfig}}
}

// BuildUpsert implements [Builder] for PostgreSQL.
// Generates INSERT … ON CONFLICT (conflict_columns) DO UPDATE SET … where
// every non-conflict column is updated to its EXCLUDED value. If all columns
// are conflict columns the statement becomes … DO NOTHING.
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
			native, err := valueToNative(v)
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

// pgQuoteIdent validates name and returns it double-quoted as a PostgreSQL identifier.
func pgQuoteIdent(name string) (string, error) {
	if err := validateIdent(name); err != nil {
		return "", err
	}
	return `"` + name + `"`, nil
}
