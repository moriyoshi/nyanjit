//go:build !linux

package irconform

import "github.com/moriyoshi/nyanjit/instrument/irinterp"

// syscallCases has Linux system calls only.
func syscallCases() []Case { return nil }

func hostSyscall(irinterp.Call) (uint64, error) { return 0, irinterp.ErrUnsupported }
