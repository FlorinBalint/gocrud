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

import gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"

// selectQuery is the fully-resolved internal representation of a SELECT that is
// built by [Service.List] after resolving pagination, then handed to a [selectBuilder].
type selectQuery struct {
	table        string
	fields       []string            // nil/empty → SELECT *
	filter       *gocrudv1.Filter    // nil → no WHERE clause
	orderBy      []*gocrudv1.OrderBy // nil/empty → no ORDER BY
	limit        int32               // rows to fetch (already includes n+1 for has-next detection)
	offset       int64
	includeTotal bool // when true, appends COUNT(*) OVER() as the last SELECT column
}

// selectBuilder translates a [selectQuery] into a SQL SELECT string and positional arguments.
// Implementations must:
//   - Validate all identifier names (table, columns) to prevent SQL injection.
//   - Use $1, $2, … positional placeholders for all values.
//   - Never interpolate values into the SQL string.
type selectBuilder interface {
	build(q selectQuery) (sql string, args []any, err error)
}