package abi

import (
	"errors"
	"runtime"
)

// MaxArguments returns the supported scalar entry arity and C call arity.
func MaxArguments() int {
	if runtime.GOOS != "linux" {
		return 8
	}
	return MaxArgumentsFor(runtime.GOARCH)
}

// MaxArgumentsFor returns the supported scalar entry and C call arity for
// an architecture. Unknown architectures retain the Builder's eight-slot limit.
func MaxArgumentsFor(architecture string) int {
	if architecture == "amd64" {
		return 6
	}
	return 8
}

// MaxGoCallArguments returns the supported scalar argument count for an
// outbound Go ABI call. It can exceed the generated function's entry arity.
func MaxGoCallArguments() int {
	if runtime.GOOS != "linux" {
		return 8
	}
	return MaxGoCallArgumentsFor(runtime.GOARCH)
}

// MaxGoCallArgumentsFor returns the outbound Go call arity for an architecture.
func MaxGoCallArgumentsFor(architecture string) int {
	switch architecture {
	case "amd64":
		return 7
	case "386", "arm64", "riscv64":
		return 9
	case "ppc64", "ppc64le":
		return 12
	default:
		return 8
	}
}

var (
	ErrInvalidSpec    = errors.New("abi: invalid call specification")
	ErrBackendLimit   = errors.New("abi: argument count exceeds backend limit")
	ErrUnsupportedABI = errors.New("abi: unsupported calling convention")
	ErrUnsupported    = errors.New("abi: unsupported platform")
)

// PreflightCall describes a call or syscall's conservative machine effects
// before the operation is lowered. It does not inspect the callee body.
func PreflightCall(spec CallSpec) (CallEffect, error) {
	architecture := spec.Architecture
	if architecture == "" {
		architecture = runtime.GOARCH
	}
	if spec.PPC64CABI != PPC64CABIUnspecified && (spec.ABI != C || spec.Kind != FunctionCall || architecture != "ppc64" && architecture != "ppc64le") {
		return CallEffect{}, ErrInvalidSpec
	}
	if spec.Kind != FunctionCall && spec.Kind != SystemCall {
		return CallEffect{}, ErrInvalidSpec
	}
	limit := MaxArgumentsFor(architecture)
	if spec.Architecture == "" {
		limit = MaxArguments()
	}
	if spec.Kind == FunctionCall && spec.ABI == Go {
		limit = MaxGoCallArgumentsFor(architecture)
		if spec.Architecture == "" {
			limit = MaxGoCallArguments()
		}
	}
	if spec.ArgumentCount < 0 || spec.ArgumentCount > limit || spec.Kind == SystemCall && spec.ArgumentCount > 6 {
		return CallEffect{}, ErrBackendLimit
	}
	if spec.ArgumentWords != nil {
		if len(spec.ArgumentWords) != spec.ArgumentCount {
			return CallEffect{}, ErrInvalidSpec
		}
		for _, words := range spec.ArgumentWords {
			if words != 1 && words != 2 {
				return CallEffect{}, ErrInvalidSpec
			}
		}
	}
	if spec.Kind == FunctionCall {
		if spec.ABI != C && spec.ABI != Go {
			return CallEffect{}, ErrUnsupportedABI
		}
	} else if spec.ABI != C {
		return CallEffect{}, ErrUnsupportedABI
	}
	if spec.Architecture == "" && runtime.GOOS != "linux" {
		return CallEffect{}, ErrUnsupported
	}
	var effect CallEffect
	var err error
	switch architecture {
	case "amd64":
		effect, err = preflightAMD64Call(spec)
	case "arm64":
		effect, err = preflightARM64Call(spec)
	case "386":
		effect, err = preflightI386Call(spec)
	case "riscv64":
		effect, err = preflightRISCV64Call(spec)
	case "ppc64", "ppc64le":
		convention := spec.PPC64CABI
		if spec.ABI == C && convention == PPC64CABIUnspecified {
			convention, _ = DefaultPPC64CABI(architecture)
		}
		var profile PPC64Profile
		profile, err = NewPPC64Profile(architecture, spec.ABI, convention)
		if err == nil {
			effect, err = profile.PreflightCall(spec.Kind, spec.ArgumentCount)
		}
	default:
		return CallEffect{}, ErrUnsupported
	}
	if err != nil {
		return CallEffect{}, err
	}
	if effect.ArgumentLocations == nil {
		effect.ArgumentLocations = make([]ValueLocation, len(effect.ArgumentRegisters))
		for i, name := range effect.ArgumentRegisters {
			effect.ArgumentLocations[i] = registerValue(name)
		}
	}
	if len(effect.ResultLocation.Words) == 0 && effect.ResultRegister != "" {
		effect.ResultLocation = registerValue(effect.ResultRegister)
	}
	return effect, nil
}

func registerValue(name string) ValueLocation {
	return ValueLocation{Words: []WordLocation{{Storage: Register, Register: name}}}
}

func stackValue(offsets ...int) ValueLocation {
	value := ValueLocation{Words: make([]WordLocation, len(offsets))}
	for i, offset := range offsets {
		value.Words[i] = WordLocation{Storage: Stack, StackOffset: offset}
	}
	return value
}
