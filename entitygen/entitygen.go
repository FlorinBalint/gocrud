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

// Package entitygen generates protobuf service definitions and request/response
// messages for entities annotated with gocrud entity options.
package entitygen

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

// KeyField describes a field that is part of the entity's primary key.
type KeyField struct {
	// Name is the proto field name (e.g., "user_id").
	Name string
	// Type is the proto type name (e.g., "string", "int64").
	Type string
	// Order is the position in the composite primary key (1-indexed).
	Order int32
}

// templateData holds the data passed to the service generator template.
type templateData struct {
	Name          string
	Package       string
	GoPackage     string
	SourceFile    string
	SnakeName     string
	AllKeys       []KeyField
	ProvidedKeys  []KeyField
	Methods       []entity.Method
	HasEtag       bool
	BasePath      string // collection path for List/Create
	ResPath       string // full resource path for Get/Update/Delete/Upsert
	ResourceType  string
}

// ResourcePath returns the full resource path including PK variables,
// e.g. "/mypackage/v1/users/{org_id=*}/{user_id=*}".
func (d templateData) ResourcePath() string {
	return d.ResPath
}

// deriveBasePath strips the last /{...} segment from a resource path
// to obtain the collection path. For example:
//
//	"accounts/{account_id}/books/{book_id}" → "accounts/{account_id}/books"
func deriveBasePath(resourcePath string) string {
	idx := strings.LastIndex(resourcePath, "/{")
	if idx < 0 {
		return resourcePath
	}
	return resourcePath[:idx]
}

// HasMethod checks if a specific method should be generated.
// If no methods are explicitly specified, all methods are generated.
func (d templateData) HasMethod(m string) bool {
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

// snakeCase converts CamelCase to snake_case.
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

// GenerateServiceProto generates the full .proto file content with CRUD
// messages and service definition for the given entity message descriptor.
func GenerateServiceProto(desc protoreflect.MessageDescriptor) (string, error) {
	if desc == nil {
		return "", fmt.Errorf("entitygen: message descriptor must not be nil")
	}

	name := string(desc.Name())
	if name == "" {
		return "", fmt.Errorf("entitygen: entity name must not be empty")
	}

	isEntity, _ := proto.GetExtension(desc.Options(), entity.E_Entity).(bool)
	if !isEntity {
		return "", fmt.Errorf("entitygen: message %q is not marked as an entity", name)
	}

	file := desc.ParentFile()
	if file == nil {
		return "", fmt.Errorf("entitygen: parent file must not be nil")
	}

	pkg := string(file.Package())
	if pkg == "" {
		return "", fmt.Errorf("entitygen: package must not be empty")
	}

	sourceFile := file.Path()
	if sourceFile == "" {
		return "", fmt.Errorf("entitygen: source file must not be empty")
	}

	fileOpts, ok := file.Options().(*descriptorpb.FileOptions)
	var goPackage string
	if ok && fileOpts != nil {
		goPackage = fileOpts.GetGoPackage()
	}
	if goPackage == "" {
		return "", fmt.Errorf("entitygen: go_package must not be empty")
	}

	var allKeyFields []KeyField
	var manualKeyFields []KeyField
	fields := desc.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		fOpts := f.Options()
		if proto.HasExtension(fOpts, entity.E_PrimaryKey) {
			order, ok := proto.GetExtension(fOpts, entity.E_PrimaryKey).(int32)
			if !ok || order <= 0 {
				continue
			}
			kf := KeyField{
				Name:  string(f.Name()),
				Type:  f.Kind().String(),
				Order: order,
			}
			allKeyFields = append(allKeyFields, kf)

			autogen := proto.GetExtension(fOpts, entity.E_AutoGenerated).(bool)
			if !autogen {
				manualKeyFields = append(manualKeyFields, kf)
			}
		}
	}

	// Sort key fields by order.
	sort.Slice(allKeyFields, func(i, j int) bool { return allKeyFields[i].Order < allKeyFields[j].Order })
	sort.Slice(manualKeyFields, func(i, j int) bool { return manualKeyFields[i].Order < manualKeyFields[j].Order })

	var methods []entity.Method
	if mExt, ok := proto.GetExtension(desc.Options(), entity.E_Methods).([]entity.Method); ok {
		methods = mExt
	}

	var hasEtag bool
	if eExt, ok := proto.GetExtension(desc.Options(), entity.E_HasEtag).(bool); ok {
		hasEtag = eExt
	}

	var basePath, resPath string
	if rp, ok := proto.GetExtension(desc.Options(), entity.E_ResourcePath).(string); ok && rp != "" {
		resPath = rp
		basePath = deriveBasePath(resPath)
	} else {
		pkgPath := strings.ReplaceAll(pkg, ".", "/")
		basePath = "/" + pkgPath + "/" + snakeCase(name) + "s"
		var b strings.Builder
		b.WriteString(basePath)
		for _, k := range allKeyFields {
			b.WriteString("/{")
			b.WriteString(k.Name)
			b.WriteString("=*}")
		}
		resPath = b.String()
	}

	var resourceType string
	if eExt, ok := proto.GetExtension(desc.Options(), entity.E_ResourceType).(string); ok && eExt != "" {
		resourceType = eExt
	} else {
		resourceType = pkg + "/" + name
	}

	data := templateData{
		Name:         name,
		Package:      pkg,
		GoPackage:    goPackage,
		SourceFile:   sourceFile,
		SnakeName:    snakeCase(name),
		AllKeys:      allKeyFields,
		ProvidedKeys: manualKeyFields,
		Methods:      methods,
		HasEtag:      hasEtag,
		BasePath:     basePath,
		ResPath:      resPath,
		ResourceType: resourceType,
	}

	var buf bytes.Buffer
	if err := serviceTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("entitygen: executing template: %w", err)
	}
	return buf.String(), nil
}
