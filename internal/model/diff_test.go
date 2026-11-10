package model

import (
	"reflect"
	"testing"
)

// TestEntityEqualCoversEveryField flips each Entity field in turn: a field entityEqual ignores
// would make a real change produce no delta.
func TestEntityEqualCoversEveryField(t *testing.T) {
	base := Entity{Labels: map[string]string{"a": "1"}}
	typ := reflect.TypeFor[Entity]()
	for i := range typ.NumField() {
		changed := base
		changed.Labels = map[string]string{"a": "1"}
		f := reflect.ValueOf(&changed).Elem().Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Int:
			f.SetInt(1)
		case reflect.Map:
			f.Set(reflect.ValueOf(map[string]string{"a": "2"}))
		default:
			t.Fatalf("field %s has kind %s; extend this test and entityEqual", typ.Field(i).Name, f.Kind())
		}
		if entityEqual(base, changed) {
			t.Errorf("entityEqual ignores field %s", typ.Field(i).Name)
		}
	}
	if !entityEqual(base, Entity{Labels: map[string]string{"a": "1"}}) {
		t.Error("equal entities compare unequal")
	}
}
