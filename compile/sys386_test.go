package compile_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// sys386Builds are system call programs for the 386 tests: n arguments, and
// values live across the call.
var sys386Builds = []struct {
	name string
	args int
	// build returns the program; live values outlast its system call.
	build func(*nyanjit.Builder)
}{
	{"getpid", 0, func(b *nyanjit.Builder) { b.Return(b.Syscall(b.Const(20))) }},
	{"write-live", 3, func(b *nyanjit.Builder) {
		x := b.Param()
		b.Return(b.Add(b.Syscall(b.Const(4), b.Const(1), x, b.Const(3)), x))
	}},
	{"five", 5, func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		b.Return(b.Xor(b.Syscall(b.Const(313), x, y, x, y, b.Add(x, y)), y))
	}},
	{"six", 6, func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		r := b.Syscall(b.Const(192), x, y, b.Const(3), b.Add(x, y), b.Const(5), b.Xor(x, y))
		b.Return(b.Add(b.Add(r, x), y))
	}},
	{"six-narrow-live", 6, func(b *nyanjit.Builder) {
		var live []nyanjit.Value
		for range 4 {
			live = append(live, b.CastUnsigned(b.Param(), 32))
		}
		zero := b.Const(0)
		r := b.Syscall(b.Const(313), zero, zero, zero, zero, zero, b.Const(1))
		for _, v := range live {
			r = b.Add(r, b.CastUnsigned(v, 64))
		}
		b.Return(r)
	}},
	{"loop", 1, func(b *nyanjit.Builder) {
		n := b.Param()
		i := b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		b.Set(i, b.Add(b.Get(i), b.Syscall(b.Const(6), b.Get(i))))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(i))
	}},
}

var (
	sys386External = compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}}
	ebpSave        = regexp.MustCompile(`mov dword ptr \[ESP \+ (\d+)\], EBP`)
	ebpRestore     = regexp.MustCompile(`^mov EBP, dword ptr \[ESP \+ (\d+)\]$`)
)

// TestI386SyscallListing lowers 386 system calls at both tiers: INT 0x80,
// a report with a system call and only the scratch registers clobbered,
// and EBP saved before a sixth argument and restored right after INT 0x80.
func TestI386SyscallListing(t *testing.T) {
	for _, s := range sys386Builds {
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			for _, f := range []compile.Func{sys386External, {ABI: abi.Go, Context: compile.ContextExternal, Stack: sys386External.Stack}, {ABI: abi.C, Context: compile.ContextExternal, Stack: sys386External.Stack, Harden: true}} {
				u := lower386(t, tier, s.build, f)
				listing, err := u.Listing()
				if err != nil {
					t.Fatal(err)
				}
				r := u.Report()
				if !r.HasSystemCalls() || r.HasFunctionCalls() || !r.MayReadMemory() || !r.MayWriteMemory() || !r.FlagsClobbered() {
					t.Errorf("%s tier %d %+v: report %+v", s.name, tier, f, r.Snapshot())
				}
				clobbers := strings.Join(r.ClobberedRegisters(), ",")
				if !strings.Contains(clobbers, "EAX") || strings.ContainsAny(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(clobbers, "EAX", ""), "ECX", ""), "EDX", ""), "ABCDSP") {
					t.Errorf("%s tier %d: clobbers %v", s.name, tier, r.ClobberedRegisters())
				}
				lines := strings.Split(listing, "\n")
				calls := 0
				for k, line := range lines {
					if strings.TrimSpace(line) != "int 0x80" {
						continue
					}
					calls++
					next := strings.TrimSpace(lines[k+1])
					m := ebpRestore.FindStringSubmatch(next)
					if s.args < 6 {
						if m != nil || ebpSave.MatchString(strings.Join(lines[max(k-7, 0):k], "\n")) {
							t.Errorf("%s tier %d: EBP touched without a sixth argument:\n%s", s.name, tier, listing)
						}
						continue
					}
					save := ebpSave.FindAllStringSubmatch(strings.Join(lines[:k], "\n"), -1)
					if m == nil || len(save) == 0 || save[len(save)-1][1] != m[1] {
						t.Errorf("%s tier %d: EBP not restored after INT 0x80:\n%s", s.name, tier, listing)
					}
				}
				if calls != 1 {
					t.Errorf("%s tier %d: %d INT 0x80 in\n%s", s.name, tier, calls, listing)
				}
			}
		}
	}
}

// TestI386SyscallRules checks that a 386 system call follows amd64's
// context and stack rules at both tiers, through Lower and a bound Builder.
func TestI386SyscallRules(t *testing.T) {
	for _, s := range sys386Builds {
		for _, c := range []struct {
			f    compile.Func
			want error
		}{
			{sys386External, nil},
			{compile.Func{ABI: abi.Go, Context: compile.ContextExternal, Stack: sys386External.Stack}, nil},
			{compile.Func{ABI: abi.C, Context: compile.ContextExternal}, compile.ErrStackRequired},
			{compile.Func{ABI: abi.C, Stack: sys386External.Stack}, compile.ErrExternalCallOnly},
			{compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: sys386External.Stack}, compile.ErrExternalCallOnly},
		} {
			b := nyanjit.NewBuilder()
			s.build(b)
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			for _, arch := range []string{"386", "amd64"} {
				for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
					target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := compile.Lower(target, p, c.f); !errors.Is(err, c.want) || (err == nil) != (c.want == nil) {
						t.Errorf("%s %s tier %d %+v: Lower: %v, want %v", s.name, arch, tier, c.f, err, c.want)
					}
					bound := nyanjit.NewBuilderFor(target, c.f)
					s.build(bound)
					if _, err := bound.Lower(); !errors.Is(err, c.want) || (err == nil) != (c.want == nil) {
						t.Errorf("%s %s tier %d %+v: bound Builder: %v, want %v", s.name, arch, tier, c.f, err, c.want)
					}
				}
			}
		}
	}
}

// TestI386SyscallEffect checks the 386 system call effect: INT 0x80's
// registers, EAX clobbered and no call stack.
func TestI386SyscallEffect(t *testing.T) {
	for n := 0; n <= 6; n++ {
		effect, err := compile.PreflightCall(abi.CallSpec{Architecture: "386", Kind: abi.SystemCall, ABI: abi.C, ArgumentCount: n})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"EBX", "ECX", "EDX", "ESI", "EDI", "EBP"}[:n]
		if effect.NumberRegister != "EAX" || strings.Join(effect.ArgumentRegisters, ",") != strings.Join(want, ",") ||
			effect.ResultRegister != "EAX" || strings.Join(effect.ClobberedRegisters, ",") != "EAX" || len(effect.ClobberedFPRegisters) != 0 ||
			!effect.FlagsClobbered || effect.RequiresCallStack || effect.StackAlignment != 0 || effect.Precision != abi.EffectPlatformGuaranteed ||
			len(effect.ArgumentLocations) != n || len(effect.ResultLocation.Words) != 1 || effect.ResultLocation.Words[0].Register != "EAX" {
			t.Errorf("%d arguments: %+v", n, effect)
		}
	}
	if _, err := compile.PreflightCall(abi.CallSpec{Architecture: "386", Kind: abi.SystemCall, ABI: abi.C, ArgumentCount: 7}); !errors.Is(err, compile.ErrBackendLimit) {
		t.Errorf("seven arguments: %v", err)
	}
}
