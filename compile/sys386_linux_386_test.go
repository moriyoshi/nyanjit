//go:build linux && 386

package compile_test

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// run386Syscall lowers build as external code at tier, maps it in this
// process as a raw-byte caller, and calls it with the 64-bit args.
func run386Syscall(t *testing.T, tier compile.Tier, harden bool, build func(*nyanjit.Builder), args ...uint64) uint64 {
	t.Helper()
	f := sys386External
	f.Harden = harden
	u := lower386(t, tier, build, f)
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := u.Report()
	c, err := execution.NewCode(code, execution.CodeOptions{ABI: r.ABI(), Arity: len(args), ArgumentWords: 2 * len(args), StackBytes: r.StackBytes() + r.CallStackBytes()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	words := make([]any, len(args))
	for i, x := range args {
		words[i] = x
	}
	lo, hi, err := c.CallPair(execution.Words(words...)...)
	if err != nil {
		t.Fatal(err)
	}
	return execution.Wide(lo, hi)
}

// sys386Run runs check for each tier, unhardened and hardened.
func sys386Run(t *testing.T, check func(t *testing.T, tier compile.Tier, harden bool)) {
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		for _, harden := range []bool{false, true} {
			check(t, tier, harden)
		}
	}
}

func TestI386SyscallGetpid(t *testing.T) {
	sys386Run(t, func(t *testing.T, tier compile.Tier, harden bool) {
		got := run386Syscall(t, tier, harden, func(b *nyanjit.Builder) { b.Return(b.Syscall(b.Const(syscall.SYS_GETPID))) })
		if got != uint64(os.Getpid()) {
			t.Errorf("tier %d harden %v: getpid = %#x, want %d", tier, harden, got, os.Getpid())
		}
	})
}

// TestI386SyscallWritePipe writes to a pipe with every argument a
// parameter whose high word is not zero, and keeps them live across the
// call.
func TestI386SyscallWritePipe(t *testing.T) {
	sys386Run(t, func(t *testing.T, tier compile.Tier, harden bool) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()
		message := []byte("nyanjit 386 int 0x80")
		const junk = 0xdead_0000_0000_0000
		got := run386Syscall(t, tier, harden, func(b *nyanjit.Builder) {
			fd, buffer, n := b.Param(), b.Param(), b.Param()
			written := b.Syscall(b.Const(syscall.SYS_WRITE), fd, buffer, n)
			b.Return(b.Add(b.Mul(written, b.Const(1000)), b.Xor(b.Xor(fd, buffer), n)))
		}, junk|uint64(w.Fd()), junk|uint64(uintptr(unsafe.Pointer(&message[0]))), junk|uint64(len(message)))
		runtime.KeepAlive(message)
		want := uint64(len(message))*1000 + (junk ^ uint64(w.Fd()) ^ uint64(uintptr(unsafe.Pointer(&message[0]))) ^ uint64(len(message)))
		if got != want {
			t.Errorf("tier %d harden %v: write = %#x, want %#x", tier, harden, got, want)
		}
		read := make([]byte, 64)
		n, err := r.Read(read)
		if err != nil || string(read[:n]) != string(message) {
			t.Errorf("tier %d harden %v: pipe read %q, %v", tier, harden, read[:n], err)
		}
	})
}

// TestI386SyscallErrno checks that -EBADF and -EINVAL results are
// sign-extended and detected by the portable unsigned r >= -4095 test.
func TestI386SyscallErrno(t *testing.T) {
	sys386Run(t, func(t *testing.T, tier compile.Tier, harden bool) {
		ebadf := run386Syscall(t, tier, harden, func(b *nyanjit.Builder) {
			b.Return(b.Syscall(b.Const(syscall.SYS_CLOSE), b.Const(1<<32|0xffffffff)))
		})
		if int64(ebadf) != -int64(syscall.EBADF) {
			t.Errorf("tier %d harden %v: close(-1) = %#x, want -EBADF", tier, harden, ebadf)
		}
		// Anonymous mmap2 without MAP_PRIVATE or MAP_SHARED: -EINVAL,
		// through all six argument registers. MAP_ANON makes fd irrelevant;
		// an invalid file descriptor without it may report -EBADF first.
		failed := run386Syscall(t, tier, harden, func(b *nyanjit.Builder) {
			r := b.Syscall(b.Const(syscall.SYS_MMAP2), b.Const(0), b.Const(4096), b.Const(syscall.PROT_READ), b.Const(syscall.MAP_ANON), b.Const(^uint64(0)), b.Const(0))
			flag := b.Var(b.Const(0))
			ok := b.NewLabel()
			b.Branch(b.Compare(r, ir.BranchBelow, b.Const(^uint64(4094))), ok)
			b.Set(flag, b.Const(1<<32))
			b.Jump(ok)
			b.Bind(ok)
			b.Return(b.Add(b.Get(flag), b.Sub(b.Const(0), r)))
		})
		if failed != 1<<32|uint64(syscall.EINVAL) {
			t.Errorf("tier %d harden %v: mmap2 error check = %#x, want %#x", tier, harden, failed, 1<<32|uint64(syscall.EINVAL))
		}
	})
}

// TestI386SyscallMmap2 maps page offset pgoff of a file with all six
// arguments, the offset in EBP, reads the page and unmaps it. Four narrow
// values live across the call, more than the registers the call preserves.
func TestI386SyscallMmap2(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "mmap2")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	page := make([]byte, 2*4096)
	for i := range page {
		page[i] = byte(0x11 * (1 + i/4096))
	}
	if _, err := file.Write(page); err != nil {
		t.Fatal(err)
	}
	sys386Run(t, func(t *testing.T, tier compile.Tier, harden bool) {
		for pgoff := uint64(0); pgoff < 2; pgoff++ {
			got := run386Syscall(t, tier, harden, func(b *nyanjit.Builder) {
				fd, offset := b.Param(), b.Param()
				var live []nyanjit.Value
				for k := range 4 {
					live = append(live, b.CastUnsigned(b.Add(fd, b.Const(uint64(k))), 32))
				}
				address := b.Syscall(b.Const(syscall.SYS_MMAP2), b.Const(0), b.Const(4096), b.Const(syscall.PROT_READ), b.Const(syscall.MAP_PRIVATE), fd, offset)
				result := b.Var(address)
				done := b.NewLabel()
				b.Branch(b.Compare(address, ir.BranchAboveEqual, b.Const(^uint64(4094))), done)
				value := b.Load64(address)
				unmapped := b.Syscall(b.Const(syscall.SYS_MUNMAP), address, b.Const(4096))
				sum := b.Add(value, unmapped)
				for k, v := range live {
					sum = b.Add(sum, b.ShiftLeft(b.Sub(b.CastUnsigned(v, 64), fd), uint8(8*k)))
				}
				b.Set(result, sum)
				b.Jump(done)
				b.Bind(done)
				b.Return(b.Get(result))
			}, uint64(file.Fd()), pgoff)
			// The live values add 0, 1<<8, 2<<16 and 3<<24.
			want := 0x1111111111111111*(pgoff+1) + 0x03020100
			if got != want {
				t.Errorf("tier %d harden %v pgoff %d: = %#x, want %#x", tier, harden, pgoff, got, want)
			}
		}
	})
}
