package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"
	"unicode"
)

func CreateTool[T any, K any](description string, toolFunc func(context.Context, T) (K, error)) (CallableTool, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil || t.Kind() != reflect.Struct {
		return CallableTool{}, fmt.Errorf("CreateTool: T must be a struct, got %v", t)
	}

	properties := make(map[string]Property)
	var required []string
	addStructProperties(t, properties, &required)

	name, err := funcName(toolFunc)
	if err != nil {
		return CallableTool{}, err
	}

	return CallableTool{
		Tool: Tool{
			Type: "function",
			Function: Function{
				Name:        snakeCase(name),
				Description: description,
				Parameters: Params{
					Type:       "object",
					Required:   required,
					Properties: properties,
				},
			},
		},
		call: func(ctx context.Context, argsJSON string) (any, error) {
			var args T
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return nil, fmt.Errorf("unmarshal tool args: %w", err)
			}
			return toolFunc(ctx, args)
		},
	}, nil
}

func funcName(fn any) (string, error) {
	pc := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if pc == nil {
		return "", fmt.Errorf("CreateTool: unable to resolve function name")
	}
	full := pc.Name()
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	// Strip compiler-generated suffixes like "-fm" for method values.
	full = strings.TrimSuffix(full, "-fm")
	return full, nil
}

func propertyName(field reflect.StructField) string {
	if tag := field.Tag.Get("json"); tag != "" {
		name, _, _ := strings.Cut(tag, ",")
		if name != "" {
			return name
		}
	}
	return snakeCase(field.Name)
}

func addStructProperties(t reflect.Type, properties map[string]Property, required *[]string) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		et := field.Type
		if et.Kind() == reflect.Pointer {
			et = et.Elem()
		}
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		// Unexported embeds still flatten, matching encoding/json.
		if field.Anonymous && jsonName == "" && et.Kind() == reflect.Struct && jsonSchemaType(et) == "object" {
			addStructProperties(et, properties, required)
			continue
		}
		if field.PkgPath != "" {
			continue
		}

		name := propertyName(field)
		if name == "-" {
			continue
		}
		if _, exists := properties[name]; exists {
			continue
		}

		properties[name] = propertyFromField(field)
		if isRequired(field) {
			*required = append(*required, name)
		}
	}
}

func isRequired(field reflect.StructField) bool {
	if field.Tag.Get("required") == "false" {
		return false
	}
	_, opts, _ := strings.Cut(field.Tag.Get("json"), ",")
	for _, opt := range splitComma(opts) {
		if opt == "omitempty" {
			return false
		}
	}
	switch field.Type.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return false
	}
	return true
}

func propertyFromField(field reflect.StructField) Property {
	prop := Property{
		Type:        jsonSchemaType(field.Type),
		Description: firstTag(field, "desc", "description"),
		Default:     field.Tag.Get("default"),
	}
	if enum := field.Tag.Get("enum"); enum != "" {
		prop.Enum = splitComma(enum)
	}
	if field.Type.Kind() == reflect.Slice || field.Type.Kind() == reflect.Array {
		prop.Items = &Items{Type: jsonSchemaType(field.Type.Elem())}
	}
	return prop
}

func jsonSchemaType(t reflect.Type) string {
	if t == reflect.TypeOf(time.Time{}) {
		return "string"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Pointer:
		return jsonSchemaType(t.Elem())
	default:
		return "string"
	}
}

func firstTag(field reflect.StructField, keys ...string) string {
	for _, key := range keys {
		if v := field.Tag.Get(key); v != "" {
			return v
		}
	}
	return ""
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func snakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
