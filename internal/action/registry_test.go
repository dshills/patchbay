package action

import (
	"reflect"
	"testing"
)

func TestRegistry(t *testing.T) {
	r := NewRegistry[int]()
	if r.Register("", 1) == nil {
		t.Fatal("empty")
	}
	if err := r.Register("z", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("a", 2); err != nil {
		t.Fatal(err)
	}
	if r.Register("a", 3) == nil {
		t.Fatal("duplicate")
	}
	if got, ok := r.Get("a"); !ok || got != 2 {
		t.Fatal(got, ok)
	}
	if !reflect.DeepEqual(r.Names(), []string{"a", "z"}) {
		t.Fatal(r.Names())
	}
}
