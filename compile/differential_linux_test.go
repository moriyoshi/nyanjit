//go:build linux && (amd64 || arm64 || 386 || riscv64)

package compile

import (
	"fmt"
	"math/bits"
	"math/rand"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
)

func nativeArithmeticProgram() (*builder.Builder, error) {
	b := builder.NewBuilder()
	x, err := b.ParamNative()
	if err != nil {
		return nil, err
	}
	y, err := b.ParamNative()
	if err != nil {
		return nil, err
	}
	scaled := x.Add(y.MulConst(13))
	mixed, err := b.Xor(scaled, x)
	if err != nil {
		return nil, err
	}
	rotated, err := b.RotateLeft(mixed, 7)
	if err != nil {
		return nil, err
	}
	if err := b.Return(rotated); err != nil {
		return nil, err
	}
	return b, nil
}

func TestConcurrentNativeArithmeticCalls(t *testing.T) {
	b, err := nativeArithmeticProgram()
	if err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	const workers, iterations = 8, 200
	var group sync.WaitGroup
	failures := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for i := 0; i < iterations; i++ {
				x, y := uint64(worker*iterations+i), uint64(i*17+worker)
				got, err := code.Call(uintptr(x), uintptr(y))
				if err != nil {
					failures <- err
					return
				}
				var want uint64
				if builder.NativeBits == 32 {
					want = uint64(bits.RotateLeft32((uint32(x)+uint32(y)*13)^uint32(x), 7))
				} else {
					want = bits.RotateLeft64((x+y*13)^x, 7)
				}
				if uint64(got) != want {
					failures <- fmt.Errorf("worker %d case %d: got %#x, want %#x", worker, i, got, want)
					return
				}
			}
		}(worker)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestDifferentialNativeArithmetic(t *testing.T) {
	b, err := nativeArithmeticProgram()
	if err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	random := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		x, y := random.Uint64(), random.Uint64()
		got, err := code.Call(uintptr(x), uintptr(y))
		if err != nil {
			t.Fatal(err)
		}
		var want uint64
		if builder.NativeBits == 32 {
			want = uint64(bits.RotateLeft32((uint32(x)+uint32(y)*13)^uint32(x), 7))
		} else {
			want = bits.RotateLeft64((x+y*13)^x, 7)
		}
		if uint64(got) != want {
			t.Fatalf("case %d: Call(%#x, %#x) = %#x, want %#x", i, x, y, got, want)
		}
	}
}

func BenchmarkNativeArithmeticCompile(b *testing.B) {
	constructed, err := nativeArithmeticProgram()
	if err != nil {
		b.Fatal(err)
	}
	program, err := constructed.Finish()
	if err != nil {
		b.Fatal(err)
	}
	options := CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		code, err := Compile(program, options)
		if err != nil {
			b.Fatal(err)
		}
		if err := code.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNativeArithmeticCall(b *testing.B) {
	constructed, err := nativeArithmeticProgram()
	if err != nil {
		b.Fatal(err)
	}
	program, err := constructed.Finish()
	if err != nil {
		b.Fatal(err)
	}
	code, err := Compile(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		b.Fatal(err)
	}
	defer code.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := code.Call(uintptr(i), 19); err != nil {
			b.Fatal(err)
		}
	}
}
