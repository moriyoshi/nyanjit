//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Walk every report slice recursively, including newly exported and nested
// fields. Retrieval and Clone must preserve the same nil/empty/populated shape.
func reportSliceShapes(v reflect.Value, path string, out map[string]string) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			reportSliceShapes(v.Field(i), path+"."+v.Type().Field(i).Name, out)
		}
	case reflect.Slice:
		shape := "populated"
		if v.IsNil() {
			shape = "nil"
		} else if v.Len() == 0 {
			shape = "empty"
		}
		out[path] = shape
		for i := 0; i < v.Len(); i++ {
			reportSliceShapes(v.Index(i), path+"[]", out)
		}
	}
}

func TestReportSliceShapesAreConsistent(t *testing.T) {
	fixtures := map[string]*ir.Program{
		"constant":  {Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 7}}, Result: 0},
		"arguments": {Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 32}, {Op: ir.OpParam, Bits: 32}, {Op: ir.OpAdd, Bits: 32, A: 0, B: 1}}, Arity: 2, Result: 2},
	}
	for name, p := range fixtures {
		spec := compile.TargetSpec{Architecture: runtime.GOARCH}
		o := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 256}, AssumePointerFree: true}
		reference, err := lowerReport(p, spec, o)
		if errors.Is(err, compile.ErrCPUUnsupported) {
			t.Skip("baseline CPU unavailable")
		}
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{}
		reportSliceShapes(reflect.ValueOf(reference.Snapshot()), "report", want)
		calls := map[string]func() (abi.Report, error){}
		for contextName, context := range map[string]compile.Context{
			"Native": compile.ContextNative, "Foreign": compile.ContextForeign,
			"GoManaged": compile.ContextGoManaged, "External": compile.ContextExternal,
		} {
			f := o
			f.Context = context
			calls["Unit.Report/"+contextName] = func() (abi.Report, error) { return lowerReport(p, spec, f) }
			calls["Template.Report/"+contextName] = func() (abi.Report, error) {
				r, e := lowerTemplate(p, spec, f)
				if e != nil {
					return nil, e
				}
				return r.Report(), nil
			}
		}
		calls["Constants"] = func() (abi.Report, error) {
			f := o
			f.Constants = map[string]uint64{}
			return lowerReport(p, spec, f)
		}
		calls["Unit.Template"] = func() (abi.Report, error) {
			u, e := lowerUnit(p, spec, o)
			if e != nil {
				return nil, e
			}
			r, e := u.Template()
			if e != nil {
				return nil, e
			}
			return r.Report(), nil
		}
		for entry, call := range calls {
			t.Run(name+"/"+entry, func(t *testing.T) {
				report, err := call()
				if err != nil {
					t.Fatal(err)
				}
				for _, snapshot := range []abi.EmissionReport{report.Snapshot(), report.Snapshot().Clone()} {
					shape := map[string]string{}
					reportSliceShapes(reflect.ValueOf(snapshot), "report", shape)
					if !reflect.DeepEqual(shape, want) {
						t.Fatalf("inconsistent report slices: want %v, got %v", want, shape)
					}
				}
			})
		}
	}
}
