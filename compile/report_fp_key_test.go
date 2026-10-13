package compile

import (
	"reflect"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestFloatWriteKeyCoversInputs guards the FloatWrites cache in
// fpClobbersFromPhysical: changing any Instruction field outside
// floatWriteKey must not change the registers FloatWrites reports. If
// FloatWrites starts reading another field, add it to floatWriteKey.
func TestFloatWriteKeyCoversInputs(t *testing.T) {
	instructionType := reflect.TypeFor[machine.Instruction]()
	keyType := reflect.TypeFor[floatWriteKey]()
	inKey := map[string]bool{}
	for i := range keyType.NumField() {
		field := keyType.Field(i)
		match, ok := instructionType.FieldByName(field.Name)
		if !ok || match.Type != field.Type {
			t.Fatalf("floatWriteKey.%s %v has no Instruction field of the same type", field.Name, field.Type)
		}
		inKey[field.Name] = true
	}
	registers := []backend.Register{3, backend.VectorRegister(5), backend.VectorRegister(6)}
	architectures := []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"}
	for op := machine.Opcode(0); op.Valid(); op++ {
		for _, arch := range architectures {
			for _, bits := range []uint8{32, 64} {
				for _, arithmetic := range []ir.Opcode{0, ir.OpFConvert, ir.OpFAdd} {
					for n := range len(registers) * len(registers) * len(registers) * len(registers) {
						base := machine.Instruction{Op: op, Bits: bits, Arithmetic: arithmetic,
							A: registers[n%3], B: registers[n/3%3], C: registers[n/9%3], D: registers[n/27%3]}
						want := machine.FloatWrites(base, arch)
						for variant := range 2 {
							changed := base
							value := reflect.ValueOf(&changed).Elem()
							for i := range instructionType.NumField() {
								if name := instructionType.Field(i).Name; !inKey[name] {
									mutateField(t, value.Field(i), name, variant)
								}
							}
							if got := machine.FloatWrites(changed, arch); !slices.Equal(got, want) {
								t.Fatalf("%s %+v: fields outside floatWriteKey changed FloatWrites from %v to %v (variant %d)", arch, base, want, got, variant)
							}
						}
					}
				}
			}
		}
	}
}

// mutateField sets v to a non-zero value; variant 1 picks a different one.
func mutateField(t *testing.T, v reflect.Value, name string, variant int) {
	t.Helper()
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt([]int64{1, -7}[variant])
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint([]uint64{1, 0x7f}[variant])
	case reflect.String:
		v.SetString([]string{"x", "symbol"}[variant])
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			t.Fatalf("Instruction.%s: unsupported slice type %v; extend mutateField", name, v.Type())
		}
		v.SetBytes([][]byte{{1}, {0xff, 0, 0xff, 0, 1, 2, 3, 4}}[variant])
	case reflect.Struct:
		for i := range v.NumField() {
			mutateField(t, v.Field(i), name+"."+v.Type().Field(i).Name, variant)
		}
	default:
		t.Fatalf("Instruction.%s: unsupported kind %v; extend mutateField", name, v.Kind())
	}
}
