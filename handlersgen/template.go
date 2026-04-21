package handlersgen

import (
	_ "embed"
	"fmt"
	"text/template"
)

//go:embed templates/types.go.tmpl
var typesTemplateContent string

//go:embed templates/create.go.tmpl
var createTemplateContent string

func setFromScanVarfunc(goType, goName string, idx int) string {
		varName := fmt.Sprintf("autoGen%d", idx)
		switch goType {
		case "string", "int64":
			return "entity." + goName + " = " + varName
		case "int32":
			return "entity." + goName + " = int32(" + varName + ")"
		case "uint32":
			return "entity." + goName + " = uint32(" + varName + ")"
		case "uint64":
			return "entity." + goName + " = uint64(" + varName + ")"
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

var handlersFuncMap = template.FuncMap{
	"sub": func(a, b int) int { return a - b },
	"zeroLiteral": zeroLiteral,
	"setFromScanVar": setFromScanVarfunc,
	"castFromInt64": castFromInt64,
}

var typesTmpl = template.Must(template.New("types").Parse(typesTemplateContent))
var createTmpl = template.Must(template.New("create").Funcs(handlersFuncMap).Parse(createTemplateContent))
