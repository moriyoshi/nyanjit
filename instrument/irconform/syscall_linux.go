package irconform

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// syscallCases make Linux system calls whose results do not depend on when
// they run: getpid and getppid, and calls that fail on a closed descriptor
// or on invalid flags. Check runs them as external code
// (compile.ContextExternal) and gives the interpreter hostSyscall. They pass
// only the low 32 bits of the number and the arguments on 386, where the
// result is EAX sign-extended, and on every target -errno is at least
// -4095 as an unsigned value.
func syscallCases() []Case {
	var cases []Case
	add := func(name string, inputs [][]uint64, build func(b *nyanjit.Builder)) {
		cases = append(cases, Case{Name: "syscall/" + name, Inputs: inputs, Build: built(build)})
	}
	// splice(fd_in, off_in, fd_out, off_out, len, flags) returns 0 for a
	// zero len, -EINVAL for unknown flags and otherwise -EBADF for fd -1:
	// its fifth and sixth arguments choose the result.
	closed := ^uint64(0)
	add("getpid", [][]uint64{{}}, func(b *nyanjit.Builder) {
		b.Return(b.Syscall(b.Const(syscall.SYS_GETPID)))
	})
	add("number", [][]uint64{{syscall.SYS_GETPID}, {syscall.SYS_GETPPID}, {syscall.SYS_GETPID | 1<<32}}, func(b *nyanjit.Builder) {
		b.Return(b.Syscall(b.Param()))
	})
	add("live", unary(64), func(b *nyanjit.Builder) {
		x := b.Param()
		y := b.Mul(x, b.Const(3))
		r := b.Syscall(b.Const(syscall.SYS_GETPID))
		b.Return(b.Add(b.Xor(r, y), x))
	})
	add("errno", [][]uint64{{0}, {1}, {1 << 32}, {^uint64(0)}}, func(b *nyanjit.Builder) {
		n := b.Param()
		r := b.Syscall(b.Const(syscall.SYS_WRITE), b.Const(closed), b.Const(0), n)
		failed := boolValue(b, b.Compare(r, ir.BranchAboveEqual, b.Const(^uint64(4094))))
		b.Return(b.Add(b.ShiftLeft(failed, 32), b.Sub(b.Const(0), r)))
	})
	add("six", [][]uint64{{0, 0}, {1, 0}, {1, 1 << 31}, {1 << 32, 0}, {7, 1<<32 | 1<<31}, {^uint64(0), ^uint64(0)}}, func(b *nyanjit.Builder) {
		n, flags := b.Param(), b.Param()
		zero := b.Const(0)
		r := b.Syscall(b.Const(syscall.SYS_SPLICE), b.Const(closed), zero, b.Const(closed), zero, n, flags)
		b.Return(b.Add(b.Xor(r, n), flags))
	})
	add("loop", [][]uint64{{0}, {1}, {5}}, func(b *nyanjit.Builder) {
		n := b.Param()
		i, acc := b.Var(b.Const(0)), b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		r := b.Syscall(b.Const(syscall.SYS_CLOSE), b.Const(closed))
		b.Set(acc, b.Add(b.Get(acc), b.Xor(r, b.Get(i))))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(acc))
	})
	add("branch", [][]uint64{{0, 0}, {1, 3}, {2, 1 << 40}}, func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		result := b.Var(y)
		other, done := b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(x), other)
		b.Set(result, b.Add(b.Syscall(b.Const(syscall.SYS_GETPPID)), b.Get(result)))
		b.Jump(done)
		b.Bind(other)
		b.Set(result, b.Sub(b.Get(result), b.Syscall(b.Const(syscall.SYS_GETPID))))
		b.Jump(done)
		b.Bind(done)
		b.Return(b.Add(b.Get(result), x))
	})
	// Raw IR: a 32-bit argument, and the result returned without a Builder.
	for _, call := range []struct {
		name   string
		number uint64
	}{{"close", syscall.SYS_CLOSE}, {"dup", syscall.SYS_DUP}} {
		cases = append(cases, Case{Name: "syscall/raw-" + call.name, Inputs: [][]uint64{{0xffffffff}, {0xfffffffe}},
			Build: straight(append(params(32),
				ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: call.number},
				ir.Instruction{Op: ir.OpSyscall, Args: []int{1, 0}})...)})
	}
	return cases
}

// hostSyscall makes system call c in this process, as the interpreter's
// oracle for OpSyscall. It returns what generated code reads: -errno on
// failure, and on a 32-bit host the 32-bit result sign-extended.
func hostSyscall(c irinterp.Call) (uint64, error) {
	if c.Op != ir.OpSyscall || len(c.Args) > 6 {
		return 0, irinterp.ErrUnsupported
	}
	var a [6]uintptr
	for i, x := range c.Args {
		a[i] = uintptr(x)
	}
	r, _, errno := syscall.RawSyscall6(uintptr(c.Target), a[0], a[1], a[2], a[3], a[4], a[5])
	if errno != 0 {
		return -uint64(errno), nil
	}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return uint64(int64(int32(r))), nil
	}
	return uint64(r), nil
}
