package abi

// PPC64CPU selects the minimum POWER instruction set for generated code.
type PPC64CPU uint8

const (
	// PPC64CPUUnspecified selects the portable POWER8 baseline.
	PPC64CPUUnspecified PPC64CPU = 0
	PPC64Power8         PPC64CPU = 8
	PPC64Power9         PPC64CPU = 9
	PPC64Power10        PPC64CPU = 10
)

// Resolve validates the requested level and defaults to POWER8.
func (level PPC64CPU) Resolve() (PPC64CPU, error) {
	switch level {
	case PPC64CPUUnspecified:
		return PPC64Power8, nil
	case PPC64Power8, PPC64Power9, PPC64Power10:
		return level, nil
	default:
		return 0, ErrInvalidSpec
	}
}

// String returns the Go/QEMU name of a supported CPU level.
func (level PPC64CPU) String() string {
	switch level {
	case PPC64CPUUnspecified:
		return "default"
	case PPC64Power8:
		return "power8"
	case PPC64Power9:
		return "power9"
	case PPC64Power10:
		return "power10"
	default:
		return "invalid"
	}
}
