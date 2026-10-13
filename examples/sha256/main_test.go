package main

import (
	"crypto/sha256"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestSHA256(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("executable backend tests currently run on Linux")
	}
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "riscv64", "ppc64le", "loong64":
	default:
		t.Skip("no executable backend")
	}
	p, err := compressionProgram()
	if err != nil {
		t.Fatal(err)
	}
	lengths := []int{0, 1, 55, 56, 63, 64, 65, 119, 120, 128, 1000}
	messages := make([]string, len(lengths))
	for i, n := range lengths {
		// Include NUL and non-ASCII bytes: hashes operate on bytes, not runes.
		messages[i] = strings.Repeat("\x00\xffnyanjit\x80", (n+9)/10)[:n]
	}
	host, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	for _, scalar := range []bool{false, true} {
		if !scalar && host.Vectors().PreferredBits == 0 {
			continue
		}
		t.Run(fmt.Sprintf("scalar=%t", scalar), func(t *testing.T) {
			spec := compile.TargetSpec{}
			if scalar {
				spec.Vectors = compile.VectorsOff()
			}
			code, err := nyanjit.Compile(p, spec, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			check := func(t *testing.T, input []string, grouped bool) {
				t.Helper()
				hash := sumBatch
				if grouped {
					hash = sumMessages
				}
				got, err := hash(code, input)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(input) {
					t.Fatalf("got %d digests for %d messages", len(got), len(input))
				}
				for i, msg := range input {
					if want := sha256.Sum256([]byte(msg)); got[i] != want {
						t.Fatalf("message %d (%d bytes): got %x want %x", i, len(msg), got[i], want)
					}
				}
			}
			for size := 1; size <= 4; size++ {
				for start := range messages {
					batch := make([]string, size)
					for lane := range batch {
						batch[lane] = messages[(start+lane)%len(messages)]
					}
					t.Run(fmt.Sprintf("batch=%d/start=%d", size, start), func(t *testing.T) { check(t, batch, false) })
				}
			}
			t.Run("cli-grouping", func(t *testing.T) { check(t, messages, true) })
			t.Run("empty", func(t *testing.T) { check(t, nil, true); check(t, nil, false) })
			if _, err := sumBatch(code, messages[:5]); err == nil {
				t.Fatal("oversized single batch accepted")
			}
		})
	}
}

func Test386SHA256InstructionCounts(t *testing.T) {
	if runtime.GOARCH != "386" {
		t.Skip("measure the 32-bit builder's native-width program")
	}
	p, err := compressionProgram()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, mode := range []string{"scalar", "SSE2"} {
		spec := compile.TargetSpec{Architecture: "386", Vectors: compile.VectorsOff()}
		if mode == "SSE2" {
			spec.Vectors = compile.VectorsRequire("sse2")
		}
		text, err := listing(p, spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasSuffix(line, ":") && !strings.Contains(line, ".byte") {
				counts[mode]++
			}
		}
		t.Logf("386 SHA-256 %s: %d instructions", mode, counts[mode])
	}
	if counts["SSE2"] >= counts["scalar"] {
		t.Fatalf("native SIMD did not reduce instructions: %v", counts)
	}
}

func TestLoong64SHA256InstructionCounts(t *testing.T) {
	if runtime.GOARCH != "loong64" {
		t.Skip("measure LoongArch lowering")
	}
	p, err := compressionProgram()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, mode := range []string{"scalar", "LSX"} {
		spec := compile.TargetSpec{Architecture: "loong64", CPU: abi.CPUProfile{Extensions: []string{"lsx"}}, Vectors: compile.VectorsOff()}
		if mode == "LSX" {
			spec.Vectors = compile.VectorsRequire("lsx")
		}
		text, err := listing(p, spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasSuffix(line, ":") && !strings.Contains(line, ".byte") {
				counts[mode]++
			}
		}
		t.Logf("loong64 SHA-256 %s: %d instructions", mode, counts[mode])
	}
	if counts["LSX"] >= counts["scalar"] {
		t.Fatalf("LSX did not reduce instructions: %v", counts)
	}
}
