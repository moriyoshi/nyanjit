package execution

import (
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/goregister"
)

func TestTemplateNilReportIsEmpty(t *testing.T) {
	template := NewTemplate(nil, nil, nil, CodeOptions{}, goregister.Frame{})
	if got := template.Report(); !reflect.DeepEqual(got.Snapshot(), abi.EmissionReport{}) {
		t.Fatalf("zero report: %+v", got.Snapshot())
	}
}

// Exercise every leaf so a new public report field cannot be dropped on the
// way through a template. Empty slices matter.
func TestTemplateRetainsEveryReportField(t *testing.T) {
	var visit func(reflect.Type, []int, string)
	visit = func(typ reflect.Type, path []int, name string) {
		for i := range typ.NumField() {
			field := typ.Field(i)
			ids := append(append([]int(nil), path...), i)
			label := name + field.Name
			if field.Type.Kind() == reflect.Struct {
				visit(field.Type, ids, label+".")
				continue
			}
			t.Run(label, func(t *testing.T) {
				var report abi.EmissionReport
				value := reflect.ValueOf(&report).Elem().FieldByIndex(ids)
				switch value.Kind() {
				case reflect.String:
					value.SetString("present")
				case reflect.Bool:
					value.SetBool(true)
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
					value.SetUint(1)
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					value.SetInt(1)
				case reflect.Slice:
					value.Set(reflect.MakeSlice(value.Type(), 0, 0))
				default:
					t.Fatalf("add nonzero case for %s", value.Kind())
				}
				template := NewTemplate(nil, abi.NewReport(report), nil, CodeOptions{}, goregister.Frame{})
				if !reflect.DeepEqual(template.Report().Snapshot(), report) {
					t.Fatalf("dropped %s", label)
				}
			})
		}
	}
	visit(reflect.TypeFor[abi.EmissionReport](), nil, "")
}
