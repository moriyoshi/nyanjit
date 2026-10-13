package machine

import (
	"math/rand"
	"testing"
)

func TestThreadedLabelsMatchesSequentialSearch(t *testing.T) {
	random := rand.New(rand.NewSource(19))
	for trial := 0; trial < 1000; trial++ {
		const count = 64
		old := make([]Instruction, count)
		indices := make([]int, count)
		bound := make([]bool, count)
		for id := range old {
			indices[id] = id
			bound[id] = true
			old[id] = Instruction{Op: OpJump, Imm: uint64(random.Intn(count))}
			if random.Intn(5) == 0 {
				old[id].Op = OpReturn
			}
		}
		got, err := threadedLabels(old, indices, bound)
		if err != nil {
			t.Fatal(err)
		}
		// The old pass rewrote earlier jumps while processing later ones. Compare
		// against that complete behavior, including cycles and self-loops.
		working := append([]Instruction(nil), old...)
		for id, ins := range working {
			if !isBranch(ins.Op) {
				continue
			}
			start := int(ins.Imm)
			destination := start
			seen := map[int]bool{}
			for {
				if seen[destination] {
					destination = start
					break
				}
				seen[destination] = true
				if working[destination].Op != OpJump {
					break
				}
				next := int(working[destination].Imm)
				if next == destination {
					break
				}
				destination = next
			}
			working[id].Imm = uint64(destination)
			if got[start] != destination {
				t.Fatalf("trial%d label%d: got%d want%d", trial, start, got[start], destination)
			}
		}
	}
}
