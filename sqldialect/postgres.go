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

import "fmt"

// postgresConfig is the dialect configuration for PostgreSQL: double-quoted
// identifiers and $N positional placeholders.
var postgresConfig = dialectConfig{
	quoteIdent:        pgQuoteIdent,
	placeholder:       func(idx int) string { return fmt.Sprintf("$%d", idx) },
	supportsReturning: true,
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
// Delegates to buildOnConflictUpsert, which generates
// INSERT … ON CONFLICT (conflict_columns) DO UPDATE SET … (or DO NOTHING).
func (b *postgresBuilder) BuildUpsert(q UpsertQuery) (string, []any, error) {
	return b.buildOnConflictUpsert(q)
}

// pgQuoteIdent validates name and returns it double-quoted as a PostgreSQL identifier.
func pgQuoteIdent(name string) (string, error) {
	if err := validateIdent(name); err != nil {
		return "", err
	}
	return `"` + name + `"`, nil
}