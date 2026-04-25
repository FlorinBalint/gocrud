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
		t.Fatalf("reading golden file: %v", err)
	}
	return string(data)
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
		}},
		{"AutogenUser", map[string]string{
			"types.go":              "types.go",
			"create_autogenuser.go": "create_autogenuser.go",
			"update_autogenuser.go": "update_autogenuser.go",
		}},
		{"Event", map[string]string{
			"types.go":        "types.go",
			"create_event.go": "create_event.go",
		}},
		{"Product", map[string]string{
			"types.go":           "types.go",
			"create_product.go":  "create_product.go",
			"update_product.go":  "update_product.go",
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
						os.WriteFile(filepath.Join("testdata", goldenName), []byte(f.Content), 0644)
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
