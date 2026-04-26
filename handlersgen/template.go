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

var handlersFuncMap = template.FuncMap{
	"sub":            func(a, b int) int { return a - b },
	"zeroLiteral":    zeroLiteral,
	"setFromScanVar": setFromScanVarfunc,
	"castFromInt64":  castFromInt64,
	"pkFilter":       pkFilter,
}

var typesTmpl = template.Must(template.New("types").Parse(typesTemplateContent))
var createTmpl = template.Must(template.New("create").Funcs(handlersFuncMap).Parse(createTemplateContent))
var updateTmpl = template.Must(template.New("update").Funcs(handlersFuncMap).Parse(updateTemplateContent))
