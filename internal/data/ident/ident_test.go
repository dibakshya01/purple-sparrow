package ident

import "testing"

func TestValidUser(t *testing.T) {
	good := []string{"todos", "user_profiles", "t1", "_draft", "a_b_c"}
	for _, g := range good {
		if !ValidUser(g) {
			t.Errorf("%q should be a valid user identifier", g)
		}
	}
	bad := []string{
		"", "Todos", "1table", "has space", "drop;table", `"quoted"`,
		"_oc_tables", "café", "a-b", "toolong_" + string(make([]byte, 70)),
	}
	for _, b := range bad {
		if ValidUser(b) {
			t.Errorf("%q should be rejected", b)
		}
	}
}

func TestTypeMapping(t *testing.T) {
	for _, lt := range LogicalTypes() {
		if !ValidType(lt) {
			t.Errorf("%q should be a valid type", lt)
		}
		if _, ok := SQLiteType(lt); !ok {
			t.Errorf("%q should map to a sqlite type", lt)
		}
	}
	if ValidType("blob") || ValidType("varchar(20)") {
		t.Error("non-whitelisted types must be rejected")
	}
	if bt, _ := SQLiteType("boolean"); bt != "INTEGER" {
		t.Errorf("boolean should map to INTEGER, got %q", bt)
	}
}
