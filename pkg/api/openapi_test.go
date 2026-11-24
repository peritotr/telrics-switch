// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every operation that is not a resource's generic one says what it takes
// and gives; a new route registered without it fails here.
func TestEveryOperationDescribed(t *testing.T) {
	s := New(nil)
	n := 0
	for _, rt := range s.routes {
		if rt.doc.generic {
			continue
		}
		n++
		op := rt.method + " " + rt.path
		if rt.doc.summary == "" {
			t.Errorf("%s: no summary", op)
		}
		if len(rt.doc.responses) == 0 {
			t.Errorf("%s: no answer declared (Returns, ReturnsNothing, ReturnsText, ReturnsRow...)", op)
		}
		switch rt.method {
		case "POST", "PUT", "PATCH":
			if rt.doc.request == nil && !rt.doc.noBody {
				t.Errorf("%s: neither Takes nor TakesNothing", op)
			}
		default:
			if rt.doc.request != nil || rt.doc.noBody {
				t.Errorf("%s: a %s declares a body", op, rt.method)
			}
		}
		for _, b := range rt.doc.responses {
			for _, typ := range b.types {
				if s := SchemaOf(typ); len(s) == 0 {
					t.Errorf("%s: answer of type %v is described as any value", op, typ)
				}
			}
		}
	}
	t.Logf("%d operations of the core's own, every one described", n)
	if n < 50 {
		t.Errorf("only %d operations of the core's own", n)
	}
}

func TestSchemaOf(t *testing.T) {
	type inner struct {
		A string `json:"a"`
	}
	type Base struct {
		ID int64 `json:"id"`
	}
	type sample struct {
		Base
		Name    string           `json:"name"`
		Note    string           `json:"note,omitempty"`
		Amount  int64            `json:"amount"`
		Small   int              `json:"small"`
		Count   uint16           `json:"count"`
		At      time.Time        `json:"at"`
		Maybe   *time.Time       `json:"maybe"`
		Tags    []string         `json:"tags"`
		By      map[string]int64 `json:"by"`
		Raw     json.RawMessage  `json:"raw"`
		Inner   inner            `json:"inner"`
		Ptr     *inner           `json:"ptr,omitempty"`
		Num     json.Number      `json:"num"`
		Quoted  int64            `json:"quoted,string"`
		Skipped string           `json:"-"`
		Free    map[string]any   `json:"free"`
		Untag   bool
		Nested  map[string][]bool `json:"nested"`
	}
	got := SchemaOf(sample{})
	want := `{"properties":{"Untag":{"type":"boolean"},"amount":{"format":"int64","type":"integer"},"at":{"format":"date-time","type":"string"},` +
		`"by":{"additionalProperties":{"format":"int64","type":"integer"},"type":"object"},"count":{"format":"int32","type":"integer"},` +
		`"free":{"additionalProperties":{},"type":"object"},"id":{"format":"int64","type":"integer"},` +
		`"inner":{"properties":{"a":{"type":"string"}},"required":["a"],"type":"object"},` +
		`"maybe":{"format":"date-time","nullable":true,"type":"string"},"name":{"type":"string"},` +
		`"nested":{"additionalProperties":{"items":{"type":"boolean"},"type":"array"},"type":"object"},"note":{"type":"string"},` +
		`"num":{"type":"number"},"ptr":{"nullable":true,"properties":{"a":{"type":"string"}},"required":["a"],"type":"object"},` +
		`"quoted":{"type":"string"},"raw":{},"small":{"format":"int64","type":"integer"},"tags":{"items":{"type":"string"},"type":"array"}},` +
		`"required":["id","name","amount","small","count","at","maybe","tags","by","raw","inner","num","quoted","free","Untag","nested"],"type":"object"}`
	if b, _ := json.Marshal(got); string(b) != want {
		t.Errorf("schema:\n got %s\nwant %s", b, want)
	}

	// Named structs become shared components; a name two packages share is
	// told apart by the package.
	comps := map[string]any{"clients": map[string]any{}}
	g := newSchemaGen(comps)
	type Registration struct {
		X int `json:"x"`
	}
	a := g.schema(reflect.TypeOf([]Registration{}))
	b := g.schema(reflect.TypeOf(&struct {
		R Registration `json:"r"`
	}{}))
	c := g.schema(reflect.TypeOf(Registration{}))
	if a["items"].(map[string]any)["$ref"] != componentRef+"Registration" || c["$ref"] != componentRef+"Registration" {
		t.Errorf("component refs: %v %v", a, c)
	}
	if b["nullable"] != true {
		t.Errorf("pointer to an anonymous struct: %v", b)
	}
	if g.schema(reflect.TypeOf(&Registration{}))["allOf"] == nil {
		t.Error("a nullable component is not wrapped in allOf")
	}
	g.schema(reflect.TypeOf(Registration2{}))
	names := make([]string, 0, len(comps))
	for k := range comps {
		names = append(names, k)
	}
	sort.Strings(names)
	if strings.Join(names, " ") != "Registration Registration2 clients" {
		t.Errorf("components: %v", names)
	}
}

// Registration2 is a second named type for TestSchemaOf.
type Registration2 struct{ Y string }

// TestOpenAPIDocument builds the description against a fresh schema and
// checks that every operation of the core's own has its bodies described.
// With OPENAPI_OUT set it writes the document there (make test-openapi
// lints it).
func TestOpenAPIDocument(t *testing.T) {
	ctx, _, srv, _, _ := freshServer(t)
	doc, err := srv.buildOpenAPI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	ops, withBody := 0, 0
	for _, rt := range srv.routes {
		ops++
		op := paths[rt.path].(map[string]any)[strings.ToLower(rt.method)].(map[string]any)
		if op["summary"] == nil {
			t.Errorf("%s %s: no summary", rt.method, rt.path)
		}
		responses := op["responses"].(map[string]any)
		var answer map[string]any
		for code, r := range responses {
			if code[0] == '2' {
				answer = r.(map[string]any)
			}
		}
		if answer == nil {
			t.Errorf("%s %s: no answer", rt.method, rt.path)
			continue
		}
		if _, ok := responses["204"]; !ok && answer["content"] == nil {
			t.Errorf("%s %s: the answer has no schema", rt.method, rt.path)
		}
		if op["requestBody"] != nil {
			withBody++
		}
	}
	comps := doc["components"].(map[string]any)["schemas"].(map[string]any)
	t.Logf("%d operations, %d with a request body, %d schemas", ops, withBody, len(comps))
	if out := os.Getenv("OPENAPI_OUT"); out != "" {
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- answers checked against the description ----

// conform serves srv and checks every JSON request and answer of an
// operation that declares its bodies against the description, so that a
// declared type and what the handler really reads or writes cannot drift
// apart.
func conform(t *testing.T, srv *Server) http.Handler {
	var once sync.Once
	var doc map[string]any
	var mu sync.Mutex
	seen := map[string]bool{}
	t.Cleanup(func() { t.Logf("openapi: %d described operations checked against their answers", len(seen)) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := srv.routeFor(r.Method, r.URL.Path)
		if !ok || rt.doc.generic || len(rt.doc.responses) == 0 {
			srv.ServeHTTP(w, r)
			return
		}
		once.Do(func() {
			var err error
			if doc, err = srv.buildOpenAPI(context.Background()); err != nil {
				t.Errorf("openapi: %v", err)
			}
		})
		var in []byte
		if r.Body != nil {
			in, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(in))
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
		if doc == nil {
			return
		}
		op := doc["paths"].(map[string]any)[rt.path].(map[string]any)[strings.ToLower(rt.method)].(map[string]any)
		name := rt.method + " " + rt.path
		mu.Lock()
		seen[name] = true
		mu.Unlock()
		if rec.Code >= 300 {
			if rec.Code != 304 {
				checkBody(t, doc, name+" refusal", op["responses"].(map[string]any)["4XX"].(map[string]any), rec.Header(), rec.Body.Bytes())
			}
			return
		}
		if rb, ok := op["requestBody"].(map[string]any); ok && len(in) > 0 {
			if content := rb["content"].(map[string]any); content["application/json"] != nil {
				checkBody(t, doc, name+" request", rb, http.Header{"Content-Type": {"application/json"}}, in)
			}
		}
		answer, ok := op["responses"].(map[string]any)[fmt.Sprint(rec.Code)].(map[string]any)
		if !ok {
			t.Errorf("%s: answered %d, which is not described", name, rec.Code)
			return
		}
		checkBody(t, doc, name, answer, rec.Header(), rec.Body.Bytes())
	})
}

// routeFor finds the registered route a request goes to.
func (s *Server) routeFor(method, path string) (routeInfo, bool) {
	segs := strings.Split(path, "/")
	var best routeInfo
	found, literal := false, -1
	for _, rt := range s.routes {
		if rt.method != method {
			continue
		}
		want := strings.Split(rt.path, "/")
		if len(want) != len(segs) {
			continue
		}
		lit, match := 0, true
		for i := range want {
			if strings.HasPrefix(want[i], "{") {
				continue
			}
			if want[i] != segs[i] {
				match = false
				break
			}
			lit++
		}
		if match && lit > literal { // as in net/http: the most literal pattern wins
			best, found, literal = rt, true, lit
		}
	}
	return best, found
}

func checkBody(t *testing.T, doc map[string]any, name string, desc map[string]any, header http.Header, body []byte) {
	t.Helper()
	content, _ := desc["content"].(map[string]any)
	if content == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			t.Errorf("%s: a body where none is described: %.200s", name, body)
		}
		return
	}
	media, _, _ := strings.Cut(header.Get("Content-Type"), ";")
	m, ok := content[media].(map[string]any)
	if !ok {
		t.Errorf("%s: answered %q, described as %v", name, media, keys(content))
		return
	}
	if media != "application/json" {
		return
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Errorf("%s: not JSON: %v", name, err)
		return
	}
	for _, problem := range validate(doc, m["schema"].(map[string]any), v, "") {
		t.Errorf("%s: %s", name, problem)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validate checks a decoded JSON value against a schema of the shapes the
// description uses, and says what does not fit.
func validate(doc map[string]any, s map[string]any, v any, at string) []string {
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, componentRef)
		return validate(doc, doc["components"].(map[string]any)["schemas"].(map[string]any)[name].(map[string]any), v, at)
	}
	if v == nil {
		if s["nullable"] == true || len(s) == 0 {
			return nil
		}
		return []string{at + ": null where it is not nullable"}
	}
	if all, ok := s["allOf"].([]any); ok {
		var out []string
		for _, x := range all {
			out = append(out, validate(doc, x.(map[string]any), v, at)...)
		}
		return out
	}
	if alts, ok := s["oneOf"].([]any); ok {
		for _, x := range alts {
			if len(validate(doc, x.(map[string]any), v, at)) == 0 {
				return nil
			}
		}
		return []string{at + ": fits none of the alternatives"}
	}
	typ, _ := s["type"].(string)
	wrong := func() []string { return []string{fmt.Sprintf("%s: %T where %s is described", at, v, typ)} }
	switch typ {
	case "":
		return nil
	case "string":
		if _, ok := v.(string); !ok {
			return wrong()
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return wrong()
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return wrong()
		}
	case "integer":
		if f, ok := v.(float64); !ok || f != float64(int64(f)) {
			return wrong()
		}
	case "array":
		list, ok := v.([]any)
		if !ok {
			return wrong()
		}
		var out []string
		for i, x := range list {
			out = append(out, validate(doc, s["items"].(map[string]any), x, fmt.Sprintf("%s[%d]", at, i))...)
		}
		return out
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return wrong()
		}
		var out []string
		props, _ := s["properties"].(map[string]any)
		extra, _ := s["additionalProperties"].(map[string]any)
		for k, x := range obj {
			if p, ok := props[k].(map[string]any); ok {
				out = append(out, validate(doc, p, x, at+"."+k)...)
			} else if extra != nil {
				out = append(out, validate(doc, extra, x, at+"."+k)...)
			} else {
				out = append(out, at+"."+k+": not described")
			}
		}
		req, _ := s["required"].([]any)
		for _, k := range req {
			if _, ok := obj[k.(string)]; !ok {
				out = append(out, at+"."+k.(string)+": described as always there, but missing")
			}
		}
		return out
	}
	return nil
}
