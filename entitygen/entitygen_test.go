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

package entitygen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FlorinBalint/gocrud/entitygen/testdata"
	"github.com/FlorinBalint/gocrud/proto/entity"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading golden file: %v", err)
	}
	return string(data)
}

func TestSnakeCase(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"User", "user"},
		{"UserProfile", "user_profile"},
		{"HTMLParser", "html_parser"},
		{"ID", "id"},
		{"A", "a"},
		{"userID", "user_id"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := snakeCase(tt.in); got != tt.want {
				t.Errorf("snakeCase(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func buildMockMessage(t *testing.T, name, pkg, goPkg, file string, isEntity bool, keys []KeyField) protoreflect.MessageDescriptor {
	t.Helper()

	msgOpts := &descriptorpb.MessageOptions{}
	if isEntity {
		proto.SetExtension(msgOpts, entity.E_Entity, true)
	}

	var fields []*descriptorpb.FieldDescriptorProto
	for i, k := range keys {
		fOpts := &descriptorpb.FieldOptions{}
		if k.Order > 0 {
			proto.SetExtension(fOpts, entity.E_PrimaryKey, k.Order)
		}

		var typ descriptorpb.FieldDescriptorProto_Type
		switch k.Type {
		case "string":
			typ = descriptorpb.FieldDescriptorProto_TYPE_STRING
		case "int64":
			typ = descriptorpb.FieldDescriptorProto_TYPE_INT64
		default:
			typ = descriptorpb.FieldDescriptorProto_TYPE_STRING
		}

		fields = append(fields, &descriptorpb.FieldDescriptorProto{
			Name:    proto.String(k.Name),
			Number:  proto.Int32(int32(i + 1)),
			Type:    typ.Enum(),
			Options: fOpts,
		})
	}

	var fdOpts *descriptorpb.FileOptions
	if goPkg != "" {
		fdOpts = &descriptorpb.FileOptions{
			GoPackage: proto.String(goPkg),
		}
	}

	var msgs []*descriptorpb.DescriptorProto
	if name != "" {
		msgs = append(msgs, &descriptorpb.DescriptorProto{
			Name:    proto.String(name),
			Options: msgOpts,
			Field:   fields,
		})
	}

	fd := &descriptorpb.FileDescriptorProto{
		Name:        proto.String(file),
		Package:     proto.String(pkg),
		Options:     fdOpts,
		MessageType: msgs,
		Dependency:  []string{"proto/entity/options.proto"},
	}

	f, err := protodesc.NewFile(fd, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("building mock file: %v", err)
	}

	if name == "" {
		return nil
	}
	return f.Messages().ByName(protoreflect.Name(name))
}

func TestGenerateServiceProto(t *testing.T) {
	tests := []struct {
		name       string
		descName   string
		descPkg    string
		descGoPkg  string
		descFile   string
		isEntity   bool
		keyFields  []KeyField
		goldenFile string
		wantErr    bool
		passNil    bool
	}{
		{
			name: "composite_key",
			descName:   "User",
			descPkg:    "mypackage.v1",
			descGoPkg:  "github.com/example/mypackage/v1;mypackagev1",
			descFile:   "mypackage/v1/user.proto",
			isEntity:   true,
			keyFields: []KeyField{
				{Name: "org_id", Type: "string", Order: 1},
				{Name: "user_id", Type: "string", Order: 2},
			},
			goldenFile: "user_service.proto",
		},
		{
			name: "single_key",
			descName:   "Book",
			descPkg:    "library.v1",
			descGoPkg:  "github.com/example/library/v1;libraryv1",
			descFile:   "library/v1/book.proto",
			isEntity:   true,
			keyFields: []KeyField{
				{Name: "book_id", Type: "string", Order: 1},
			},
			goldenFile: "book_service.proto",
		},
		{
			name: "keys_sorted_by_order",
			descName:   "User",
			descPkg:    "mypackage.v1",
			descGoPkg:  "github.com/example/mypackage/v1;mypackagev1",
			descFile:   "mypackage/v1/user.proto",
			isEntity:   true,
			keyFields: []KeyField{
				{Name: "user_id", Type: "string", Order: 2},
				{Name: "org_id", Type: "string", Order: 1},
			},
			goldenFile: "user_service.proto",
		},
		{
			name:    "nil_descriptor",
			passNil: true,
			wantErr: true,
		},
		{
			name:      "not_an_entity",
			descName:  "User",
			descPkg:   "mypackage.v1",
			descGoPkg: "github.com/example/mypackage/v1;mypackagev1",
			descFile:  "mypackage/v1/user.proto",
			isEntity:  false,
			wantErr: true,
		},
		{
			name:      "empty_package",
			descName:  "X",
			descPkg:   "",
			descGoPkg: "g",
			descFile:  "f",
			isEntity:  true,
			wantErr: true,
		},
		{
			name:      "empty_go_package",
			descName:  "X",
			descPkg:   "p",
			descGoPkg: "",
			descFile:  "f",
			isEntity:  true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var desc protoreflect.MessageDescriptor
			if !tt.passNil {
				desc = buildMockMessage(t, tt.descName, tt.descPkg, tt.descGoPkg, tt.descFile, tt.isEntity, tt.keyFields)
			}
			got, err := GenerateServiceProto(desc)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			want := readGolden(t, tt.goldenFile)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("output mismatch for %s (-want +got):\n%s", tt.goldenFile, diff)
			}
		})
	}
}

func TestGenerateServiceProto_RealProto(t *testing.T) {
	tests := []struct {
		message string
		golden  string
	}{
		{"RealUser", "realuser_service.proto"},
		{"CreateOnlyUser", "create_only_service.proto"},
		{"GetOnlyUser", "get_only_service.proto"},
		{"ListOnlyUser", "list_only_service.proto"},
		{"UpdateOnlyUser", "update_only_service.proto"},
		{"UpdateWithEtagUser", "update_with_etag_service.proto"},
		{"UpsertOnlyUser", "upsert_only_service.proto"},
	}

	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			desc := testdata.File_entitygen_testdata_entities_proto.Messages().ByName(protoreflect.Name(tt.message))
			if desc == nil {
				t.Fatalf("%s descriptor not found in testdata proto", tt.message)
			}

			got, err := GenerateServiceProto(desc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got == "" {
				t.Error("expected non-empty service proto output")
			}

			want := readGolden(t, tt.golden)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("output mismatch for %s (-want +got):\n%s", tt.golden, diff)
			}
		})
	}
}
