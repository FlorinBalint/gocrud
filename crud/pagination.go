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

package crud

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"

	gocrudv1 "github.com/FlorinBalint/gocrud/proto/v1"
	"google.golang.org/protobuf/proto"
)

const defaultPageSize int32 = 50

// offsetToken is the internal page position encoded into an opaque page token.
// JSON field names are intentionally short to keep tokens compact.
type offsetToken struct {
	Offset    int64  `json:"o"`
	QueryHash string `json:"qh,omitempty"` // fingerprint of filter + order_by
}

// encodeToken serialises t as a base64url opaque string.
func encodeToken(t offsetToken) string {
	data, _ := json.Marshal(t)
	return base64.URLEncoding.EncodeToString(data)
}

// decodeToken parses an opaque token back to an offsetToken.
func decodeToken(s string) (offsetToken, error) {
	data, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return offsetToken{}, fmt.Errorf("malformed page token: %w", err)
	}
	var t offsetToken
	if err := json.Unmarshal(data, &t); err != nil {
		return offsetToken{}, fmt.Errorf("malformed page token: %w", err)
	}
	if t.Offset < 0 {
		return offsetToken{}, fmt.Errorf("malformed page token: negative offset")
	}
	return t, nil
}

// queryHash returns a hex fingerprint of the filter and order_by fields using
// FNV-1a 64-bit — fast, non-cryptographic, and sufficient for detecting
// accidental query changes between paginated requests.
//
// The hash is computed over:
//   - deterministically marshalled filter bytes (empty if filter is nil)
//   - a 0x00 separator byte
//   - deterministically marshalled bytes of each OrderBy, each separated by 0x00
//
// The separator prevents collisions between (filter=nil, orderBy=[X]) and
// (filter=X_bytes, orderBy=[]).
func queryHash(filter *gocrudv1.Filter, orderBy []*gocrudv1.OrderBy) (string, error) {
	opts := proto.MarshalOptions{Deterministic: true}
	h := fnv.New64a()

	if filter != nil {
		fb, err := opts.Marshal(filter)
		if err != nil {
			return "", fmt.Errorf("marshalling filter: %w", err)
		}
		h.Write(fb)
	}
	h.Write([]byte{0x00}) // separator between filter and order_by

	for _, ob := range orderBy {
		b, err := opts.Marshal(ob)
		if err != nil {
			return "", fmt.Errorf("marshalling order_by: %w", err)
		}
		h.Write(b)
		h.Write([]byte{0x00}) // separator between items
	}

	return hex.EncodeToString(h.Sum(nil)), nil // 16 hex chars (8 bytes)
}

// resolvePageParams extracts the effective offset and page size from the proto
// Pagination message and validates that the query fingerprint in the token
// matches the current request's filter and order_by.
func resolvePageParams(p *gocrudv1.Pagination, currentQueryHash string, cfg Config) (offset int64, pageSize int32, err error) {
	pageSize = defaultPageSize
	if cfg.DefaultPageSize > 0 {
		pageSize = cfg.DefaultPageSize
	}

	if p == nil {
		return 0, pageSize, nil
	}

	if p.GetPageSize() > 0 {
		ps := p.GetPageSize()
		if cfg.MaxPageSize > 0 && ps > cfg.MaxPageSize {
			ps = cfg.MaxPageSize
		}
		pageSize = ps
	}

	if tok := p.GetPageToken(); tok != "" {
		t, err := decodeToken(tok)
		if err != nil {
			return 0, 0, err
		}
		if t.QueryHash != currentQueryHash {
			return 0, 0, fmt.Errorf("filter or order_by has changed since the page token was issued; pagination would be inconsistent")
		}
		offset = t.Offset
	}

	return offset, pageSize, nil
}

// nextPageToken returns the token for the next page, or "" if there is none.
// hasNext is true when the query returned pageSize+1 rows (n+1 trick).
func nextPageToken(currentOffset int64, pageSize int32, hasNext bool, qHash string) string {
	if !hasNext {
		return ""
	}
	return encodeToken(offsetToken{
		Offset:    currentOffset + int64(pageSize),
		QueryHash: qHash,
	})
}

// prevPageToken returns the token for the previous page, or "" on the first page.
func prevPageToken(currentOffset int64, pageSize int32, qHash string) string {
	if currentOffset <= 0 {
		return ""
	}
	prev := currentOffset - int64(pageSize)
	if prev < 0 {
		prev = 0
	}
	return encodeToken(offsetToken{Offset: prev, QueryHash: qHash})
}
