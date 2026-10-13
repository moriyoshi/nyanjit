package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestScratchDropsOversizedBuffers(t *testing.T) {
	s := getCompileScratch(compileOptions{})
	s.allocation.definedAt = make([]int, scratchBufferLimit/4+1)
	// On 32-bit targets use a byte-sized buffer so this checks the same cap.
	s.control.resultWordCounts = make([]uint8, scratchBufferLimit+1)
	putCompileScratch(s)
	if s.machine == nil || s.allocation.definedAt != nil || s.control.resultWordCounts != nil {
		t.Fatal("oversized buffers survived, or discarded unrelated workspace")
	}
}

func TestScratchTrimsLargeMachineBuffersIndividually(t *testing.T) {
	s := getCompileScratch(compileOptions{})
	s.allocation.definedAt = make([]int, 16)
	original := &s.allocation.definedAt[0]
	stream := s.machine.Stream(machine.StreamOptions{})
	var one [1]machine.Instruction
	stream.Reserve(scratchPointerLimit/bufferBytes(one[:]) + 1)
	if _, big := s.machine.RetainedBytes(scratchPointerLimit); !big {
		t.Fatal("test did not reserve a large machine buffer")
	}
	putCompileScratch(s)
	if s.machine == nil || len(s.allocation.definedAt) != 16 || &s.allocation.definedAt[0] != original {
		t.Fatal("large machine buffer discarded unrelated reusable tables")
	}
	if _, big := s.machine.RetainedBytes(scratchPointerLimit); big {
		t.Fatal("large pointer-bearing buffer was retained")
	}
}

func TestScratchReleasesUnusedStreamsBetweenCompiles(t *testing.T) {
	s := &compileScratch{machine: new(machine.Workspace)}
	var one [1]machine.Instruction
	count := scratchPointerLimit / (2 * bufferBytes(one[:]))
	lease := func() { s.machine.Stream(machine.StreamOptions{}).Reserve(count) }
	lease() // Main stream.
	s.machine.Rewind(1)
	lease()
	lease() // A retried fragment.
	before, _ := s.machine.RetainedBytes(scratchPointerLimit)
	s.machine.Rewind(1)
	lease() // A smaller final fragment.
	s.machine.Trim(scratchPointerLimit)
	kept, _ := s.machine.RetainedBytes(scratchPointerLimit)
	if kept != before {
		t.Fatal("a final small block lost buffers used earlier in the same compile")
	}
	s.machine.Release()
	s.machine.Reset()
	lease() // The next compile uses only the main stream.
	putCompileScratch(s)
	after, _ := s.machine.RetainedBytes(scratchPointerLimit)
	if after >= kept/2 {
		t.Fatalf("unused streams survived into the next compile: %d -> %d bytes", kept, after)
	}
}
