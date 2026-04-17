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

// sqliteConfig is the dialect configuration for SQLite: double-quoted
// identifiers (same as PostgreSQL), ? positional placeholders, and rejection
// of IntervalValue since SQLite has no native INTERVAL type.
var sqliteConfig = dialectConfig{
	quoteIdent:    pgQuoteIdent, // SQLite supports double-quoted identifiers
	placeholder:   func(_ int) string { return "?" },
	validateValue: rejectIntervalValue,
}

// sqliteBuilder builds SQLite queries. SQL syntax (including the ON CONFLICT
// upsert clause) is identical to PostgreSQL; the differences are ? placeholders
// and the absence of a native INTERVAL type.
type sqliteBuilder struct {
	baseBuilder
}

var _ Builder = (*sqliteBuilder)(nil)

func newSQLiteBuilder() *sqliteBuilder {
	return &sqliteBuilder{baseBuilder: baseBuilder{cfg: sqliteConfig}}
}

// BuildUpsert implements [Builder] for SQLite.
// SQLite 3.24+ supports the same INSERT … ON CONFLICT syntax as PostgreSQL.
func (b *sqliteBuilder) BuildUpsert(q UpsertQuery) (string, []any, error) {
	return b.buildOnConflictUpsert(q)
}
