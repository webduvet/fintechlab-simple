package httputilx

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
)

// ReadJSONExtras decodes a request body into v and returns every top-level
// field the body carried that v has no place for.
//
// ReadJSON is the right default: it refuses unknown fields, so a caller that
// misspells one is told immediately instead of watching it vanish. That
// only works when the schema is known. For a mock of a third-party API
// whose full schema is *not* published to us, refusing an unknown field
// turns "this vendor accepts a field we haven't modelled" into a 400 the
// real vendor would never send -- and a client that then removes the field
// to get past the mock has been actively misled.
//
// So: decode what we model, keep what we don't, and echo it back. Nothing
// is silently dropped, and nothing is wrongly rejected. Returns a nil map
// when the body carried nothing extra.
func ReadJSONExtras(r *http.Request, v any) (map[string]any, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		// A non-object body (an array, a bare string) decoded into v
		// successfully or it would have failed above; there is nothing
		// extra to report either way.
		return nil, nil
	}
	for name := range jsonFieldNames(reflect.TypeOf(v)) {
		delete(raw, name)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return raw, nil
}

// jsonFieldNames collects the JSON names of t's exported fields, following
// pointers and flattening embedded structs the way encoding/json does.
func jsonFieldNames(t reflect.Type) map[string]struct{} {
	names := map[string]struct{}{}
	collectJSONFieldNames(t, names)
	return names
}

func collectJSONFieldNames(t reflect.Type, into map[string]struct{}) {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, name := f.Tag.Get("json"), f.Name
		if tag != "" {
			tag, _, _ = strings.Cut(tag, ",")
			if tag == "-" {
				// Explicitly not from the wire (an Extra map, say) -- so a
				// field of that name in the body really is extra.
				continue
			}
			if tag != "" {
				name = tag
			}
		}
		// An untagged embedded struct is flattened by encoding/json, so
		// its fields are not extra. This is checked before the unexported
		// test on purpose: an embedded field of an unexported *type* still
		// has its exported fields promoted onto the wire.
		if f.Anonymous && tag == "" {
			ft := f.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectJSONFieldNames(ft, into)
				continue
			}
		}
		if f.PkgPath != "" {
			continue // unexported, never decoded from the body
		}
		into[name] = struct{}{}
	}
}
