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
	"go/format"
	"sort"
	"strings"
	"text/template"
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

// pkPathSegment maps a URL path segment index to a primary key field.
type pkPathSegment struct {
	// SegmentIdx is the 0-based index into the split URL path segments.
	SegmentIdx int
	// Field is the fieldInfo for this PK.
	Field fieldInfo
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
	// NonPKAutoGenFields lists auto-generated fields that are NOT primary keys.
	NonPKAutoGenFields []fieldInfo
	// InsertFields lists fields included in the INSERT (all except auto-generated).
	InsertFields []fieldInfo
	// AllKeys are all PK fields (provided + auto-generated), sorted by PKOrder.
	AllKeys []fieldInfo
	// UpdateFields are non-PK fields that can be updated.
	UpdateFields []fieldInfo
	// AllNonPKFields are all non-PK fields in proto order (UpdateFields ∪ NonPKAutoGenFields).
	// Used by the update handler to read back the full entity after UPDATE.
	AllNonPKFields []fieldInfo

	// Imports flags for WKTs used in create (autogen fields only).
	HasTimestamp bool
	HasDate      bool
	HasDuration  bool
	HasDecimal   bool
	HasTimeOfDay bool

	// UpdateImport flags for WKTs needed in the update template (all non-PK fields).
	UpdateHasTimestamp bool
	UpdateHasDate      bool
	UpdateHasDuration  bool
	UpdateHasDecimal   bool
	UpdateHasTimeOfDay bool

	// GetResourcePath is the full URL pattern used to parse the name field in Get requests
	// (e.g. "/testdata/v1/real_users/{id=*}").
	GetResourcePath string
	// GetPKSegments describes how to extract each PK from the split URL path.
	GetPKSegments []pkPathSegment
	// NumGetPathSegments is the expected number of path segments in the name field.
	NumGetPathSegments int
	// GetNeedsStrconv is true if any PK field requires strconv for parsing.
	GetNeedsStrconv bool
	// GetPKHas* are WKT import flags for PK fields that need type-specific parsing
	// from URL path segments in the Get handler.
	GetPKHasTimestamp bool
	GetPKHasDate      bool
	GetPKHasDuration  bool
	GetPKHasDecimal   bool
	GetPKHasTimeOfDay bool

	// DeleteResourcePath is the full URL pattern used to parse the name field in Delete requests.
	DeleteResourcePath string
	// DeletePKSegments describes how to extract each PK from the split URL path.
	DeletePKSegments []pkPathSegment
	// NumDeletePathSegments is the expected number of path segments in the name field.
	NumDeletePathSegments int
	// DeleteNeedsStrconv is true if any PK field requires strconv for parsing.
	DeleteNeedsStrconv bool
	// DeletePKHas* are WKT import flags for PK fields in the Delete handler.
	DeletePKHasTimestamp bool
	DeletePKHasDate      bool
	DeletePKHasDuration  bool
	DeletePKHasDecimal   bool
	DeletePKHasTimeOfDay bool

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
	if isEntity, _ := proto.GetExtension(desc.Options(), entity.E_Entity).(bool); !isEntity {
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

	importPath, importAlias := deriveImport(fileOpts.GetGoPackage(), entityImportPath)

	data, err := buildHandlerData(desc, importPath, importAlias)
	if err != nil {
		return nil, err
	}

	if data.HasMethod("GET") {
		if err := populateGetMetadata(desc, &data); err != nil {
			return nil, err
		}
	}

	if data.HasMethod("DELETE") {
		if err := populateDeleteMetadata(desc, &data); err != nil {
			return nil, err
		}
	}

	return renderHandlerFiles(data)
}

// deriveImport extracts the Go import path and package alias from a go_package
// option string ("github.com/foo/bar;barpb" or "github.com/foo/bar").
// entityImportPath overrides the path when non-empty.
func deriveImport(goPackage, entityImportPath string) (importPath, importAlias string) {
	importPath = goPackage
	if idx := strings.Index(goPackage, ";"); idx >= 0 {
		importPath = goPackage[:idx]
		importAlias = goPackage[idx+1:]
	} else {
		parts := strings.Split(goPackage, "/")
		importAlias = parts[len(parts)-1]
	}
	if entityImportPath != "" {
		importPath = entityImportPath
	}
	return importPath, importAlias
}

// buildHandlerData constructs the full handlerData for an entity descriptor,
// covering field extraction, partitioning, key sorting, and WKT import flags.
func buildHandlerData(desc protoreflect.MessageDescriptor, importPath, importAlias string) (handlerData, error) {
	name := string(desc.Name())

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

	allFields, err := extractFields(desc)
	if err != nil {
		return handlerData{}, err
	}

	providedKeys, autoGenFields, nonPKAutoGenFields, insertFields, autoGenPK, err := partitionFields(allFields)
	if err != nil {
		return handlerData{}, err
	}
	sort.Slice(providedKeys, func(i, j int) bool { return providedKeys[i].PKOrder < providedKeys[j].PKOrder })

	var allKeys, updateFields []fieldInfo
	for i := range allFields {
		f := &allFields[i]
		if f.IsPK {
			allKeys = append(allKeys, *f)
		} else {
			updateFields = append(updateFields, *f)
		}
	}
	sort.SliceStable(allKeys, func(i, j int) bool { return allKeys[i].PKOrder < allKeys[j].PKOrder })

	var allNonPKFields []fieldInfo
	for _, f := range allFields {
		if !f.IsPK {
			allNonPKFields = append(allNonPKFields, f)
		}
	}

	autoGenFlags := computeWKTFlags(nonPKAutoGenFields)
	nonPKFlags := computeWKTFlags(allNonPKFields)

	return handlerData{
		EntityName:         name,
		EntityGoType:       importAlias + "." + name,
		Package:            "handlers",
		ImportPath:         importPath,
		ImportAlias:        importAlias,
		Table:              table,
		AllFields:          allFields,
		ProvidedKeys:       providedKeys,
		AutoGenPK:          autoGenPK,
		AutoGenFields:      autoGenFields,
		NonPKAutoGenFields: nonPKAutoGenFields,
		InsertFields:       insertFields,
		AllKeys:            allKeys,
		UpdateFields:       updateFields,
		AllNonPKFields:     allNonPKFields,
		Methods:            methods,
		HasTimestamp:       autoGenFlags.timestamp,
		HasDate:            autoGenFlags.date,
		HasDuration:        autoGenFlags.duration,
		HasDecimal:         autoGenFlags.decimal,
		HasTimeOfDay:       autoGenFlags.timeOfDay,
		UpdateHasTimestamp: nonPKFlags.timestamp,
		UpdateHasDate:      nonPKFlags.date,
		UpdateHasDuration:  nonPKFlags.duration,
		UpdateHasDecimal:   nonPKFlags.decimal,
		UpdateHasTimeOfDay: nonPKFlags.timeOfDay,
	}, nil
}

// extractFields builds a fieldInfo slice from the proto message descriptor.
func extractFields(desc protoreflect.MessageDescriptor) ([]fieldInfo, error) {
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
	return allFields, nil
}

// partitionFields groups fields by their role: provided PKs, auto-generated
// fields, non-PK auto-generated fields, and insert fields.
func partitionFields(allFields []fieldInfo) (
	providedKeys, autoGenFields, nonPKAutoGenFields, insertFields []fieldInfo,
	autoGenPK *fieldInfo, err error,
) {
	for i := range allFields {
		f := &allFields[i]
		if f.IsAutoGen {
			autoGenFields = append(autoGenFields, *f)
			if f.IsPK {
				if autoGenPK != nil {
					return nil, nil, nil, nil, nil, fmt.Errorf(
						"handlersgen: at most one primary key may be auto_generated, found both %q and %q",
						autoGenPK.ProtoName, f.ProtoName,
					)
				}
				cp := *f
				autoGenPK = &cp
			} else {
				nonPKAutoGenFields = append(nonPKAutoGenFields, *f)
			}
		}
		if f.IsPK && !f.IsAutoGen {
			providedKeys = append(providedKeys, *f)
		}
		if !f.IsAutoGen {
			insertFields = append(insertFields, *f)
		}
	}
	return providedKeys, autoGenFields, nonPKAutoGenFields, insertFields, autoGenPK, nil
}

// wktPresence tracks which Well-Known Types appear in a set of fields.
type wktPresence struct {
	timestamp bool
	date      bool
	duration  bool
	decimal   bool
	timeOfDay bool
}

// computeWKTFlags returns which WKTs are present among the given fields.
func computeWKTFlags(fields []fieldInfo) wktPresence {
	var p wktPresence
	for _, f := range fields {
		switch f.GoType {
		case "timestamp":
			p.timestamp = true
		case "date":
			p.date = true
		case "duration":
			p.duration = true
		case "decimal":
			p.decimal = true
		case "timeofday":
			p.timeOfDay = true
		}
	}
	return p
}

// populateGetMetadata derives the URL resource path and PK segment positions,
// then sets all Get-specific fields on data.
func populateGetMetadata(desc protoreflect.MessageDescriptor, data *handlerData) error {
	resPath, err := resourcePathForGet(desc, data.AllKeys)
	if err != nil {
		return err
	}
	pkSegs, numSeg, err := parseResourcePathPKSegments(resPath, data.AllKeys)
	if err != nil {
		return err
	}
	data.GetResourcePath = resPath
	data.GetPKSegments = pkSegs
	data.NumGetPathSegments = numSeg

	pkFlags := computeWKTFlags(data.AllKeys)
	data.GetPKHasTimestamp = pkFlags.timestamp
	data.GetPKHasDate = pkFlags.date
	data.GetPKHasDuration = pkFlags.duration
	data.GetPKHasDecimal = pkFlags.decimal
	data.GetPKHasTimeOfDay = pkFlags.timeOfDay

	for _, k := range data.AllKeys {
		switch k.GoType {
		case "int32", "int64", "uint32", "uint64", "float32", "float64", "bool":
			data.GetNeedsStrconv = true
		}
	}
	return nil
}

// populateDeleteMetadata derives the URL resource path and PK segment positions
// for the Delete handler, then sets all Delete-specific fields on data.
func populateDeleteMetadata(desc protoreflect.MessageDescriptor, data *handlerData) error {
	resPath, err := resourcePathForGet(desc, data.AllKeys)
	if err != nil {
		return err
	}
	pkSegs, numSeg, err := parseResourcePathPKSegments(resPath, data.AllKeys)
	if err != nil {
		return err
	}
	data.DeleteResourcePath = resPath
	data.DeletePKSegments = pkSegs
	data.NumDeletePathSegments = numSeg

	pkFlags := computeWKTFlags(data.AllKeys)
	data.DeletePKHasTimestamp = pkFlags.timestamp
	data.DeletePKHasDate = pkFlags.date
	data.DeletePKHasDuration = pkFlags.duration
	data.DeletePKHasDecimal = pkFlags.decimal
	data.DeletePKHasTimeOfDay = pkFlags.timeOfDay

	for _, k := range data.AllKeys {
		switch k.GoType {
		case "int32", "int64", "uint32", "uint64", "float32", "float64", "bool":
			data.DeleteNeedsStrconv = true
		}
	}
	return nil
}

// renderHandlerFiles executes each enabled method's template and returns the
// formatted Go source files.
func renderHandlerFiles(data handlerData) ([]GeneratedFile, error) {
	entityLower := strings.ToLower(data.EntityName)
	var files []GeneratedFile

	typesContent, err := renderTemplate(typesTmpl, data, "types")
	if err != nil {
		return nil, err
	}
	files = append(files, GeneratedFile{Filename: "types.go", Content: typesContent})

	if data.HasMethod("CREATE") {
		content, err := renderTemplate(createTmpl, data, "create")
		if err != nil {
			return nil, err
		}
		files = append(files, GeneratedFile{Filename: "create_" + entityLower + ".go", Content: content})
	}

	if data.HasMethod("UPDATE") {
		content, err := renderTemplate(updateTmpl, data, "update")
		if err != nil {
			return nil, err
		}
		files = append(files, GeneratedFile{Filename: "update_" + entityLower + ".go", Content: content})
	}

	if data.HasMethod("GET") {
		content, err := renderTemplate(getTmpl, data, "get")
		if err != nil {
			return nil, err
		}
		files = append(files, GeneratedFile{Filename: "get_" + entityLower + ".go", Content: content})
	}

	if data.HasMethod("DELETE") {
		content, err := renderTemplate(deleteTmpl, data, "delete")
		if err != nil {
			return nil, err
		}
		files = append(files, GeneratedFile{Filename: "delete_" + entityLower + ".go", Content: content})
	}

	if data.HasMethod("UPSERT") {
		content, err := renderTemplate(upsertTmpl, data, "upsert")
		if err != nil {
			return nil, err
		}
		files = append(files, GeneratedFile{Filename: "upsert_" + entityLower + ".go", Content: content})
	}

	return files, nil
}

// renderTemplate executes tmpl against data and returns gofmt-formatted source.
func renderTemplate(tmpl *template.Template, data handlerData, name string) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("handlersgen: executing %s template: %w", name, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("handlersgen: formatting %s template output: %w\n%s", name, err, buf.String())
	}
	return string(formatted), nil
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

// resourcePathForGet returns the URL template for the Get RPC, mirroring the
// logic in entitygen. If the entity has a resource_path option, it is used
// as-is. Otherwise the default /pkg/path/entity_names/{pk1=*}/{pk2=*}/...
// is derived.
func resourcePathForGet(desc protoreflect.MessageDescriptor, allKeys []fieldInfo) (string, error) {
	if rp, ok := proto.GetExtension(desc.Options(), entity.E_ResourcePath).(string); ok && rp != "" {
		return rp, nil
	}
	file := desc.ParentFile()
	if file == nil {
		return "", fmt.Errorf("handlersgen: parent file is nil")
	}
	pkg := string(file.Package())
	pkgPath := strings.ReplaceAll(pkg, ".", "/")
	var b strings.Builder
	b.WriteString("/" + pkgPath + "/" + snakeCase(string(desc.Name())) + "s")
	for _, k := range allKeys {
		b.WriteString("/{")
		b.WriteString(k.ProtoName)
		b.WriteString("=*}")
	}
	return b.String(), nil
}

// parseResourcePathPKSegments parses the URL pattern to locate the segment
// index of each primary key. It returns an error if any PK is absent from the
// pattern, satisfying the generator-time validation requirement.
func parseResourcePathPKSegments(resPath string, allKeys []fieldInfo) ([]pkPathSegment, int, error) {
	path := strings.TrimPrefix(resPath, "/")
	segments := strings.Split(path, "/")

	varSegments := make(map[string]int, len(allKeys))
	for i, seg := range segments {
		if len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
			inner := seg[1 : len(seg)-1]
			if eq := strings.Index(inner, "="); eq >= 0 {
				inner = inner[:eq]
			}
			varSegments[inner] = i
		}
	}

	var pkSegs []pkPathSegment
	for _, key := range allKeys {
		idx, ok := varSegments[key.ProtoName]
		if !ok {
			return nil, 0, fmt.Errorf(
				"handlersgen: primary key %q not found in resource path %q; "+
					"all primary keys must appear as {field} or {field=*} segments in the Get URL",
				key.ProtoName, resPath,
			)
		}
		pkSegs = append(pkSegs, pkPathSegment{SegmentIdx: idx, Field: key})
	}
	return pkSegs, len(segments), nil
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
		switch f.Message().FullName() {
		case "google.protobuf.Timestamp", "google.type.Date", "google.type.TimeOfDay":
			return "time.Time"
		case "google.protobuf.Duration":
			return "int64"
		case "google.type.Decimal":
			return "string"
		default:
			// Message-type fields are generally not expected as auto-generated scan targets.
			return "any"
		}
	default:
		return "any"
	}
}

// sqlValueExpr returns the Go expression to create a *gocrudv1.Value from a Go value.
func sqlValueExpr(goType, accessor string) string {
	switch goType {
	case "string":
		return fmt.Sprintf("stringValue(%s)", accessor)
	case "int32", "int64", "uint32", "uint64":
		return fmt.Sprintf("intValue(%s)", accessor)
	case "float32", "float64":
		return fmt.Sprintf("doubleValue(%s)", accessor)
	case "bool":
		return fmt.Sprintf("boolValue(%s)", accessor)
	case "[]byte":
		return fmt.Sprintf("bytesValue(%s)", accessor)
	case "timestamp":
		return fmt.Sprintf("timestampValue(%s)", accessor)
	case "date":
		return fmt.Sprintf("dateValue(%s)", accessor)
	case "duration":
		return fmt.Sprintf("durationValue(%s)", accessor)
	case "decimal":
		return fmt.Sprintf("decimalValue(%s)", accessor)
	case "timeofday":
		return fmt.Sprintf("timeOfDayValue(%s)", accessor)
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
