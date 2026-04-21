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

// Package handlersgen generates Go CRUD handler implementations from protobuf
// entity descriptors. Unlike the reflection-based handlers in the crud package,
// the generated handlers are fully concrete: field access, type conversions,
// and SQL column mappings are all resolved at generation time.
package handlersgen

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/FlorinBalint/gocrud/proto/entity"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// GeneratedFile represents a single generated output file.
type GeneratedFile struct {
	// Filename is the output filename (e.g. "types.go", "create_realuser.go").
	Filename string
	// Content is the full Go source of the file.
	Content string
}

// fieldInfo holds metadata about a single entity field.
type fieldInfo struct {
	// GoName is the PascalCase Go field accessor (e.g. "UserId").
	GoName string
	// ProtoName is the snake_case proto field name (e.g. "user_id").
	ProtoName string
	// ColName is the SQL column name (respects db_column override).
	ColName string
	// GoType is the Go type (e.g. "string", "int64").
	GoType string
	// SQLValueExpr is the Go expression to convert the field to *gocrudv1.Value.
	SQLValueExpr string
	// ScanType is the Go type to use in sql.Scan (e.g. "string", "int64").
	ScanType string
	// SetFromScan is the Go expression to set the proto field from a scanned value.
	// Uses "v" as the scan variable name.
	SetFromScan string
	// IsPK is true if this field is a primary key.
	IsPK bool
	// PKOrder is the primary key order (1-indexed), 0 if not a PK.
	PKOrder int32
	// IsAutoGen is true if the field has auto_generated = true.
	IsAutoGen bool
}

// handlerData holds all data needed to generate handlers for one entity.
type handlerData struct {
	// EntityName is the proto message name (e.g. "Book").
	EntityName string
	// EntityGoType is the qualified Go type for the entity (e.g. "libpb.Book").
	EntityGoType string
	// Package is the Go package name for the generated file.
	Package string
	// ImportPath is the Go import path for the entity's go_proto_library.
	ImportPath string
	// ImportAlias is the Go import alias for the entity package.
	ImportAlias string
	// Table is the SQL table name.
	Table string

	// AllFields lists every field in the entity.
	AllFields []fieldInfo
	// ProvidedKeys are PK fields that the user provides (not auto-generated).
	ProvidedKeys []fieldInfo
	// AutoGenPK is the single auto-generated PK, or nil.
	AutoGenPK *fieldInfo
	// AutoGenFields lists all auto-generated fields (PKs and non-PKs).
	AutoGenFields []fieldInfo
	// InsertFields lists fields included in the INSERT (all except auto-generated).
	InsertFields []fieldInfo

	// Methods lists the CRUD methods to generate handlers for.
	Methods []entity.Method
}

// HasMethod returns true if the given method should be generated.
func (d handlerData) HasMethod(m string) bool {
	if len(d.Methods) == 0 {
		return true
	}
	val, ok := entity.Method_value[m]
	if !ok {
		return false
	}
	target := entity.Method(val)
	for _, method := range d.Methods {
		if method == target {
			return true
		}
	}
	return false
}

// GenerateHandlers produces the Go source files for CRUD handlers of the given entity.
// It returns a types.go file plus one handler file per enabled method
// (e.g. "create_realuser.go").
func GenerateHandlers(desc protoreflect.MessageDescriptor, entityImportPath string) ([]GeneratedFile, error) {
	if desc == nil {
		return nil, fmt.Errorf("handlersgen: message descriptor must not be nil")
	}

	name := string(desc.Name())
	isEntity, _ := proto.GetExtension(desc.Options(), entity.E_Entity).(bool)
	if !isEntity {
		return nil, fmt.Errorf("handlersgen: message %q is not marked as an entity", name)
	}

	file := desc.ParentFile()
	if file == nil {
		return nil, fmt.Errorf("handlersgen: parent file must not be nil")
	}

	fileOpts, ok := file.Options().(*descriptorpb.FileOptions)
	if !ok || fileOpts == nil || fileOpts.GetGoPackage() == "" {
		return nil, fmt.Errorf("handlersgen: go_package must not be empty")
	}
	goPackage := fileOpts.GetGoPackage()

	// Derive import alias from the go_package.
	// "github.com/foo/bar;barpb" → alias "barpb", import "github.com/foo/bar"
	importPath := goPackage
	importAlias := ""
	if idx := strings.Index(goPackage, ";"); idx >= 0 {
		importPath = goPackage[:idx]
		importAlias = goPackage[idx+1:]
	} else {
		// Use last path segment as alias.
		parts := strings.Split(goPackage, "/")
		importAlias = parts[len(parts)-1]
	}
	if entityImportPath != "" {
		importPath = entityImportPath
	}

	table := snakeCase(name)
	if ext := proto.GetExtension(desc.Options(), entity.E_Table); ext != nil {
		if t, ok := ext.(string); ok && t != "" {
			table = t
		}
	}

	var methods []entity.Method
	if mExt, ok := proto.GetExtension(desc.Options(), entity.E_Methods).([]entity.Method); ok {
		methods = mExt
	}

	var allFields []fieldInfo
	fields := desc.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)

		fi := fieldInfo{
			GoName:    fieldGoName(string(f.Name())),
			ProtoName: string(f.Name()),
			ColName:   string(f.Name()),
			GoType:    protoKindToGoType(f),
			ScanType:  protoKindToScanType(f),
		}

		if ext := proto.GetExtension(f.Options(), entity.E_DbColumn); ext != nil {
			if col, ok := ext.(string); ok && col != "" {
				fi.ColName = col
			}
		}

		if order, ok := proto.GetExtension(f.Options(), entity.E_PrimaryKey).(int32); ok && order > 0 {
			fi.IsPK = true
			fi.PKOrder = order
		}

		if autoGen, _ := proto.GetExtension(f.Options(), entity.E_AutoGenerated).(bool); autoGen {
			fi.IsAutoGen = true
		}

		fi.SQLValueExpr = sqlValueExpr(fi.GoType, "entity.Get"+fi.GoName+"()")
		fi.SetFromScan = setFromScanExpr(fi.GoType, fi.GoName)

		allFields = append(allFields, fi)
	}

	// Partition fields.
	var providedKeys, autoGenFields, insertFields []fieldInfo
	var autoGenPK *fieldInfo
	for i := range allFields {
		f := &allFields[i]
		if f.IsAutoGen {
			autoGenFields = append(autoGenFields, *f)
			if f.IsPK {
				if autoGenPK != nil {
					return nil, fmt.Errorf("handlersgen: at most one primary key may be auto_generated, found both %q and %q", autoGenPK.ProtoName, f.ProtoName)
				}
				cp := *f
				autoGenPK = &cp
			}
		}
		if f.IsPK && !f.IsAutoGen {
			providedKeys = append(providedKeys, *f)
		}
		if !f.IsAutoGen {
			insertFields = append(insertFields, *f)
		}
	}

	sort.Slice(providedKeys, func(i, j int) bool { return providedKeys[i].PKOrder < providedKeys[j].PKOrder })

	data := handlerData{
		EntityName:    name,
		EntityGoType:  importAlias + "." + name,
		Package:       "handlers",
		ImportPath:    importPath,
		ImportAlias:   importAlias,
		Table:         table,
		AllFields:     allFields,
		ProvidedKeys:  providedKeys,
		AutoGenPK:     autoGenPK,
		AutoGenFields: autoGenFields,
		InsertFields:  insertFields,
		Methods:       methods,
	}

	var files []GeneratedFile
	var buf bytes.Buffer

	// Generate shared types file.
	if err := typesTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("handlersgen: executing types template: %w", err)
	}
	files = append(files, GeneratedFile{Filename: "types.go", Content: buf.String()})

	entityLower := strings.ToLower(name)

	// Generate per-method handler files.
	if data.HasMethod("CREATE") {
		buf.Reset()
		if err := createTmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("handlersgen: executing create template: %w", err)
		}
		files = append(files, GeneratedFile{
			Filename: "create_" + entityLower + ".go",
			Content:  buf.String(),
		})
	}

	return files, nil
}

// fieldGoName converts a snake_case proto field name to PascalCase Go name,
// matching the protobuf Go codegen convention.
// Underscores before letters are removed (next letter capitalized).
// Underscores before digits are preserved.
// Examples: "user_id" → "UserId", "id_1" → "Id_1", "tenant_id" → "TenantId".
func fieldGoName(name string) string {
	var b strings.Builder
	runes := []rune(name)
	upper := true
	for i, r := range runes {
		if r == '_' {
			// Keep underscore if next char is a digit; otherwise skip and capitalize next.
			if i+1 < len(runes) && unicode.IsDigit(runes[i+1]) {
				b.WriteRune('_')
			} else {
				upper = true
			}
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func snakeCase(s string) string {
	var result strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				if unicode.IsLower(prev) {
					result.WriteRune('_')
				} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
					result.WriteRune('_')
				}
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func protoKindToGoType(f protoreflect.FieldDescriptor) string {
	switch f.Kind() {
	case protoreflect.StringKind:
		return "string"
	case protoreflect.Int32Kind:
		return "int32"
	case protoreflect.Int64Kind:
		return "int64"
	case protoreflect.Uint32Kind:
		return "uint32"
	case protoreflect.Uint64Kind:
		return "uint64"
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.FloatKind:
		return "float32"
	case protoreflect.DoubleKind:
		return "float64"
	case protoreflect.BytesKind:
		return "[]byte"
	case protoreflect.MessageKind:
		switch f.Message().FullName() {
		case "google.protobuf.Timestamp":
			return "timestamp"
		case "google.type.Date":
			return "date"
		case "google.protobuf.Duration":
			return "duration"
		case "google.type.Decimal":
			return "decimal"
		case "google.type.TimeOfDay":
			return "timeofday"
		default:
			return "any"
		}
	default:
		return "any"
	}
}

func protoKindToScanType(f protoreflect.FieldDescriptor) string {
	switch f.Kind() {
	case protoreflect.StringKind:
		return "string"
	case protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		return "int64"
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return "float64"
	case protoreflect.BytesKind:
		return "[]byte"
	case protoreflect.MessageKind:
		// Message-type fields are not expected as auto-generated scan targets.
		return "any"
	default:
		return "any"
	}
}

// sqlValueExpr returns the Go expression to create a *gocrudv1.Value from a Go value.
func sqlValueExpr(goType, accessor string) string {
	switch goType {
	case "string":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_StringValue{StringValue: %s}}", accessor)
	case "int32", "int64", "uint32", "uint64":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_IntValue{IntValue: int64(%s)}}", accessor)
	case "float32", "float64":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_DoubleValue{DoubleValue: float64(%s)}}", accessor)
	case "bool":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_BoolValue{BoolValue: %s}}", accessor)
	case "[]byte":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_BytesValue{BytesValue: %s}}", accessor)
	case "timestamp":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_TimestampValue{TimestampValue: %s}}", accessor)
	case "date":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_DateValue{DateValue: %s}}", accessor)
	case "duration":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_IntervalValue{IntervalValue: %s}}", accessor)
	case "decimal":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_DecimalValue{DecimalValue: %s}}", accessor)
	case "timeofday":
		return fmt.Sprintf("&gocrudv1.Value{Kind: &gocrudv1.Value_TimeValue{TimeValue: %s}}", accessor)
	default:
		return "nil"
	}
}

// setFromScanExpr returns the Go expression to set a proto field from a scanned value v.
func setFromScanExpr(goType, goName string) string {
	switch goType {
	case "string":
		return "entity." + goName + " = v"
	case "int64":
		return "entity." + goName + " = v"
	case "int32":
		return "entity." + goName + " = int32(v)"
	case "uint32":
		return "entity." + goName + " = uint32(v)"
	case "uint64":
		return "entity." + goName + " = uint64(v)"
	default:
		return "entity." + goName + " = v"
	}
}
