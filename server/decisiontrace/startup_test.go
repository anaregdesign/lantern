package decisiontrace

import (
	"reflect"
	"testing"
)

func TestStartupSnapshotHasNoMutableReferences(t *testing.T) {
	var check func(reflect.Type)
	check = func(value reflect.Type) {
		switch value.Kind() {
		case reflect.Array:
			check(value.Elem())
		case reflect.Struct:
			for i := range value.NumField() {
				check(value.Field(i).Type)
			}
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
			t.Fatalf("startup snapshot shares mutable state through %v", value)
		}
	}
	check(reflect.TypeFor[StartupSnapshot]())
}
