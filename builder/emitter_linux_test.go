//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

func constantFragment(t *testing.T, value uint64) *ir.Program {
	t.Helper()
	b := NewBuilder()
	constant := controlConst(t, b, value)
	requireControlOK(t, b.Return(constant))
	program, err := b.Finish()
	requireControlOK(t, err)
	return program
}

func TestEmitterLabelsBranchesAndTextAssembler(t *testing.T) {
	for _, input := range []uint64{0, 1} {
		e := compile.NewEmitter()
		entry, check, yes, no := e.NewLabel(), e.NewLabel(), e.NewLabel(), e.NewLabel()
		requireControlOK(t, e.Bind(entry))
		requireControlOK(t, e.Jump(check))
		requireControlOK(t, e.Bind(check))
		requireControlOK(t, e.AppendProgram(constantFragment(t, input)))
		requireControlOK(t, e.Branch(0, BranchNotEqual, yes, no))
		requireControlOK(t, e.Bind(yes))
		requireControlOK(t, e.AppendProgram(constantFragment(t, 42)))
		requireControlOK(t, e.Exit())
		requireControlOK(t, e.Bind(no))
		requireControlOK(t, e.AppendProgram(constantFragment(t, 99)))
		requireControlOK(t, e.Exit())
		options := controlOptions()
		assembly, err := e.EmitAssembly(options)
		requireControlOK(t, err)
		if !strings.Contains(assembly, "ret") ||
			!strings.Contains(assembly, "b.ne ") && !strings.Contains(assembly, "b.eq ") &&
				!strings.Contains(assembly, "jnz ") && !strings.Contains(assembly, "je ") &&
				!strings.Contains(assembly, "bne ") && !strings.Contains(assembly, "beq ") {
			t.Fatalf("incomplete emitter assembly:\n%s", assembly)
		}
		bytes, report, err := e.Emit(options)
		requireControlOK(t, err)
		if len(bytes) == 0 || len(bytes) != report.CodeBytes {
			t.Fatalf("emission size = %d, report = %+v", len(bytes), report)
		}
		code, err := execution.NewCode(bytes, execution.CodeOptions{ABI: ABIC}, nil)
		requireControlOK(t, err)
		defer code.Close()
		got, err := code.Call()
		requireControlOK(t, err)
		want := uint64(42)
		if input == 0 {
			want = 99
		}
		if uint64(got) != want {
			t.Fatalf("emitted function for condition %d returned %d, want %d", input, got, want)
		}
	}
}

func TestEmitterRejectsInvalidSequence(t *testing.T) {
	var nilEmitter *compile.Emitter
	if label := nilEmitter.NewLabel(); label != -1 {
		t.Fatalf("nil emitter label = %d, want -1", label)
	}
	e := compile.NewEmitter()
	entry, target := e.NewLabel(), e.NewLabel()
	if err := e.Jump(-1); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Jump(invalid) = %v, want %v", err, ErrInvalidFunction)
	}
	if err := e.Exit(); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Exit without value = %v, want %v", err, ErrInvalidFunction)
	}
	if err := e.Branch(0, BranchEqual, entry, target); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Branch without value = %v, want %v", err, ErrInvalidFunction)
	}
	requireControlOK(t, e.Bind(entry))
	if err := e.Bind(entry); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Bind twice = %v, want %v", err, ErrInvalidFunction)
	}
	withParam := NewBuilder()
	parameter, err := withParam.Param()
	requireControlOK(t, err)
	requireControlOK(t, withParam.Return(parameter))
	if err := e.AppendProgram(func() *ir.Program { program, err := withParam.Finish(); requireControlOK(t, err); return program }()); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Append fragment with parameter = %v, want %v", err, ErrInvalidFunction)
	}
	requireControlOK(t, e.AppendProgram(constantFragment(t, 1)))
	if err := e.Branch(4096, BranchEqual, entry, target); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Branch with unsupported immediate = %v, want %v", err, ErrInvalidFunction)
	}
	if err := e.Branch(0, BranchCondition(255), entry, target); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Branch with invalid condition = %v, want %v", err, ErrInvalidFunction)
	}
	requireControlOK(t, e.Exit())
	unbound := compile.NewEmitter()
	target = unbound.NewLabel()
	requireControlOK(t, unbound.Jump(target))
	if _, _, err := unbound.Emit(controlOptions()); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Emit with unbound label = %v, want %v", err, ErrInvalidFunction)
	}
}
