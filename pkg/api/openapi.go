// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The API describes itself: GET /v1/openapi.json is built from the routes
// and resources registered in this process, with the columns' types read
// from the database. An extension's resources and routes are in it without
// further work, and it cannot drift from the code.

type routeInfo struct {
	method, path  string
	write, public bool
	doc           opDoc
}

// genericDocs replaces the summary of a resource's generic operation where
// the resource needs a word more than its name.
var genericDocs = map[string]string{
	"GET /v1/audit-log":      "Who changed what, and when, newest first.",
	"GET /v1/audit-log/{id}": "One audit record with the fields that changed.",
}

var jsonTypes = map[string]map[string]any{
	"bigint": {"type": "integer", "format": "int64"}, "integer": {"type": "integer", "format": "int32"},
	"smallint": {"type": "integer", "format": "int32"}, "boolean": {"type": "boolean"},
	"timestamp with time zone": {"type": "string", "format": "date-time"}, "date": {"type": "string", "format": "date"},
	"jsonb": {}, "json": {}, "numeric": {"type": "number"}, "ARRAY": {"type": "array", "items": map[string]any{}},
}

// openAPIDoc keeps the description once it has been built: routes and
// columns do not change while the process runs.
type openAPIDoc struct {
	mu  sync.Mutex
	doc map[string]any
}

func (s *Server) openAPI(w http.ResponseWriter, r *http.Request, c Caller) error {
	s.apiDoc.mu.Lock()
	defer s.apiDoc.mu.Unlock()
	if s.apiDoc.doc == nil {
		doc, err := s.buildOpenAPI(r.Context())
		if err != nil {
			return err
		}
		s.apiDoc.doc = doc
	}
	WriteJSON(w, 200, s.apiDoc.doc)
	return nil
}

func (s *Server) buildOpenAPI(ctx context.Context) (map[string]any, error) {
	// Column types of every table a resource stands on.
	rows, err := s.db.Query(ctx, `select table_name, column_name, data_type, is_nullable = 'YES'
		from information_schema.columns where table_schema = current_schema() order by table_name, ordinal_position`)
	if err != nil {
		return nil, err
	}
	type column struct {
		name, kind string
		nullable   bool
	}
	tables := map[string][]column{}
	for rows.Next() {
		var table string
		var col column
		if err := rows.Scan(&table, &col.name, &col.kind, &col.nullable); err != nil {
			return nil, err
		}
		tables[table] = append(tables[table], col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	colSchema := func(col column) map[string]any {
		p := map[string]any{"type": "string"}
		if t, ok := jsonTypes[col.kind]; ok {
			p = map[string]any{}
			for k, v := range t {
				p[k] = v
			}
		}
		if col.nullable && p["type"] != nil { // a schema without a type takes null already
			p["nullable"] = true
		}
		return p
	}
	schemas := map[string]any{}
	for name, res := range s.resources {
		props := map[string]any{}
		for _, col := range tables[res.Table] {
			if contains(res.Hide, col.name) {
				continue
			}
			p := colSchema(col)
			if res.ReadOnly || !contains(res.Cols, col.name) {
				p["readOnly"] = true
			}
			props[col.name] = p
		}
		schemas[name] = map[string]any{"type": "object", "properties": props}
	}
	gen := newSchemaGen(schemas)
	// Every refusal has the one shape writeErr gives it.
	errType := reflect.TypeOf(ErrorBody{})
	gen.names[errType], gen.taken["Error"] = "Error", errType
	schemas["Error"] = gen.object(errType)

	fail := map[string]any{"description": "Refused; the body says why.",
		"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": componentRef + "Error"}}}}
	body := func(schema map[string]any) map[string]any {
		return map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	query := func(name, desc string, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "in": "query", "description": desc, "schema": schema}
	}
	// describe turns a declared body into its schema and media type.
	in := &schemaGen{input: true}
	describe := func(b *bodyDoc, request bool) (string, map[string]any) {
		var schema map[string]any
		switch {
		case b.media != "":
			return b.media, map[string]any{"type": "string", "format": "binary"}
		case b.ref != "":
			schema = map[string]any{"$ref": componentRef + b.ref}
		case b.table != "":
			props, required := map[string]any{}, []any{}
			for _, col := range tables[b.table] {
				props[col.name] = colSchema(col)
				required = append(required, col.name)
			}
			if b.extra != nil {
				ex := gen.object(b.extra)
				for k, v := range ex["properties"].(map[string]any) {
					props[k] = v
				}
				if more, ok := ex["required"].([]any); ok {
					required = append(required, more...)
				}
			}
			schema = map[string]any{"type": "object", "properties": props, "required": required}
		case len(b.types) == 1 && request:
			schema = in.schema(b.types[0])
		case len(b.types) == 1:
			schema = gen.schema(b.types[0])
		default:
			var alts []any
			for _, t := range b.types {
				alts = append(alts, gen.schema(t))
			}
			schema = map[string]any{"oneOf": alts}
		}
		if b.list {
			schema = map[string]any{"type": "array", "items": schema}
		}
		return "application/json", schema
	}
	text := map[string]any{"type": "string"}
	paths := map[string]any{}
	for _, rt := range s.routes {
		op := map[string]any{"responses": map[string]any{"4XX": fail}, "security": []any{}}
		if !rt.public {
			op["security"] = []any{map[string]any{"bearer": []any{}}}
		}
		if rt.doc.summary != "" {
			op["summary"] = rt.doc.summary
		}
		seg := strings.Split(strings.TrimPrefix(rt.path, "/"), "/")
		if len(seg) > 1 {
			op["tags"] = []any{seg[1]}
		}
		var params []any
		for _, part := range seg {
			if strings.HasPrefix(part, "{") {
				params = append(params, map[string]any{"name": strings.Trim(part, "{}"), "in": "path", "required": true,
					"schema": map[string]any{"type": "integer", "format": "int64"}})
			}
		}
		for _, p := range rt.doc.params {
			q := query(p.name, p.desc, gen.schema(p.typ))
			if p.required {
				q["required"] = true
			}
			params = append(params, q)
		}
		ok := map[string]any{"description": "OK"}
		code := "200"
		// The generic operations of a resource.
		if rt.doc.generic {
			name := seg[1]
			res := s.resources[name]
			ref := map[string]any{"$ref": componentRef + name}
			switch {
			case rt.method == "GET" && len(seg) == 2:
				op["summary"] = "List " + name + ". The X-Total-Count header carries the number of matches where it is counted."
				params = append(params, query("limit", "Page size.", map[string]any{"type": "integer"}),
					query("offset", "Rows to skip.", map[string]any{"type": "integer"}))
				if len(res.Search) > 0 {
					params = append(params, query("q", "Text to look for in: "+strings.Join(res.Search, ", ")+".", text))
				}
				if res.TimeCol != "" {
					params = append(params, query("from", "Earliest "+res.TimeCol+" (RFC 3339).", map[string]any{"type": "string", "format": "date-time"}),
						query("to", "Latest "+res.TimeCol+", exclusive.", map[string]any{"type": "string", "format": "date-time"}))
				}
				filters := append(append([]string{"id"}, res.Cols...), res.Filters...)
				sort.Strings(filters)
				for i, f := range filters {
					if i > 0 && filters[i-1] == f {
						continue
					}
					how := "Only rows with this value."
					if contains(res.Prefix, f) {
						how = "Only rows whose value starts with this."
					}
					params = append(params, query(f, how, text))
				}
				for name, values := range res.Named {
					var enum []any
					for v := range values {
						enum = append(enum, v)
					}
					sort.Slice(enum, func(i, j int) bool { return enum[i].(string) < enum[j].(string) })
					params = append(params, query(name, "A named filter.", map[string]any{"type": "string", "enum": enum}))
				}
				ok = body(map[string]any{"type": "array", "items": ref})
				ok["description"] = "The page of rows."
			case rt.method == "GET":
				op["summary"] = "One of " + name + "."
				ok = body(ref)
				ok["description"] = "The row."
			case rt.method == "POST":
				op["summary"] = "Create one of " + name + "."
				op["requestBody"] = body(ref)
				ok = body(ref)
				ok["description"] = "The row as stored."
				code = "201"
			case rt.method == "PATCH":
				op["summary"] = "Change fields of one of " + name + "."
				op["requestBody"] = body(ref)
				ok = body(ref)
				ok["description"] = "The row as stored."
			case rt.method == "DELETE":
				op["summary"] = "Delete one of " + name + "."
				code, ok = "204", map[string]any{"description": "Done."}
			}
			if doc := genericDocs[rt.method+" "+rt.path]; doc != "" {
				op["summary"] = doc
			}
		}
		if b := rt.doc.request; b != nil {
			media, schema := describe(b, true)
			req := map[string]any{"required": true, "content": map[string]any{media: map[string]any{"schema": schema}}}
			if b.desc != "" {
				req["description"] = b.desc
			}
			op["requestBody"] = req
		}
		if len(rt.doc.responses) == 0 {
			op["responses"].(map[string]any)[code] = ok
		}
		for _, b := range rt.doc.responses {
			ok := map[string]any{"description": "Done."}
			if !b.empty {
				media, schema := describe(b, false)
				ok = map[string]any{"description": "OK", "content": map[string]any{media: map[string]any{"schema": schema}}}
				if b.desc != "" {
					ok["description"] = b.desc
				}
			}
			op["responses"].(map[string]any)[strconv.Itoa(b.status)] = ok
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		op["operationId"] = strings.ToLower(rt.method) + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_", ".", "_").Replace(strings.TrimPrefix(rt.path, "/v1"))
		item, _ := paths[rt.path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[rt.path] = item
		}
		item[strings.ToLower(rt.method)] = op
	}

	info := map[string]any{
		"title":   "Softswitch management API (" + s.edition + ")",
		"version": "1",
		"description": "Generated by the running server from its own routes and resources. Amounts of money are integers in " +
			"micro-units (1/1,000,000 of the currency unit). Send the API key or session token as a bearer token.",
	}
	if s.edition == "community" {
		info["license"] = map[string]any{"name": "AGPL-3.0-only", "url": "https://www.gnu.org/licenses/agpl-3.0.html"}
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info":    info,
		"servers": []any{map[string]any{"url": "/"}},
		"paths":   paths,
		"components": map[string]any{
			"schemas":         schemas,
			"securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}},
		},
	}, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
