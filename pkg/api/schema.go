// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding"
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// An operation that is not one of a resource's generic ones says what it
// takes and gives when it is registered, with the Go values it reads and
// writes; the API's description (openapi.go) turns their types into
// schemas. Nothing is described twice: the struct a handler decodes or
// encodes is the description.
//
//	s.Handle("POST /v1/firewall/unban", true, s.firewallUnban,
//		api.Summary("Lift a ban."), api.Takes(unbanRequest{}), api.ReturnsNothing())

// OpOption describes an operation for GET /v1/openapi.json. Options are
// read when the description is built; they change nothing at run time.
type OpOption func(*opDoc)

// opDoc is what the options say about one operation.
type opDoc struct {
	summary   string
	generic   bool // one of a resource's generic operations (crud.go)
	request   *bodyDoc
	noBody    bool       // declared to take no body
	responses []*bodyDoc // one per status
	params    []paramDoc
}

// bodyDoc is a request or response body. Exactly one of types, ref, table
// or media is set.
type bodyDoc struct {
	status int
	types  []reflect.Type // JSON; more than one = any one of them
	ref    string         // a resource's schema
	table  string         // rows of a table, with extra's fields added
	extra  reflect.Type
	list   bool   // an array of ref or table rows
	media  string // a body that is not JSON
	desc   string
	empty  bool // no body at all (204)
}

type paramDoc struct {
	name, desc string
	typ        reflect.Type
	required   bool
}

func typeOf(v any) reflect.Type {
	if t, ok := v.(reflect.Type); ok {
		return t
	}
	return reflect.TypeOf(v)
}

// Summary is the operation's one-line description.
func Summary(text string) OpOption { return func(d *opDoc) { d.summary = text } }

// Takes declares the JSON request body: v is a value of the type the
// handler decodes (a zero value will do).
func Takes(v any) OpOption {
	return func(d *opDoc) { d.request = &bodyDoc{types: []reflect.Type{typeOf(v)}} }
}

// TakesText declares a request body that is not JSON, such as text/csv.
func TakesText(mediaType, desc string) OpOption {
	return func(d *opDoc) { d.request = &bodyDoc{media: mediaType, desc: desc} }
}

// TakesNothing declares that a POST, PUT or PATCH has no body.
func TakesNothing() OpOption { return func(d *opDoc) { d.noBody = true } }

// Returns declares the JSON answer with its status. With several values
// the answer is any one of them; an operation that answers with more than
// one status says Returns once for each.
func Returns(status int, v ...any) OpOption {
	return func(d *opDoc) {
		b := &bodyDoc{status: status}
		for _, x := range v {
			b.types = append(b.types, typeOf(x))
		}
		d.responses = append(d.responses, b)
	}
}

// ReturnsNothing declares an answer of 204 with no body.
func ReturnsNothing() OpOption {
	return func(d *opDoc) { d.responses = append(d.responses, &bodyDoc{status: 204, empty: true}) }
}

// ReturnsText declares an answer that is not JSON, such as a CSV file.
func ReturnsText(mediaType, desc string) OpOption {
	return func(d *opDoc) { d.responses = append(d.responses, &bodyDoc{status: 200, media: mediaType, desc: desc}) }
}

// ReturnsRow declares an answer that is one row of a resource, as its
// generic operations give it.
func ReturnsRow(status int, resource string) OpOption {
	return func(d *opDoc) { d.responses = append(d.responses, &bodyDoc{status: status, ref: resource}) }
}

// ReturnsRows declares an answer that is a list of a resource's rows.
func ReturnsRows(resource string) OpOption {
	return func(d *opDoc) { d.responses = append(d.responses, &bodyDoc{status: 200, ref: resource, list: true}) }
}

// ReturnsTableRows declares an answer that is a list of rows of a table,
// every column, with the fields of extra (nil for none) added to each.
func ReturnsTableRows(table string, extra any) OpOption {
	return func(d *opDoc) {
		b := &bodyDoc{status: 200, table: table, list: true}
		if extra != nil {
			b.extra = typeOf(extra)
		}
		d.responses = append(d.responses, b)
	}
}

// Query declares a query parameter; v is a value of its type.
func Query(name string, v any, required bool, desc string) OpOption {
	return func(d *opDoc) {
		d.params = append(d.params, paramDoc{name: name, desc: desc, typ: typeOf(v), required: required})
	}
}

// genericOp marks a resource's generic operations, which are described
// from the resource itself.
func genericOp() OpOption { return func(d *opDoc) { d.generic = true } }

// ---- schemas from Go types ----

var (
	timeType     = reflect.TypeOf(time.Time{})
	rawType      = reflect.TypeOf(json.RawMessage{})
	numberType   = reflect.TypeOf(json.Number(""))
	jsonMarshal  = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshal  = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	componentRef = "#/components/schemas/"
)

// schemaGen turns Go types into JSON schemas as encoding/json writes them.
// A named struct becomes a shared component under its type's name (with
// its package's in front when two packages have one of the same name);
// everything else is written in place.
type schemaGen struct {
	// input describes what a request may carry: a field left out of a
	// request decodes to its zero value, so none is required unless tagged
	// `openapi:"required"` (the handler refuses the request without it).
	// Request bodies are written in place, never as components, so that a
	// type is not described both ways under one name.
	input bool
	comps map[string]any          // the components, by name
	names map[reflect.Type]string // the component of each struct type
	taken map[string]reflect.Type // which type holds each name
}

func newSchemaGen(comps map[string]any) *schemaGen {
	return &schemaGen{comps: comps, names: map[reflect.Type]string{}, taken: map[string]reflect.Type{}}
}

// SchemaOf returns the JSON schema of v's type, with every struct it uses
// written in place. Tests use it to check that two types describe the
// same JSON, as where the API passes on the engine's answers.
func SchemaOf(v any) map[string]any {
	g := newSchemaGen(nil)
	return g.schema(typeOf(v))
}

func (g *schemaGen) schema(t reflect.Type) map[string]any {
	nullable := false
	for t.Kind() == reflect.Pointer {
		t, nullable = t.Elem(), true
	}
	s := g.plain(t)
	if nullable && len(s) > 0 { // a schema without a type takes null already
		if _, isRef := s["$ref"]; isRef {
			// Next to $ref every other keyword is ignored in OpenAPI 3.0,
			// and nullable wants a type beside it.
			s = map[string]any{"type": "object", "allOf": []any{s}}
		}
		s["nullable"] = true
	}
	return s
}

func (g *schemaGen) plain(t reflect.Type) map[string]any {
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == rawType:
		return map[string]any{} // any JSON value
	case t == numberType:
		return map[string]any{"type": "number"}
	case t.Implements(jsonMarshal) || reflect.PointerTo(t).Implements(jsonMarshal):
		return map[string]any{}
	case t.Implements(textMarshal) || reflect.PointerTo(t).Implements(textMarshal):
		return map[string]any{"type": "string"}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16:
		return map[string]any{"type": "integer", "format": "int32"}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "format": "int64"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 && t.Kind() == reflect.Slice {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.Struct:
		if t.Name() == "" || g.comps == nil || g.input {
			return g.object(t)
		}
		return map[string]any{"$ref": componentRef + g.component(t)}
	}
	return map[string]any{} // an interface: any JSON value
}

// component names a struct's component, writing it the first time.
func (g *schemaGen) component(t reflect.Type) string {
	if name, ok := g.names[t]; ok {
		return name
	}
	name := cleanName(upperFirst(t.Name()))
	if other, ok := g.taken[name]; ok && other != t {
		pkg := t.PkgPath()
		pkg = pkg[strings.LastIndex(pkg, "/")+1:]
		name = cleanName(upperFirst(pkg) + upperFirst(t.Name()))
	}
	if _, used := g.comps[name]; used && g.taken[name] == nil {
		name += "Body" // a resource or a fixed schema holds the name
	}
	g.names[t], g.taken[name] = name, t
	g.comps[name] = map[string]any{} // a struct that refers to itself finds its name
	g.comps[name] = g.object(t)
	return name
}

// upperFirst names an unexported type's component as an exported one's:
// the name is the description's, not Go's.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// cleanName keeps what a component name may hold (an instance of a
// generic type has brackets and dots in its name).
func cleanName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.') {
			return r
		}
		return '_'
	}, s)
}

// object describes a struct's fields as encoding/json writes them:
// embedded structs without a tag lend their fields, a field without
// omitempty is always there (in an answer). A field tagged `openapi:"ref:<name>"` is
// described by that component, for a json.RawMessage holding a resource's
// row.
func (g *schemaGen) object(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []any
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			ft := f.Type
			if f.Anonymous && name == "" {
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if _, seen := props[name]; seen {
				continue // the shallower field wins, as in encoding/json
			}
			s := g.schema(ft)
			hint := f.Tag.Get("openapi")
			if ref, ok := strings.CutPrefix(hint, "ref:"); ok {
				// JSON the handler passes on as it is (a resource's row,
				// say), described by the schema it names.
				s = map[string]any{"$ref": componentRef + ref}
			}
			if strings.Contains(","+opts+",", ",string,") {
				s = map[string]any{"type": "string"}
			}
			props[name] = s
			always := !strings.Contains(","+opts+",", ",omitempty,") && !strings.Contains(","+opts+",", ",omitzero,")
			if g.input && hint == "required" || !g.input && always {
				required = append(required, name)
			}
		}
	}
	walk(t)
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}
