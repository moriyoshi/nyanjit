//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"encoding/binary"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func mappedTestMemory(t *testing.T) ([]byte, uintptr) {
	t.Helper()
	memory, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(memory); err != nil {
			t.Errorf("Munmap: %v", err)
		}
	})
	return memory, uintptr(unsafe.Pointer(&memory[0]))
}

func TestNativeMemorySharedScaledAddress(t *testing.T) {
	memory, baseAddress := mappedTestMemory(t)
	b := NewBuilder()
	base, err := b.Param()
	requireControlOK(t, err)
	index, err := b.Param()
	requireControlOK(t, err)
	value, err := b.Param()
	requireControlOK(t, err)
	address := base.Add(index.MulConst(8))
	requireControlOK(t, b.Store64(address, value))
	loaded, err := b.Load64(address)
	requireControlOK(t, err)
	requireControlOK(t, b.Return(loaded))
	report, err := testReport(b, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	requireControlOK(t, err)
	if !report.ExplicitMemoryReads() || !report.ExplicitMemoryWrites() {
		t.Fatalf("memory effects missing from report: %+v", report.Snapshot())
	}
	code, err := testCompile(b)
	requireControlOK(t, err)
	defer code.Close()
	const want = uint64(0x8877665544332211)
	got, err := code.Call(baseAddress, 3, uintptr(want))
	requireControlOK(t, err)
	runtime.KeepAlive(memory)
	if uint64(got) != want || binary.LittleEndian.Uint64(memory[24:32]) != want {
		t.Fatalf("shared address load = %#x, memory = %#x, want %#x", got, binary.LittleEndian.Uint64(memory[24:32]), want)
	}
}

func TestNativeMemoryFolded64Address(t *testing.T) {
	memory, baseAddress := mappedTestMemory(t)
	b := NewBuilder()
	base, err := b.Param()
	requireControlOK(t, err)
	index, err := b.Param()
	requireControlOK(t, err)
	value, err := b.Param()
	requireControlOK(t, err)
	storeAddress := base.Add(index.MulConst(8))
	requireControlOK(t, b.Store64(storeAddress, value))
	loadAddress := base.Add(index.MulConst(8))
	loaded, err := b.Load64(loadAddress)
	requireControlOK(t, err)
	requireControlOK(t, b.Return(loaded))
	code, err := testCompile(b)
	requireControlOK(t, err)
	defer code.Close()
	const want = uint64(0x123456789abcdef0)
	got, err := code.Call(baseAddress, 4, uintptr(want))
	requireControlOK(t, err)
	runtime.KeepAlive(memory)
	if uint64(got) != want || binary.LittleEndian.Uint64(memory[32:40]) != want {
		t.Fatalf("folded load = %#x, memory = %#x, want %#x", got, binary.LittleEndian.Uint64(memory[32:40]), want)
	}
}

func TestNativeMemoryByteAddress(t *testing.T) {
	memory, baseAddress := mappedTestMemory(t)
	b := NewBuilder()
	base, err := b.Param()
	requireControlOK(t, err)
	index, err := b.Param()
	requireControlOK(t, err)
	value, err := b.Param()
	requireControlOK(t, err)
	storeAddress := base.Add(index)
	requireControlOK(t, b.Store8(storeAddress, value))
	loadAddress := base.Add(index)
	loaded, err := b.Load8(loadAddress)
	requireControlOK(t, err)
	requireControlOK(t, b.Return(loaded))
	code, err := testCompile(b)
	requireControlOK(t, err)
	defer code.Close()
	got, err := code.Call(baseAddress, 13, 0x1234ab)
	requireControlOK(t, err)
	runtime.KeepAlive(memory)
	if got != 0xab || memory[13] != 0xab {
		t.Fatalf("byte load = %#x, memory[13] = %#x, want 0xab", got, memory[13])
	}
}

func TestNativeMemoryLittleEndianHelpers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store func(*Builder, Value, Value) error
		load  func(*Builder, Value) (Value, error)
		want  uint64
		bytes []byte
	}{
		{name: "16-bit", store: (*Builder).Store16, load: (*Builder).Load16, want: 0xbabe, bytes: []byte{0xbe, 0xba}},
		{name: "32-bit", store: (*Builder).Store32, load: (*Builder).Load32, want: 0xcafebabe, bytes: []byte{0xbe, 0xba, 0xfe, 0xca}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			memory, address := mappedTestMemory(t)
			b := NewBuilder()
			pointer, err := b.Param()
			requireControlOK(t, err)
			input, err := b.Param()
			requireControlOK(t, err)
			requireControlOK(t, tc.store(b, pointer, input))
			loaded, err := tc.load(b, pointer)
			requireControlOK(t, err)
			requireControlOK(t, b.Return(loaded))
			code, err := testCompile(b)
			requireControlOK(t, err)
			defer code.Close()
			got, err := code.Call(address, 0xfeedbeefcafebabe)
			requireControlOK(t, err)
			runtime.KeepAlive(memory)
			if uint64(got) != tc.want {
				t.Fatalf("loaded %#x, want %#x", got, tc.want)
			}
			for i, want := range tc.bytes {
				if memory[i] != want {
					t.Fatalf("memory[%d] = %#x, want %#x", i, memory[i], want)
				}
			}
		})
	}
}

func TestIndexedAddressBranchesBeforeLoad(t *testing.T) {
	memory, address := mappedTestMemory(t)
	binary.LittleEndian.PutUint64(memory[8:16], 0x123456789abcdef0)
	b := NewBuilder()
	entry, invalid := b.NewLabel(), b.NewLabel()
	requireControlOK(t, b.Bind(entry))
	base, err := b.Param()
	requireControlOK(t, err)
	index, err := b.Param()
	requireControlOK(t, err)
	count, err := b.Param()
	requireControlOK(t, err)
	indexed, err := b.IndexedAddress(base, index, count, 8, invalid)
	requireControlOK(t, err)
	loaded, err := b.Load64(indexed)
	requireControlOK(t, err)
	requireControlOK(t, b.Return(loaded))
	requireControlOK(t, b.Bind(invalid))
	bad := controlConst(t, b, 99)
	requireControlOK(t, b.Return(bad))
	code, err := testCompileWithOptions(b, controlOptions())
	requireControlOK(t, err)
	defer code.Close()
	got, err := code.Call(address, 1, 2)
	requireControlOK(t, err)
	runtime.KeepAlive(memory)
	if got != 0x123456789abcdef0 {
		t.Fatalf("valid indexed load = %#x", got)
	}
	got, err = code.Call(0, 9, 2)
	requireControlOK(t, err)
	if got != 99 {
		t.Fatalf("invalid indexed load = %#x, want 99", got)
	}
}

func TestSignExtendWidths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width uint8
		input uint64
		want  uint64
	}{
		{name: "8-bit positive", width: 8, input: 0x7f, want: 0x7f},
		{name: "8-bit negative", width: 8, input: 0x180, want: 0xffffffffffffff80},
		{name: "16-bit positive", width: 16, input: 0x7fff, want: 0x7fff},
		{name: "16-bit negative", width: 16, input: 0x18000, want: 0xffffffffffff8000},
		{name: "32-bit positive", width: 32, input: 0x7fffffff, want: 0x7fffffff},
		{name: "32-bit negative", width: 32, input: 0x180000000, want: 0xffffffff80000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			input, err := b.Param()
			requireControlOK(t, err)
			value, err := b.SignExtend(input, tc.width)
			requireControlOK(t, err)
			requireControlOK(t, b.Return(value))
			code, err := testCompile(b)
			requireControlOK(t, err)
			defer code.Close()
			got, err := code.Call(uintptr(tc.input))
			requireControlOK(t, err)
			if uint64(got) != tc.want {
				t.Fatalf("SignExtend(%#x, %d) = %#x, want %#x", tc.input, tc.width, got, tc.want)
			}
		})
	}
}
