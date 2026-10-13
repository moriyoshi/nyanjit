package abi

// EntryLayout describes the scalar words at a generated function boundary.
type EntryLayout struct {
	Arguments []ValueLocation
	Result    ValueLocation
}

// ClassifyEntry assigns arguments with the supplied native word counts to ABI locations.
// The 386 C convention uses stack arguments and EAX:EDX; register-based
// targets supply their register names through paramName and resultName.
func ClassifyEntry(architecture string, flavor Flavor, argumentWords []int, maxArgs int, paramName func(int) string, resultName string) (EntryLayout, error) {
	arity := len(argumentWords)
	if architecture == "386" {
		if flavor == C {
			return Classify386CEntry(argumentWords)
		}
		if flavor == Go {
			return Classify386GoEntry(argumentWords)
		}
		return EntryLayout{}, ErrUnsupportedABI
	}
	if flavor != C && flavor != Go {
		return EntryLayout{}, ErrUnsupportedABI
	}
	if arity < 0 || arity > maxArgs {
		return EntryLayout{}, ErrBackendLimit
	}
	if paramName == nil || resultName == "" {
		return EntryLayout{}, ErrInvalidSpec
	}
	layout := EntryLayout{Arguments: make([]ValueLocation, arity), Result: registerValue(resultName)}
	for i := range layout.Arguments {
		name := paramName(i)
		if name == "" {
			return EntryLayout{}, ErrInvalidSpec
		}
		layout.Arguments[i] = registerValue(name)
	}
	return layout, nil
}

// Classify386CEntry describes cdecl arguments: values up to 32 bits occupy
// one stack word; 64-bit values occupy two consecutive words, low word first.
func Classify386CEntry(argumentWords []int) (EntryLayout, error) {
	arguments, _, err := stack386Arguments(argumentWords, 4, 8)
	if err != nil {
		return EntryLayout{}, err
	}
	return EntryLayout{Arguments: arguments, Result: ValueLocation{Words: []WordLocation{
		{Storage: Register, Register: "EAX"},
		{Storage: Register, Register: "EDX"},
	}}}, nil
}

// Classify386GoEntry describes the stack-based 386 Go ABI. The caller owns
// the argument words and the two-word result slot immediately after them.
func Classify386GoEntry(argumentWords []int) (EntryLayout, error) {
	arguments, end, err := stack386Arguments(argumentWords, 4, 8)
	if err != nil {
		return EntryLayout{}, err
	}
	return EntryLayout{Arguments: arguments, Result: stackValue(end, end+4)}, nil
}

func stack386Arguments(argumentWords []int, offset, limit int) ([]ValueLocation, int, error) {
	if len(argumentWords) > limit {
		return nil, 0, ErrBackendLimit
	}
	arguments := make([]ValueLocation, len(argumentWords))
	for i, words := range argumentWords {
		if words != 1 && words != 2 {
			return nil, 0, ErrInvalidSpec
		}
		arguments[i] = stackValue(offset)
		if words == 2 {
			arguments[i] = stackValue(offset, offset+4)
		}
		offset += 4 * words
	}
	return arguments, offset, nil
}

// RegisterValue describes a single register location.
func RegisterValue(name string) ValueLocation { return registerValue(name) }

// SingleRegister returns a register name only for a one-word register value.
func SingleRegister(value ValueLocation) string {
	if len(value.Words) != 1 || value.Words[0].Storage != Register {
		return ""
	}
	return value.Words[0].Register
}
