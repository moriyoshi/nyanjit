package compile

import (
	"errors"
	"fmt"

	"github.com/moriyoshi/nyanjit/abi"
)

type abiEntryLayout struct {
	arguments []ABIValueLocation
	result    ABIValueLocation
}

func rootEntryLayout(layout abi.EntryLayout, err error) (abiEntryLayout, error) {
	switch {
	case errors.Is(err, abi.ErrBackendLimit):
		return abiEntryLayout{}, ErrBackendLimit
	case errors.Is(err, abi.ErrUnsupportedABI):
		return abiEntryLayout{}, ErrUnsupportedABI
	case err != nil:
		return abiEntryLayout{}, ErrInvalidFunction
	}
	return abiEntryLayout{arguments: layout.Arguments, result: layout.Result}, nil
}

func entryLayout(target machineTarget, argumentWords []int) (abiEntryLayout, error) {
	layout, err := abi.ClassifyEntry(target.Architecture(), target.ABIFlavor(), argumentWords, abi.MaxArgumentsFor(target.Architecture()),
		func(index int) string { return target.RegisterName(target.ParamRegister(index)) },
		target.RegisterName(target.ResultRegister()))
	return rootEntryLayout(layout, err)
}

func c386EntryLayout(argumentWords []int) (abiEntryLayout, error) {
	layout, err := abi.Classify386CEntry(argumentWords)
	return rootEntryLayout(layout, err)
}

func registerABIValue(name string) ABIValueLocation   { return abi.RegisterValue(name) }
func singleABIRegister(value ABIValueLocation) string { return abi.SingleRegister(value) }

// entryWordsFromIR includes unused parameters; the entry layout is a signature,
// independent of liveness or instruction selection.
func entryWordsFromIR(ops []instruction, registerBits uint8) []int {
	var words []int
	for _, ins := range ops {
		if ins.Op == opParam {
			words = append(words, (int(instructionBits(ins))+int(registerBits)-1)/int(registerBits))
		}
	}
	return words
}

func selectedArgumentWords(program machineProgram, args []int) []int {
	words := make([]int, len(args))
	for i, id := range args {
		words[i] = max(1, int(program.Values[id].Words))
	}
	return words
}

func argumentLocationWords(locations []ABIValueLocation) []int {
	words := make([]int, len(locations))
	for i, location := range locations {
		words[i] = len(location.Words)
	}
	return words
}

// The 386 call lowering uses whole-word slots. Adjacent subword Go params
// instead share a word in ABI0, so reject this shape before emitting a call.
func validate386GoSubwords(bits []uint8) error {
	for i := 1; i < len(bits); i++ {
		if bits[i-1] != 0 && bits[i] != 0 && bits[i-1] < 32 && bits[i] < 32 {
			return fmt.Errorf("386 CallGo arguments %d (%d bits) and %d (%d bits) require packed Go ABI0 subword slots; widen the arguments and callee parameters to 32 bits: %w", i-1, bits[i-1], i, bits[i], ErrBackendLimit)
		}
	}
	return nil
}
