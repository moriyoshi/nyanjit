package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

var digestSink [4][32]byte
var programSink *ir.SealedProgram

func benchmarkSpec(scalar bool) compile.TargetSpec {
	if scalar {
		return compile.TargetSpec{Vectors: compile.VectorsOff()}
	}
	return compile.TargetSpec{}
}

func skipWithoutSIMD(b *testing.B) {
	b.Helper()
	target, err := compile.NewTarget(benchmarkSpec(false))
	if err != nil {
		b.Fatal(err)
	}
	if target.Vectors().PreferredBits == 0 {
		b.Skip("no native SIMD backend on this host")
	}
}

func benchmarkCode(b *testing.B, scalar bool) *execution.Code {
	b.Helper()
	if !scalar {
		skipWithoutSIMD(b)
	}
	program, err := compressionProgram()
	if err != nil {
		b.Fatal(err)
	}
	target, err := compile.NewTarget(benchmarkSpec(scalar))
	if err != nil {
		b.Fatal(err)
	}
	unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
	if err != nil {
		b.Fatal(err)
	}
	template, err := unit.Template()
	if errors.Is(err, execution.ErrUnsupported) {
		b.Skip(err)
	}
	if err != nil {
		b.Fatal(err)
	}
	report := template.Report()
	b.Logf("code=%d bytes; workspace=%d bytes/call; stack=%d bytes", report.CodeBytes(), report.WorkspaceBytes(), report.StackBytes())
	code, err := template.Bind(nil)
	if errors.Is(err, execution.ErrUnsupported) {
		b.Skip(err)
	}
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := code.Close(); err != nil {
			b.Error(err)
		}
	})
	return code
}

func benchmarkMessages(lengths [4]int) ([]string, [4][]byte) {
	messages := make([]string, 4)
	var inputs [4][]byte
	for lane, size := range lengths {
		inputs[lane] = make([]byte, size)
		for i := range inputs[lane] {
			inputs[lane][i] = byte(i*31 + lane*17)
		}
		messages[lane] = string(inputs[lane])
	}
	return messages, inputs
}

// BenchmarkSHA256 times the public example's complete hashing path. One op
// hashes four independent messages, including padding, transposition, pinned
// buffers, every Code.Call and output allocation. Compilation and conversion
// of the standard-library inputs to []byte are outside the timed region.
func BenchmarkSHA256(b *testing.B) {
	type benchCase struct {
		name    string
		lengths [4]int
	}
	cases := []benchCase{}
	for _, size := range []int{32, 64, 1024, 16384} {
		cases = append(cases, benchCase{fmt.Sprintf("%dB", size), [4]int{size, size, size, size}})
	}
	cases = append(cases, benchCase{"Mixed", [4]int{0, 55, 56, 1024}})
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			messages, inputs := benchmarkMessages(tc.lengths)
			total := int64(0)
			var want [4][32]byte
			for lane := range inputs {
				total += int64(len(inputs[lane]))
				want[lane] = sha256.Sum256(inputs[lane])
			}
			for _, variant := range []struct {
				name   string
				scalar bool
			}{{"JITSIMD", false}, {"JITScalar", true}} {
				b.Run(variant.name, func(b *testing.B) {
					code := benchmarkCode(b, variant.scalar)
					got, err := sumBatch(code, messages)
					if err != nil {
						b.Fatal(err)
					}
					for lane := range want {
						if got[lane] != want[lane] {
							b.Fatalf("lane %d: got %x, want %x", lane, got[lane], want[lane])
						}
					}
					b.ReportAllocs()
					b.SetBytes(total)
					for b.Loop() {
						sums, err := sumBatch(code, messages)
						if err != nil {
							b.Fatal(err)
						}
						copy(digestSink[:], sums)
					}
					b.ReportMetric(4, "messages/op")
				})
			}
			b.Run("GoStdlib", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(total)
				for b.Loop() {
					for lane := range inputs {
						digestSink[lane] = sha256.Sum256(inputs[lane])
					}
				}
				b.ReportMetric(4, "messages/op")
			})
		})
	}
}

// BenchmarkSHA256Compression measures one block per lane with preallocated,
// pinned, transposed input/state. It includes Code.Call and its workspace
// handling. State and schedule deliberately evolve; no data preparation or
// padding is measured, and bytes/op includes all four 64-byte blocks.
func BenchmarkSHA256Compression(b *testing.B) {
	for _, variant := range []struct {
		name   string
		scalar bool
	}{{"JITSIMD", false}, {"JITScalar", true}} {
		b.Run(variant.name, func(b *testing.B) {
			code := benchmarkCode(b, variant.scalar)
			state := new([8][4]uint32)
			schedule := new([16][4]uint32)
			var pinned runtime.Pinner
			pinned.Pin(state)
			pinned.Pin(schedule)
			defer pinned.Unpin()
			for word, value := range initial {
				for lane := range state[word] {
					state[word][lane] = value
				}
			}
			for word := range schedule {
				for lane := range schedule[word] {
					schedule[word][lane] = uint32(word*31 + lane*17)
				}
			}
			stateAddress, scheduleAddress := uintptr(unsafe.Pointer(state)), uintptr(unsafe.Pointer(schedule))
			b.ReportAllocs()
			b.SetBytes(4 * 64)
			for b.Loop() {
				if _, err := code.Call(stateAddress, scheduleAddress); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(4, "blocks/op")
		})
	}
}

// BenchmarkSHA256Build measures construction of an immutable Builder program.
func BenchmarkSHA256Build(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		program, err := compressionProgram()
		if err != nil {
			b.Fatal(err)
		}
		programSink = program
	}
}

// BenchmarkSHA256Compile excludes Builder construction; each operation lowers
// the same immutable IR, maps executable code, and closes that mapping.
func BenchmarkSHA256Compile(b *testing.B) {
	program, err := compressionProgram()
	if err != nil {
		b.Fatal(err)
	}
	for _, variant := range []struct {
		name   string
		scalar bool
	}{{"JITSIMD", false}, {"JITScalar", true}} {
		b.Run(variant.name, func(b *testing.B) {
			if !variant.scalar {
				skipWithoutSIMD(b)
			}
			b.ReportAllocs()
			for b.Loop() {
				code, err := nyanjit.Compile(program, benchmarkSpec(variant.scalar), compile.Func{ABI: abi.C})
				if errors.Is(err, execution.ErrUnsupported) {
					b.Skip(err)
				}
				if err != nil {
					b.Fatal(err)
				}
				if err := code.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
