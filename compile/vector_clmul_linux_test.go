//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Polynomial multiplication over GF(2), independently of vector legalization.
func referenceCarrylessMul(x, y uint64) [2]uint64 {
	var product [2]uint64
	for bit := 0; bit < 64; bit++ {
		if y>>bit&1 != 0 {
			product[0] ^= x << bit
			if bit != 0 {
				product[1] ^= x >> (64 - bit)
			}
		}
	}
	return product
}

func TestCarrylessMulDifferential(t *testing.T) {
	hasNative := (runtime.GOARCH == "amd64" || runtime.GOARCH == "386") && cpuprofile.Host().Has("pclmulqdq")
	for _, convention := range []struct {
		name string
		abi  abi.Flavor
	}{{"C", abi.C}, {"Go", abi.Go}} {
		for _, mode := range []string{"default", "scalar", "pclmulqdq"} {
			t.Run(convention.name+"/"+mode, func(t *testing.T) {
				if mode == "pclmulqdq" && !hasNative {
					t.Skip("host has no verified PCLMULQDQ")
				}
				for _, lanes := range []uint16{2, 4, 8} {
					for selector := uint16(0); selector < 4; selector++ {
						t.Run(fmt.Sprintf("lanes=%d/selector=%d", lanes, selector), func(t *testing.T) {
							xLane, yLane := selector&1, selector>>1
							b := nyanjit.NewBuilder()
							in1, in2, out := b.ParamNative(), b.ParamNative(), b.ParamNative()
							typ := ir.Vec(lanes, 64)
							size := int(lanes) * 8
							x, y := b.VLoad(typ, in1), b.VLoad(typ, in2)
							product := b.CarrylessMul(x, y, xLane, yLane)
							b.VStore(out, product)
							// Both input positions receive a prior result; stores materialize each
							// stage once, and last-use reuse can destructively update a register.
							product = b.CarrylessMul(product, y, xLane, yLane)
							b.VStore(b.Add(out, b.ConstNative(uintptr(size))), product)
							product = b.CarrylessMul(x, product, xLane, yLane)
							b.VStore(b.Add(out, b.ConstNative(uintptr(2*size))), product)
							b.Return(b.ConstNative(0))
							p, err := testFinishRaw(b)
							if err != nil {
								t.Fatal(err)
							}
							var spec compile.TargetSpec
							options := compile.Func{ABI: convention.abi}
							switch mode {
							case "scalar":
								spec.Vectors = compile.VectorsOff()
							case "pclmulqdq":
								spec.Vectors = compile.VectorsRequire("pclmulqdq")
							}
							if runtime.GOARCH == "386" {
								// The scalar expansion's 64-bit shifts use paired-width helper calls.
								options.Stack = compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}
								if convention.abi == abi.C {
									options.Context = compile.ContextForeign
								}
							}
							template, err := lowerTemplate(p, spec, options)
							if err != nil {
								t.Fatal(err)
							}
							want := abi.Scalarized
							if hasNative && mode != "scalar" {
								want = abi.Native
								if lanes > 2 {
									want = abi.Split
								}
							}
							products := 0
							for _, operation := range template.Report().VectorOperations() {
								if ir.Opcode(operation.Operation) == ir.OpVCarrylessMul {
									products++
									if operation.Lowering != want {
										t.Fatalf("reported lowering %v want %v", operation.Lowering, want)
									}
								}
							}
							if products != 3 {
								t.Fatalf("reported %d products; deferred stages should materialize once", products)
							}
							code, err := template.Bind(nil)
							if runtime.GOARCH == "386" && convention.abi == abi.Go && errors.Is(err, compile.ErrBackendLimit) {
								t.Logf("Go entry correctly rejects an oversized goroutine frame: %v; C foreign entry exercises this scalar expansion", err)
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							defer code.Close()
							m, address := simdMemory(t)
							random := rand.New(rand.NewPCG(uint64(lanes), uint64(selector)))
							for sample := 0; sample < 12; sample++ {
								for i := range m {
									m[i] = 0xa5
								}
								for lane := 0; lane < int(lanes); lane++ {
									a, c := random.Uint64(), random.Uint64()
									switch sample {
									case 0:
										a, c = 0, 0
									case 1:
										a, c = ^uint64(0), ^uint64(0)
									case 2:
										a, c = 1<<63, 1<<63
									case 3:
										a, c = 0, ^uint64(0)
									case 4:
										a, c = ^uint64(0), 0
									case 5:
										a, c = 1<<63, 1
									case 6:
										a, c = 1, 1<<63
									}
									writeSIMDLane(m[1+lane*8:], 64, a)
									writeSIMDLane(m[129+lane*8:], 64, c)
								}
								inputX, inputY := bytes.Clone(m[1:1+size]), bytes.Clone(m[129:129+size])
								if _, err := code.Call(address+1, address+129, address+257); err != nil {
									t.Fatal(err)
								}
								if !bytes.Equal(inputX, m[1:1+size]) || !bytes.Equal(inputY, m[129:129+size]) {
									t.Fatal("product modified an input buffer")
								}
								for chunk := 0; chunk < int(lanes); chunk += 2 {
									X := [2]uint64{readSIMDLane(inputX[chunk*8:], 64), readSIMDLane(inputX[(chunk+1)*8:], 64)}
									Y := [2]uint64{readSIMDLane(inputY[chunk*8:], 64), readSIMDLane(inputY[(chunk+1)*8:], 64)}
									want := referenceCarrylessMul(X[xLane], Y[yLane])
									for stage := 0; stage < 3; stage++ {
										if stage == 1 {
											want = referenceCarrylessMul(want[xLane], Y[yLane])
										}
										if stage == 2 {
											want = referenceCarrylessMul(X[xLane], want[yLane])
										}
										for word := 0; word < 2; word++ {
											got := readSIMDLane(m[257+stage*size+(chunk+word)*8:], 64)
											if got != want[word] {
												t.Fatalf("sample=%d chunk=%d stage=%d word=%d: got %016x want %016x", sample, chunk/2, stage, word, got, want[word])
											}
										}
									}
								}
								if m[256] != 0xa5 || m[257+3*size] != 0xa5 {
									t.Fatal("product overwrote output guard")
								}
							}
						})
					}
				}
			})
		}
	}
}

func TestCarrylessMulNativeGate(t *testing.T) {
	if (runtime.GOARCH != "amd64" && runtime.GOARCH != "386") || cpuprofile.Host().Has("pclmulqdq") {
		t.Skip("requires an x86 CPU without PCLMULQDQ; deterministic profile gate is covered separately")
	}
	b := nyanjit.NewBuilder()
	b.Return(b.ConstNative(42))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []compile.TargetSpec{
		{Vectors: compile.VectorsRequire("pclmulqdq")},
		{CPU: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}},
	} {
		code, err := nyanjit.Compile(p, spec, compile.Func{})
		if code != nil {
			code.Close()
			t.Fatal("unsupported explicit CPU request produced executable code")
		}
		if !errors.Is(err, compile.ErrCPUUnsupported) || !errors.Is(err, compile.ErrBackendLimit) {
			t.Fatalf("unsupported PCLMULQDQ request: %v", err)
		}
	}
}
