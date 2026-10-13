// sha256 hashes four independent messages in parallel using 32-bit SIMD lanes.
// Go performs padding and transposition; generated code implements the message
// schedule, all 64 SHA-256 rounds, and state feed-forward for one block per lane.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"runtime"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// FIPS 180-4, sections 5.3.3 and 4.2.2: square/cube-root fractional bits.
var initial = [...]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}

func blockCount(message string) int {
	count := len(message)/64 + 1
	if len(message)%64 >= 56 {
		count++
	}
	return count
}

// paddedBlock synthesizes a block without making a padded copy of the input.
func paddedBlock(message string, index int) [64]byte {
	var block [64]byte
	full := len(message) / 64
	switch {
	case index < full:
		copy(block[:], message[index*64:(index+1)*64])
	case index == full:
		tail := len(message) % 64
		copy(block[:], message[full*64:])
		block[tail] = 0x80
	}
	if index == blockCount(message)-1 {
		binary.BigEndian.PutUint64(block[56:], uint64(len(message))*8)
	}
	return block
}

// sumBatch accepts up to four messages of independent lengths. Finished lanes
// may continue evolving while other lanes finish; snapshot their digest once.
func sumBatch(code *execution.Code, messages []string) ([][32]byte, error) {
	if len(messages) > 4 {
		return nil, fmt.Errorf("batch has %d messages; maximum is four", len(messages))
	}
	result := make([][32]byte, len(messages))
	if len(messages) == 0 {
		return result, nil
	}
	state := new([8][4]uint32)
	schedule := new([16][4]uint32)
	var pinned runtime.Pinner
	pinned.Pin(state)
	pinned.Pin(schedule)
	defer pinned.Unpin()
	maximum := 0
	for lane, message := range messages {
		for word, value := range initial {
			state[word][lane] = value
		}
		if n := blockCount(message); n > maximum {
			maximum = n
		}
	}
	for index := 0; index < maximum; index++ {
		clear(schedule[:])
		for lane, message := range messages {
			if index >= blockCount(message) {
				continue
			}
			block := paddedBlock(message, index)
			// Input bytes are big endian. Convert in Go to native uint32 words
			// so vector memory lane order matches the generated code.
			for word := range schedule {
				schedule[word][lane] = binary.BigEndian.Uint32(block[word*4:])
			}
		}
		_, err := code.Call(uintptr(unsafe.Pointer(state)), uintptr(unsafe.Pointer(schedule)))
		if err != nil {
			return nil, err
		}
		for lane, message := range messages {
			if index+1 == blockCount(message) {
				for word := range state {
					binary.BigEndian.PutUint32(result[lane][word*4:], state[word][lane])
				}
			}
		}
	}
	return result, nil
}

// sumMessages uses the same four-lane grouping for any number of CLI arguments.
func sumMessages(code *execution.Code, messages []string) ([][32]byte, error) {
	result := make([][32]byte, 0, len(messages))
	for first := 0; first < len(messages); first += 4 {
		sums, err := sumBatch(code, messages[first:min(first+4, len(messages))])
		if err != nil {
			return nil, err
		}
		result = append(result, sums...)
	}
	return result, nil
}

// listing renders the compression function's assembly for spec. Other
// architectures can be listed but not executed.
func listing(program ir.Input, spec compile.TargetSpec) (string, error) {
	target, err := compile.NewTarget(spec)
	if err != nil {
		return "", err
	}
	unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
	if err != nil {
		return "", err
	}
	return unit.Listing()
}

func main() {
	assembly := flag.String("assembly", "", "emit assembly for an architecture instead of hashing")
	scalar := flag.Bool("scalar", false, "force scalar lane lowering")
	flag.Parse()
	program, err := compressionProgram()
	if err != nil {
		log.Fatal(err)
	}
	spec := compile.TargetSpec{}
	if *scalar {
		spec.Vectors = compile.VectorsOff()
	}
	if *assembly != "" {
		spec.Architecture = *assembly
		text, err := listing(program, spec)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(text)
		return
	}
	code, err := nyanjit.Compile(program, spec, compile.Func{ABI: abi.C})
	if err != nil {
		log.Fatal(err)
	}
	defer code.Close()
	messages := flag.Args()
	if len(messages) == 0 {
		messages = []string{"", "abc", "nyanjit", "SIMD"}
	}
	sums, err := sumMessages(code, messages)
	if err != nil {
		log.Fatal(err)
	}
	for i, sum := range sums {
		fmt.Printf("%x  %q\n", sum, messages[i])
	}
}
