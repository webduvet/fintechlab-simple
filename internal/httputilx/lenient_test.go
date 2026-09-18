package httputilx

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type inner struct {
	City    string `json:"city"`
	Country string `json:"country"`
}

type target struct {
	Name string `json:"name"`
	inner
	Skipped  string `json:"-"`
	Untagged string
	private  string
	Extra    map[string]any `json:"-"`
}

func read(t *testing.T, body string, v any) map[string]any {
	t.Helper()
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	extra, err := ReadJSONExtras(r, v)
	if err != nil {
		t.Fatalf("ReadJSONExtras(%s): %v", body, err)
	}
	return extra
}

// The point of the whole function: a field this service does not model is
// kept and handed back, not refused. Refusing it would teach a client to
// stop sending something the real vendor accepts.
func TestReadJSONExtrasKeepsUnmodelledFields(t *testing.T) {
	var v target
	extra := read(t, `{"name":"Acme","city":"London","vibans":[{"currency":"EUR"}],"nace_codes":["6201"]}`, &v)

	if v.Name != "Acme" || v.City != "London" {
		t.Fatalf("modelled fields not decoded: %+v", v)
	}
	if _, ok := extra["vibans"]; !ok {
		t.Errorf("vibans was dropped: %v", extra)
	}
	if got, ok := extra["nace_codes"]; !ok || !reflect.DeepEqual(got, []any{"6201"}) {
		t.Errorf("nace_codes = %v, want the array as sent", got)
	}
	if len(extra) != 2 {
		t.Errorf("extra = %v, want exactly the two unmodelled fields", extra)
	}
}

// Embedded structs are flattened by encoding/json, so their fields are not
// extra. Getting this wrong would report every inherited field as unknown.
func TestReadJSONExtrasFlattensEmbeddedAndHonoursTags(t *testing.T) {
	var v target
	extra := read(t, `{"name":"Acme","city":"London","country":"GB","Untagged":"x","-":"y"}`, &v)

	if v.Country != "GB" || v.Untagged != "x" {
		t.Fatalf("decode: %+v", v)
	}
	// A json:"-" field is not from the wire, so a body field of that name
	// really is extra rather than being silently claimed by it.
	if _, ok := extra["-"]; !ok {
		t.Errorf(`a body field named "-" should be reported as extra, got %v`, extra)
	}
	if _, ok := extra["city"]; ok {
		t.Errorf("an embedded struct's field was reported as extra: %v", extra)
	}
	if _, ok := extra["Untagged"]; ok {
		t.Errorf("an untagged field is matched by its Go name: %v", extra)
	}
}

func TestReadJSONExtrasNilWhenNothingExtra(t *testing.T) {
	var v target
	if extra := read(t, `{"name":"Acme"}`, &v); extra != nil {
		t.Fatalf("extra = %v, want nil", extra)
	}
	if extra := read(t, ``, &v); extra != nil {
		t.Fatalf("empty body: extra = %v, want nil", extra)
	}
}

func TestReadJSONExtrasStillRejectsBrokenJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":`))
	var v target
	if _, err := ReadJSONExtras(r, &v); err == nil {
		t.Fatal("truncated JSON should still be an error: lenient about unknown fields is not lenient about invalid bodies")
	}
}
