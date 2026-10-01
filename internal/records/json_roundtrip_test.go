package records

import (
	"context"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/catalog"
)

// A json column must round-trip values symmetrically: a scalar string stays a
// string (not reinterpreted as a bool/number), and objects/arrays decode back.
func TestJSONColumnRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	if _, err := s.cat.CreateTable(ctx, "docs", []catalog.Column{{Name: "data", Type: "json"}}); err != nil {
		t.Fatal(err)
	}

	cases := map[string]any{
		"str_true":  "true",                       // must stay the string "true"
		"str_plain": "hello",                       // plain string
		"object":    map[string]any{"a": float64(1)}, // object
		"number":    float64(42),                   // number
		"boolean":   true,                          // actual boolean
	}
	ids := map[string]string{}
	for name, val := range cases {
		row, err := s.rec.Insert(ctx, admin(), "docs", map[string]any{"data": val})
		if err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		ids[name] = row["id"].(string)
	}

	get := func(name string) any {
		row, err := s.rec.Get(ctx, admin(), "docs", ids[name])
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return row["data"]
	}

	if got := get("str_true"); got != "true" {
		t.Errorf("json string \"true\" must stay a string, got %#v", got)
	}
	if got := get("str_plain"); got != "hello" {
		t.Errorf("json string round-trip failed, got %#v", got)
	}
	if got := get("boolean"); got != true {
		t.Errorf("json boolean round-trip failed, got %#v", got)
	}
	if got := get("number"); got != float64(42) {
		t.Errorf("json number round-trip failed, got %#v", got)
	}
	if m, ok := get("object").(map[string]any); !ok || m["a"] != float64(1) {
		t.Errorf("json object round-trip failed, got %#v", get("object"))
	}
}
