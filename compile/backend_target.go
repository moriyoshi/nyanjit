package compile

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/cpuprofile"
)

func targetArchitecture(options CompileOptions) string {
	if options.Architecture != "" {
		return options.Architecture
	}
	if runtime.GOOS != "linux" {
		return ""
	}
	return runtime.GOARCH
}

func validateEmissionTarget(options CompileOptions, kind emissionKind) error {
	architecture := targetArchitecture(options)
	switch architecture {
	case "amd64", "386", "arm64", "riscv64", "ppc64", "ppc64le":
	default:
		return fmt.Errorf("unknown emission architecture %q: %w", architecture, ErrUnsupported)
	}
	if kind == emitNative && (runtime.GOOS != "linux" || architecture != runtime.GOARCH) {
		return fmt.Errorf("native emission requires the host Linux architecture: %w", ErrUnsupported)
	}
	requested, err := resolveCPU(options)
	if err != nil {
		return fmt.Errorf("invalid CPU profile: %v: %w", err, ErrInvalidFunction)
	}
	if kind == emitNative && !cpuprofile.Host().Supports(requested) {
		return fmt.Errorf("requested CPU %s/%v exceeds verified host %s/%v: %w", requested.Level, requested.Extensions, cpuprofile.Host().Level, cpuprofile.Host().Extensions, ErrBackendLimit)
	}
	return validateEntryConvention(options)
}

func newMachineTarget(options CompileOptions) machineTarget {
	switch targetArchitecture(options) {
	case "amd64":
		return newAMD64MachineTarget(options)
	case "arm64":
		return newARM64MachineTarget(options)
	case "386":
		return newI386MachineTarget(options)
	case "riscv64":
		return newRISCV64MachineTarget(options)
	case "ppc64", "ppc64le":
		return newPPC64MachineTarget(options)
	default:
		return nil
	}
}

func newMachineEmitter(options CompileOptions) machineEmitter {
	switch targetArchitecture(options) {
	case "amd64":
		return newAMD64MachineEmitter(options)
	case "arm64":
		return newARM64MachineEmitter(options)
	case "386":
		return newI386MachineEmitter(options)
	case "riscv64":
		return newRISCV64MachineEmitter(options)
	case "ppc64", "ppc64le":
		return newPPC64MachineEmitter(options)
	default:
		return nil
	}
}

func newMachineAssembler(options CompileOptions, kind emissionKind) machineAssembler {
	switch targetArchitecture(options) {
	case "amd64":
		return newAMD64MachineAssembler(options, kind)
	case "arm64":
		return newARM64MachineAssembler(options, kind)
	case "386":
		return newI386MachineAssembler(options, kind)
	case "riscv64":
		return newRISCV64MachineAssembler(options, kind)
	case "ppc64", "ppc64le":
		return newPPC64MachineAssembler(options, kind)
	default:
		return nil
	}
}

func resolveCPU(options CompileOptions) (abi.CPUProfile, error) {
	arch := targetArchitecture(options)
	requested := options.CPU.Clone()
	if options.AMD64FMA {
		if arch != "amd64" {
			return abi.CPUProfile{}, abi.ErrInvalidSpec
		}
		requested.Extensions = append(requested.Extensions, "fma")
	}
	if options.PPC64CPU != 0 {
		if arch != "ppc64" && arch != "ppc64le" {
			return abi.CPUProfile{}, abi.ErrInvalidSpec
		}
		level, err := options.PPC64CPU.Resolve()
		if err != nil {
			return abi.CPUProfile{}, err
		}
		if requested.Level != "" && !strings.EqualFold(requested.Level, level.String()) {
			return abi.CPUProfile{}, abi.ErrInvalidSpec
		}
		requested.Level = level.String()
	}
	if requested.Level == "" && len(requested.Extensions) == 0 && options.Architecture == "" && arch != "ppc64" && arch != "ppc64le" {
		return cpuprofile.Host().Resolve(arch)
	}
	return requested.Resolve(arch)
}
func resolvedCPU(options CompileOptions) abi.CPUProfile { p, _ := resolveCPU(options); return p }
