package compile_test

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func finishCrossAssembly(t *testing.T, b *nyanjit.Builder) *ir.Program {
	t.Helper()
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func TestEmitAssemblyExplicitArchitecture(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.Add(b.ParamBits(64), b.ParamBits(64)))
	program := finishCrossAssembly(t, b)
	for _, test := range []struct {
		architecture, parameter, result string
	}{
		{"amd64", "RDI", "RAX"},
		{"arm64", "X1", "X0"},
		{"386", "[ESP", "EAX"},
		{"riscv64", "x11", "x10"},
		{"ppc64", "r4", "r3"},
		{"ppc64le", "r4", "r3"},
	} {
		t.Run(test.architecture, func(t *testing.T) {
			assembly, err := compile.EmitAssembly(program, abi.CompileOptions{Architecture: test.architecture, Stack: abi.StackPlatform, StackBytesAvailable: 4096})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(assembly, test.parameter) || !strings.Contains(assembly, test.result) {
				t.Fatalf("listing does not use the requested entry ABI:\n%s", assembly)
			}
		})
	}
	if runtime.GOOS == "linux" {
		host, err := compile.EmitAssembly(program, abi.CompileOptions{})
		if err == nil {
			explicit, err := compile.EmitAssembly(program, abi.CompileOptions{Architecture: runtime.GOARCH})
			if err != nil || explicit != host {
				t.Fatalf("explicit host differs from default: %v\n%s\n%s", err, explicit, host)
			}
		}
	}
}

func TestCrossAssemblyControlAndSpills(t *testing.T) {
	for _, architecture := range []string{"amd64", "arm64", "386", "riscv64", "ppc64", "ppc64le"} {
		t.Run(architecture, func(t *testing.T) {
			b := nyanjit.NewBuilder()
			n := b.ConstBits(32, 5)
			loop, done := b.NewLabel(), b.NewLabel()
			b.JumpArgs(loop, n)
			state := b.BindParamsBits(loop, []uint8{32})[0]
			b.BranchArgs(b.IsZero(state), done, []nyanjit.Value{state}, loop,
				[]nyanjit.Value{b.Sub(state, b.ConstBits(32, 1))})
			b.Return(b.BindParamsBits(done, []uint8{32})[0])
			options := abi.CompileOptions{Architecture: architecture, Stack: abi.StackPlatform, StackBytesAvailable: 4096}
			if _, err := compile.EmitAssembly(finishCrossAssembly(t, b), options); err != nil {
				t.Fatalf("loop: %v", err)
			}

			b = nyanjit.NewBuilder()
			x := b.ParamBits(32)
			b.Enter()
			values := make([]nyanjit.Value, 40)
			for i := range values {
				values[i] = b.Add(x, b.ConstBits(32, uint64(i+1)))
			}
			b.Leave(values...)
			sum := values[0]
			for _, value := range values[1:] {
				sum = b.Add(sum, value)
			}
			b.Return(sum)
			program := finishCrossAssembly(t, b)
			if _, err := compile.EmitAssembly(program, options); err != nil {
				t.Fatalf("spills: %v", err)
			}
			options.StackBytesAvailable = 0
			if _, err := compile.EmitAssembly(program, options); !errors.Is(err, compile.ErrStackRequired) {
				t.Fatalf("missing spill budget: %v", err)
			}
		})
	}
}

func TestCrossAssemblyCallsUseTargetABI(t *testing.T) {
	if nyanjit.NativeBits != 64 {
		t.Skip("call addresses in this Builder use the host native width")
	}
	for _, architecture := range []string{"amd64", "arm64", "riscv64", "ppc64", "ppc64le"} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(64)
			callee := b.Symbol("callee")
			if flavor == abi.C {
				b.Return(b.CallC(callee, x))
			} else {
				b.Return(b.CallGo(callee, x))
			}
			program := finishCrossAssembly(t, b)
			options := abi.CompileOptions{Architecture: architecture, ABI: flavor, Stack: abi.StackPlatform, StackBytesAvailable: 640}
			if _, err := compile.EmitAssembly(program, options); err != nil {
				t.Fatalf("%s ABI %d: %v", architecture, flavor, err)
			}
			usage, err := compile.PreflightRegisterScopes(program, options)
			if err != nil {
				t.Fatal(err)
			}
			effect, err := abi.PreflightCall(abi.CallSpec{Architecture: architecture, ABI: flavor, ArgumentCount: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(usage[0].FixedUses) != 2 || usage[0].FixedUses[1].Register != effect.ArgumentRegisters[0] {
				t.Fatalf("%s ABI %d uses host argument locations: %+v", architecture, flavor, usage)
			}
			if flavor == abi.C {
				options.ManagedCalls = true
				assembly, err := compile.EmitAssembly(program, options)
				if err != nil || !strings.Contains(assembly, "CallBridge") {
					t.Fatalf("%s managed gateway: %v\n%s", architecture, err, assembly)
				}
			}
		}
	}
}

func TestCrossAssemblyAMD64FMAFeatures(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		x, y, z := b.ParamBits(64), b.ParamBits(64), b.ParamBits(64)
		if explicit {
			b.Return(b.FMA(x, y, z))
		} else {
			b.Return(x.FMul(y).FAdd(z))
		}
		program := finishCrossAssembly(t, b)
		options := abi.CompileOptions{Architecture: "amd64"}
		assembly, err := compile.EmitAssembly(program, options)
		if explicit {
			if !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("explicit FMA without target feature: %v", err)
			}
		} else if err != nil || strings.Contains(assembly, "vfmadd") || !strings.Contains(assembly, "mulsd") || !strings.Contains(assembly, "addsd") {
			t.Fatalf("baseline must retain separate rounding: %v\n%s", err, assembly)
		}
		options.CPU = abi.CPUProfile{Extensions: []string{"fma"}}
		assembly, err = compile.EmitAssembly(program, options)
		if err != nil || !strings.Contains(assembly, "vfmadd213sd") {
			t.Fatalf("FMA3 target: %v\n%s", err, assembly)
		}
	}
}

func TestCrossAssemblyNativeEmissionRejectsForeignTarget(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.Const(42))
	program := finishCrossAssembly(t, b)
	architecture := "amd64"
	if runtime.GOARCH == architecture {
		architecture = "arm64"
	}
	options := abi.CompileOptions{Architecture: architecture}
	if _, err := compile.EmitAssembly(program, options); err != nil {
		t.Fatal(err)
	}
	if _, _, err := compile.Emit(program, options); !errors.Is(err, compile.ErrUnsupported) {
		t.Fatalf("foreign byte emission: %v", err)
	}
	if code, err := compile.Compile(program, options); !errors.Is(err, compile.ErrUnsupported) {
		if code != nil {
			code.Close()
		}
		t.Fatalf("foreign callable compilation: %v", err)
	}
	options.Architecture = "invalid"
	if _, err := compile.EmitAssembly(program, options); !errors.Is(err, compile.ErrUnsupported) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestCrossAssemblyEmitterAndBindings(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.Symbol("answer"))
	program := finishCrossAssembly(t, b)
	options := abi.CompileOptions{Architecture: "amd64"}
	assembly, err := compile.EmitAssemblyWithBindings(program, options, map[string]uint64{"answer": 42})
	if err != nil || !strings.Contains(assembly, "mov EAX, 0x2a") || strings.Contains(assembly, "<answer>") {
		t.Fatalf("bound target listing: %v\n%s", err, assembly)
	}
	emitter := compile.NewEmitter()
	if err := emitter.AppendProgram(program); err != nil {
		t.Fatal(err)
	}
	if err := emitter.Exit(); err != nil {
		t.Fatal(err)
	}
	assembly, err = emitter.EmitAssembly(options)
	if err != nil || !strings.Contains(assembly, "movabs RAX, <answer>") {
		t.Fatalf("emitter target listing: %v\n%s", err, assembly)
	}
}

func TestCrossAssemblyEntryArityUsesTargetLimit(t *testing.T) {
	// Construct the IR directly because Builder entry limits still follow its
	// host. Seven parameters are valid for arm64 and exceed amd64's limit.
	program := &ir.Program{Arity: 7, Result: 0}
	for i := 0; i < program.Arity; i++ {
		program.Instructions = append(program.Instructions, ir.Instruction{Op: ir.OpParam, Bits: 64, Imm: uint64(i)})
	}
	if _, err := compile.EmitAssembly(program, abi.CompileOptions{Architecture: "arm64"}); err != nil {
		t.Fatal(err)
	}
	if _, err := compile.EmitAssembly(program, abi.CompileOptions{Architecture: "amd64"}); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("amd64 entry arity: %v", err)
	}
}
