package handlersgen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FlorinBalint/gocrud/entitygen/testdata"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		if os.IsNotExist(err) && os.Getenv("UPDATE_GOLDEN") == "1" {
			return "" // golden will be written by the caller
		}
		t.Fatalf("reading golden file: %v", err)
	}
	return string(data)
}

func TestParseResourcePathPKSegments(t *testing.T) {
	sk := func(name string) fieldInfo { return fieldInfo{ProtoName: name, GoType: "string"} }

	tests := []struct {
		name        string
		path        string
		keys        []fieldInfo
		wantErr     bool
		wantNum     int
		wantIndices []int // expected SegmentIdx for each key in input order
	}{
		{
			name:        "single PK with =* syntax",
			path:        "/v1/users/{id=*}",
			keys:        []fieldInfo{sk("id")},
			wantNum:     3,
			wantIndices: []int{2},
		},
		{
			name:        "single PK without =* suffix",
			path:        "/api/v2/accounts/{account_id}",
			keys:        []fieldInfo{sk("account_id")},
			wantNum:     4,
			wantIndices: []int{3},
		},
		{
			name:        "composite PKs at non-contiguous segments",
			path:        "/api/v2/accounts/{account_id}/custom_users/{id}",
			keys:        []fieldInfo{sk("account_id"), sk("id")},
			wantNum:     6,
			wantIndices: []int{3, 5},
		},
		{
			name:        "composite PKs contiguous with mixed syntax",
			path:        "/testdata/v1/checkpoints/{job_id=*}/{checkpoint_at=*}",
			keys:        []fieldInfo{sk("job_id"), sk("checkpoint_at")},
			wantNum:     5,
			wantIndices: []int{3, 4},
		},
		{
			name:    "one PK present but second missing",
			path:    "/v1/accounts/{account_id}/users",
			keys:    []fieldInfo{sk("account_id"), sk("user_id")},
			wantErr: true,
		},
		{
			name:    "no variable segments at all",
			path:    "/v1/users/static",
			keys:    []fieldInfo{sk("id")},
			wantErr: true,
		},
		{
			name:        "empty key list always succeeds",
			path:        "/v1/users",
			keys:        nil,
			wantNum:     2,
			wantIndices: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segs, num, err := parseResourcePathPKSegments(tt.path, tt.keys)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (segs=%v)", segs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if num != tt.wantNum {
				t.Errorf("num segments = %d, want %d", num, tt.wantNum)
			}
			if len(segs) != len(tt.wantIndices) {
				t.Fatalf("got %d segments, want %d", len(segs), len(tt.wantIndices))
			}
			for i, seg := range segs {
				if seg.SegmentIdx != tt.wantIndices[i] {
					t.Errorf("segs[%d].SegmentIdx = %d, want %d (field %q)",
						i, seg.SegmentIdx, tt.wantIndices[i], seg.Field.ProtoName)
				}
				if seg.Field.ProtoName != tt.keys[i].ProtoName {
					t.Errorf("segs[%d].Field.ProtoName = %q, want %q",
						i, seg.Field.ProtoName, tt.keys[i].ProtoName)
				}
			}
		})
	}
}

func TestFieldGoName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"id", "Id"},
		{"user_id", "UserId"},
		{"id_1", "Id_1"},
		{"tenant_id", "TenantId"},
		{"email", "Email"},
		{"account_id", "AccountId"},
		{"id_2", "Id_2"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := fieldGoName(tt.in); got != tt.want {
				t.Errorf("fieldGoName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGenerateHandlers_RealProto(t *testing.T) {
	tests := []struct {
		message string
		goldens map[string]string // generated filename -> golden filename
	}{
		{"RealUser", map[string]string{
			"types.go":           "types.go",
			"create_realuser.go": "create_realuser.go",
			"update_realuser.go": "update_realuser.go",
			"get_realuser.go":    "get_realuser.go",
			"delete_realuser.go": "delete_realuser.go",
			"upsert_realuser.go": "upsert_realuser.go",
			"list_realuser.go":   "list_realuser.go",
		}},
		{"AutogenUser", map[string]string{
			"types.go":              "types.go",
			"create_autogenuser.go": "create_autogenuser.go",
			"update_autogenuser.go": "update_autogenuser.go",
			"get_autogenuser.go":    "get_autogenuser.go",
			"delete_autogenuser.go": "delete_autogenuser.go",
			"upsert_autogenuser.go": "upsert_autogenuser.go",
			"list_autogenuser.go":   "list_autogenuser.go",
		}},
		{"Event", map[string]string{
			"types.go":        "types.go",
			"create_event.go": "create_event.go",
		}},
		{"Product", map[string]string{
			"types.go":          "types.go",
			"create_product.go": "create_product.go",
			"update_product.go": "update_product.go",
		}},
		{"AuditLog", map[string]string{
			"types.go":           "types.go",
			"create_auditlog.go": "create_auditlog.go",
		}},
		// GetOnlyUser: GET-only entity, single string PK, no non-PK fields.
		{"GetOnlyUser", map[string]string{
			"types.go":            "types.go",
			"get_getonlyuser.go":  "get_getonlyuser.go",
		}},
		// OptionsOverrideUser: custom resource_path, composite string PKs, no non-PK fields.
		{"OptionsOverrideUser", map[string]string{
			"types.go":                         "types.go",
			"create_optionsoverrideuser.go":    "create_optionsoverrideuser.go",
			"update_optionsoverrideuser.go":    "update_optionsoverrideuser.go",
			"get_optionsoverrideuser.go":       "get_optionsoverrideuser.go",
			"delete_optionsoverrideuser.go":    "delete_optionsoverrideuser.go",
			"upsert_optionsoverrideuser.go":    "upsert_optionsoverrideuser.go",
			"list_optionsoverrideuser.go":      "list_optionsoverrideuser.go",
		}},
		// DeleteOnlyUser: DELETE-only entity, single string PK.
		{"DeleteOnlyUser", map[string]string{
			"types.go":                  "types.go",
			"delete_deleteonlyuser.go":  "delete_deleteonlyuser.go",
		}},
		// ListOnlyUser: LIST-only entity, single string PK.
		{"ListOnlyUser", map[string]string{
			"types.go":               "types.go",
			"list_listonlyuser.go":   "list_listonlyuser.go",
		}},
		// UpsertOnlyUser: UPSERT-only entity, single string PK.
		{"UpsertOnlyUser", map[string]string{
			"types.go":                    "types.go",
			"upsert_upsertonlyuser.go":    "upsert_upsertonlyuser.go",
		}},
		// ListUpsertDeleteWithEtagUser: LIST+UPSERT+DELETE entity with etag.
		{"ListUpsertDeleteWithEtagUser", map[string]string{
			"types.go":                                                "types.go",
			"list_listupsertdeletewithetaguser.go":                     "list_listupsertdeletewithetaguser.go",
			"delete_listupsertdeletewithetaguser.go":                   "delete_listupsertdeletewithetaguser.go",
			"upsert_listupsertdeletewithetaguser.go":                   "upsert_listupsertdeletewithetaguser.go",
		}},
		// Checkpoint: GET-only entity with a composite PK where one part is a Timestamp.
		// Exercises RFC-3339 parsing of a Timestamp key from the resource name.
		{"Checkpoint", map[string]string{
			"types.go":            "types.go",
			"get_checkpoint.go":   "get_checkpoint.go",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			desc := testdata.File_entitygen_testdata_entities_proto.Messages().ByName(protoreflect.Name(tt.message))
			if desc == nil {
				t.Fatalf("%s descriptor not found in testdata proto", tt.message)
			}

			files, err := GenerateHandlers(desc, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(files) == 0 {
				t.Fatal("expected non-empty handler output")
			}

			for i := 0; i < desc.Fields().Len(); i++ {
				f := desc.Fields().Get(i)
				if f.Message() != nil {
					t.Logf("Field %s has FullName: %s", f.Name(), f.Message().FullName())
				}
			}

			// Check each generated file against its golden.
			for _, f := range files {
				goldenName, ok := tt.goldens[f.Filename]
				if !ok {
					t.Errorf("unexpected generated file: %s", f.Filename)
					continue
				}

				want := readGolden(t, goldenName)
				if diff := cmp.Diff(want, f.Content); diff != "" {
					if os.Getenv("UPDATE_GOLDEN") == "1" {
						dir := "testdata"
						if wd := os.Getenv("BUILD_WORKSPACE_DIRECTORY"); wd != "" {
							dir = filepath.Join(wd, "handlersgen", "testdata")
						}
						os.WriteFile(filepath.Join(dir, goldenName), []byte(f.Content), 0644)
						t.Logf("updated golden file: %s", goldenName)
					} else {
						t.Errorf("output mismatch for %s (-want +got):\n%s", f.Filename, diff)
					}
				}
			}

			// Check we got all expected files.
			generated := make(map[string]bool)
			for _, f := range files {
				generated[f.Filename] = true
			}
			for name := range tt.goldens {
				if !generated[name] {
					t.Errorf("expected generated file %s not found", name)
				}
			}
		})
	}
}
