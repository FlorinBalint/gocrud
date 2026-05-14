package handlersgen

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/types.go.tmpl
var typesTemplateContent string

//go:embed templates/create.go.tmpl
var createTemplateContent string

//go:embed templates/update.go.tmpl
var updateTemplateContent string

//go:embed templates/get.go.tmpl
var getTemplateContent string

//go:embed templates/delete.go.tmpl
var deleteTemplateContent string

//go:embed templates/upsert.go.tmpl
var upsertTemplateContent string

//go:embed templates/list.go.tmpl
var listTemplateContent string

func setFromScanVarfunc(goType, goName string, idx int, prefix string) string {
		varName := fmt.Sprintf("%s%d", prefix, idx)
		switch goType {
		case "string", "int64":
			return "entity." + goName + " = " + varName
		case "int32":
			return "entity." + goName + " = int32(" + varName + ")"
		case "uint32":
			return "entity." + goName + " = uint32(" + varName + ")"
		case "uint64":
			return "entity." + goName + " = uint64(" + varName + ")"
		case "timestamp":
			return "entity." + goName + " = timestamppb.New(" + varName + ")"
		case "date":
			return "entity." + goName + " = &date.Date{Year: int32(" + varName + ".Year()), Month: int32(" + varName + ".Month()), Day: int32(" + varName + ".Day())}"
		case "duration":
			return "entity." + goName + " = durationpb.New(time.Duration(" + varName + "))"
		case "decimal":
			return "entity." + goName + " = &decimal.Decimal{Value: " + varName + "}"
		case "timeofday":
			return "entity." + goName + " = &timeofday.TimeOfDay{Hours: int32(" + varName + ".Hour()), Minutes: int32(" + varName + ".Minute()), Seconds: int32(" + varName + ".Second()), Nanos: int32(" + varName + ".Nanosecond())}"
		default:
			return "entity." + goName + " = " + varName
		}
}

func castFromInt64(goType, varName string) string {
		switch goType {
		case "string", "int64":
			return varName
		case "int32":
			return "int32(" + varName + ")"
		case "uint32":
			return "uint32(" + varName + ")"
		case "uint64":
			return "uint64(" + varName + ")"
		default:
			return varName
		}
}

func zeroLiteral(goType string) string {
		switch goType {
		case "string":
			return `""`
		case "int32", "int64", "uint32", "uint64":
			return "0"
		case "float32", "float64":
			return "0"
		case "bool":
			return "false"
		default:
			return "nil"
		}
	}

// pkCondition generates a multi-line Go source for a *gocrudv1.Condition literal.
// The indent parameter is the indentation of the opening &gocrudv1.Condition{ line.
func pkCondition(k fieldInfo, indent string) string {
	val := sqlValueExpr(k.GoType, "entity.Get"+k.GoName+"()")
	return fmt.Sprintf("&gocrudv1.Condition{\n"+
		"%s\tColumn: %q,\n"+
		"%s\tOp:     gocrudv1.Operator_EQUAL,\n"+
		"%s\tOperand: &gocrudv1.Condition_Value{Value: %s},\n"+
		"%s}", indent, k.ColName, indent, indent, val, indent)
}

// pkFilter generates Go source code that constructs a *gocrudv1.Filter
// from the given primary key fields. A single key produces a simple Condition;
// multiple keys produce a CompositeFilter with AND.
func pkFilter(keys []fieldInfo) string {
	if len(keys) == 1 {
		return fmt.Sprintf("&gocrudv1.Filter{\n"+
			"\t\tFilter: &gocrudv1.Filter_Condition{\n"+
			"\t\t\tCondition: %s,\n"+
			"\t\t},\n"+
			"\t}", pkCondition(keys[0], "\t\t\t"))
	}
	var entries []string
	for _, k := range keys {
		entries = append(entries, fmt.Sprintf(
			"\t\t\t\t\t{\n"+
				"\t\t\t\t\t\tFilter: &gocrudv1.Filter_Condition{\n"+
				"\t\t\t\t\t\t\tCondition: %s,\n"+
				"\t\t\t\t\t\t},\n"+
				"\t\t\t\t\t}", pkCondition(k, "\t\t\t\t\t\t\t")))
	}
	return "&gocrudv1.Filter{\n" +
		"\t\tFilter: &gocrudv1.Filter_Composite{\n" +
		"\t\t\tComposite: &gocrudv1.CompositeFilter{\n" +
		"\t\t\t\tOp: gocrudv1.CompositeFilter_AND,\n" +
		"\t\t\t\tFilters: []*gocrudv1.Filter{\n" +
		strings.Join(entries, ",\n") + ",\n" +
		"\t\t\t\t},\n" +
		"\t\t\t},\n" +
		"\t\t},\n" +
		"\t}"
}

// parsePKFromStr generates inline Go source that parses a primary key field
// from the URL path segment at segIdx. The generated code sets entity.<GoName>
// from parts[segIdx], performing type conversion as needed.
func parsePKFromStr(f fieldInfo, segIdx int) string {
	seg := fmt.Sprintf("parts[%d]", segIdx)
	switch f.GoType {
	case "string":
		return fmt.Sprintf(
			"if %s == \"\" {\n"+
				"\t\treturn nil, status.Errorf(codes.InvalidArgument, \"missing %s in resource name: %%q\", name)\n"+
				"\t}\n"+
				"\tentity.%s = %s",
			seg, f.ProtoName, f.GoName, seg,
		)
	case "int64":
		return fmt.Sprintf(
			"{\n"+
				"\t\tv, err := strconv.ParseInt(%s, 10, 64)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = v\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "int32":
		return fmt.Sprintf(
			"{\n"+
				"\t\tv, err := strconv.ParseInt(%s, 10, 32)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = int32(v)\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "uint64":
		return fmt.Sprintf(
			"{\n"+
				"\t\tv, err := strconv.ParseUint(%s, 10, 64)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = v\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "uint32":
		return fmt.Sprintf(
			"{\n"+
				"\t\tv, err := strconv.ParseUint(%s, 10, 32)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = uint32(v)\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "float64":
		return fmt.Sprintf(
			"{\n"+
				"\t\tv, err := strconv.ParseFloat(%s, 64)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = v\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "float32":
		return fmt.Sprintf(
			"{\n"+
				"\t\tv, err := strconv.ParseFloat(%s, 32)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = float32(v)\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "bool":
		return fmt.Sprintf(
			"{\n"+
				"\t\tv, err := strconv.ParseBool(%s)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = v\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "timestamp":
		return fmt.Sprintf(
			"{\n"+
				"\t\tt, err := time.Parse(time.RFC3339Nano, %s)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = timestamppb.New(t)\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "date":
		return fmt.Sprintf(
			"{\n"+
				"\t\tt, err := time.Parse(\"2006-01-02\", %s)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = &date.Date{Year: int32(t.Year()), Month: int32(t.Month()), Day: int32(t.Day())}\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "duration":
		return fmt.Sprintf(
			"{\n"+
				"\t\td, err := time.ParseDuration(%s)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = durationpb.New(d)\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	case "decimal":
		return fmt.Sprintf(
			"if %s == \"\" {\n"+
				"\t\treturn nil, status.Errorf(codes.InvalidArgument, \"missing %s in resource name: %%q\", name)\n"+
				"\t}\n"+
				"\tentity.%s = &decimal.Decimal{Value: %s}",
			seg, f.ProtoName, f.GoName, seg,
		)
	case "timeofday":
		return fmt.Sprintf(
			"{\n"+
				"\t\tt, err := time.Parse(\"15:04:05.999999999\", %s)\n"+
				"\t\tif err != nil {\n"+
				"\t\t\treturn nil, status.Errorf(codes.InvalidArgument, \"invalid %s in resource name: %%v\", err)\n"+
				"\t\t}\n"+
				"\t\tentity.%s = &timeofday.TimeOfDay{Hours: int32(t.Hour()), Minutes: int32(t.Minute()), Seconds: int32(t.Second()), Nanos: int32(t.Nanosecond())}\n"+
				"\t}",
			seg, f.ProtoName, f.GoName,
		)
	default:
		return fmt.Sprintf("// unsupported PK type %q for field %s", f.GoType, f.ProtoName)
	}
}

var handlersFuncMap = template.FuncMap{
	"sub":            func(a, b int) int { return a - b },
	"zeroLiteral":    zeroLiteral,
	"setFromScanVar": setFromScanVarfunc,
	"castFromInt64":  castFromInt64,
	"pkFilter":       pkFilter,
	"parsePKFromStr": parsePKFromStr,
}

var typesTmpl = template.Must(template.New("types").Parse(typesTemplateContent))
var createTmpl = template.Must(template.New("create").Funcs(handlersFuncMap).Parse(createTemplateContent))
var updateTmpl = template.Must(template.New("update").Funcs(handlersFuncMap).Parse(updateTemplateContent))
var getTmpl = template.Must(template.New("get").Funcs(handlersFuncMap).Parse(getTemplateContent))
var deleteTmpl = template.Must(template.New("delete").Funcs(handlersFuncMap).Parse(deleteTemplateContent))
var upsertTmpl = template.Must(template.New("upsert").Funcs(handlersFuncMap).Parse(upsertTemplateContent))
var listTmpl = template.Must(template.New("list").Funcs(handlersFuncMap).Parse(listTemplateContent))
