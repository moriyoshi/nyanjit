package compile

import (
	"errors"
	"fmt"
	"runtime"
	"slices"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func targetArchitecture(options compileOptions) string {
	if options.Architecture != "" {
		return options.Architecture
	}
	if runtime.GOOS != "linux" {
		return ""
	}
	return runtime.GOARCH
}

func validateEmissionTarget(options compileOptions, kind emissionKind) error {
	_, err := emissionCPU(options, kind)
	return err
}

func emissionCPU(options compileOptions, kind emissionKind) (abi.ResolvedCPU, error) {
	architecture := targetArchitecture(options)
	switch architecture {
	case "amd64", "386", "arm64", "riscv64", "ppc64le", "loong64":
	default:
		return abi.ResolvedCPU{}, fmt.Errorf("unknown emission architecture %q: %w", architecture, ErrUnsupported)
	}
	if kind.native() && (runtime.GOOS != "linux" || architecture != runtime.GOARCH) {
		return abi.ResolvedCPU{}, fmt.Errorf("native emission requires the host Linux architecture: %w", ErrUnsupported)
	}
	requested, err := resolveCPUProfile(options)
	if err != nil {
		if errors.Is(err, ErrCPUUnsupported) {
			return abi.ResolvedCPU{}, err
		}
		return abi.ResolvedCPU{}, fmt.Errorf("invalid CPU profile: %w: %w", err, ErrInvalidFunction)
	}
	if kind.native() {
		if err := validateResolvedCPUCompatibility(requested, compilerHostCPU); err != nil {
			return abi.ResolvedCPU{}, err
		}
	}
	return requested, nil
}

func validateCPUCompatibility(requested, host abi.CPUProfile) error {
	if !host.Supports(requested) {
		return fmt.Errorf("requested CPU %s/%v exceeds verified host %s/%v: %w", requested.Level, requested.Extensions, host.Level, host.Extensions, ErrCPUUnsupported)
	}
	return nil
}

func newMachineTarget(options compileOptions) machineTarget {
	switch targetArchitecture(options) {
	case "amd64":
		return newAMD64MachineTarget(options)
	case "arm64":
		return newARM64MachineTarget(options)
	case "386":
		return newI386MachineTarget(options)
	case "loong64":
		return newLoong64MachineTarget(options)
	case "riscv64":
		return newRISCV64MachineTarget(options)
	case "ppc64le":
		return newPPC64MachineTarget(options)
	default:
		return nil
	}
}

func newMachineEmitter(options compileOptions, scratch ...*compileScratch) machineEmitter {
	return newMachineEmitterIn(new(physicalEmitter), options, scratch...)
}

// newMachineEmitterIn builds the target's emitter in e, overwriting every
// field, so callers can supply the storage.
func newMachineEmitterIn(e *physicalEmitter, options compileOptions, scratch ...*compileScratch) machineEmitter {
	switch targetArchitecture(options) {
	case "amd64":
		initAMD64MachineEmitter(e, options)
	case "arm64":
		initARM64MachineEmitter(e, options)
	case "386":
		initI386MachineEmitter(e, options)
	case "loong64":
		initLoong64MachineEmitter(e, options)
	case "riscv64":
		initRISCV64MachineEmitter(e, options)
	case "ppc64le":
		if !initPPC64MachineEmitter(e, options) {
			return nil
		}
	default:
		return nil
	}
	e.harden = options.Harden
	e.withholdPreserved = options.withholdPreserved
	if len(scratch) > 0 && scratch[0] != nil {
		options := machine.StreamOptions{Size: e.size, FlagEffect: e.flagEffect, FlagFacts: e.flagFacts, ExactFlagFacts: e.binarySetsFlags || e.decSetsCarry, BinarySetsFlags: e.binarySetsFlags, DecSetsCarry: e.decSetsCarry}
		e.stream = scratch[0].machine.Stream(options)
	}
	return e
}

func newMachineAssembler(options compileOptions, kind emissionKind) machineAssembler {
	switch targetArchitecture(options) {
	case "amd64":
		return newAMD64MachineAssembler(options, kind)
	case "arm64":
		return newARM64MachineAssembler(options, kind)
	case "386":
		return newI386MachineAssembler(options, kind)
	case "loong64":
		return newLoong64MachineAssembler(options, kind)
	case "riscv64":
		return newRISCV64MachineAssembler(options, kind)
	case "ppc64le":
		return newPPC64MachineAssembler(options, kind)
	default:
		return nil
	}
}

func resolveCPUProfile(options compileOptions) (abi.ResolvedCPU, error) {
	if t := options.target; t != nil && t.resolvedFor(options) {
		return t.cpu, nil
	}
	arch := targetArchitecture(options)
	requested := options.CPU
	if requested.Level == "" && len(requested.Extensions) == 0 && options.Architecture == "" && arch != "ppc64le" {
		requested = compilerHostCPU
	}
	// An explicitly requested SIMD extension is also a minimum CPU contract.
	// Default SIMD detection never adds an extension absent from this profile.
	if (arch == "amd64" || arch == "386") && slices.Contains(options.Features, "pclmulqdq") {
		if !requested.Has("pclmulqdq") {
			requested.Extensions = append(append([]string(nil), requested.Extensions...), "pclmulqdq")
		}
	}
	if arch == "loong64" {
		for _, feature := range options.Features {
			if feature != "lsx" && feature != "lasx" {
				continue
			}
			if options.CPU.Level != "" || len(options.CPU.Extensions) != 0 {
				profile, err := requested.Resolved(arch)
				if err != nil {
					return abi.ResolvedCPU{}, err
				}
				if !profile.Has(feature) {
					return abi.ResolvedCPU{}, fmt.Errorf("SIMD feature %q exceeds CPU profile %s/%v: %w", feature, profile.Level(), profile.AppendExtensions(nil), ErrCPUUnsupported)
				}
			}
			if !requested.Has(feature) {
				requested.Extensions = append(append([]string(nil), requested.Extensions...), feature)
			}
		}
	}
	return requested.Resolved(arch)
}
func resolvedCPU(options compileOptions) abi.CPUProfile { p, _ := resolveCPU(options); return p }

// Host detection is process-invariant. Keep its detached snapshot private.
var compilerHostCPU = cpuprofile.Host()

func resolveCPU(options compileOptions) (abi.CPUProfile, error) {
	p, err := resolveCPUProfile(options)
	return p.Profile(), err
}
func resolvedFeatures(options compileOptions) abi.ResolvedCPU {
	p, _ := resolveCPUProfile(options)
	return p
}
func reportCPU(options compileOptions) abi.CPUProfile { return resolvedCPU(options) }

func validateResolvedCPUCompatibility(requested abi.ResolvedCPU, host abi.CPUProfile) error {
	compatible := host.Supports(abi.CPUProfile{Level: requested.Level()})
	for extension := range requested.Extensions() {
		if !host.Has(extension) {
			compatible = false
			break
		}
	}
	if !compatible {
		return fmt.Errorf("requested CPU %s/%v exceeds verified host %s/%v: %w", requested.Level(), requested.AppendExtensions(nil), host.Level, host.Extensions, ErrCPUUnsupported)
	}
	return nil
}
