// Package cpuprofile discovers ISA features enabled for this process.
package cpuprofile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"golang.org/x/sys/cpu"
	"runtime"
	"sync"
)

var once sync.Once
var host abi.CPUProfile

// Host returns a detached, conservatively verified native profile. /proc/cpuinfo is never used.
func Host() abi.CPUProfile { once.Do(detect); return host.Clone() }
func detect() {
	switch runtime.GOARCH {
	case "amd64", "386":
		host = detectX86()
	case "arm64":
		p := abi.CPUProfile{}
		if cpu.ARM64.HasATOMICS {
			p.Extensions = append(p.Extensions, "lse")
		}
		if cpu.ARM64.HasSVE {
			p.Extensions = append(p.Extensions, "sve")
		}
		if cpu.ARM64.HasSVE2 {
			p.Extensions = append(p.Extensions, "sve2")
		}
		host, _ = p.Resolve("arm64")
	case "loong64":
		host = detectLoong64()
	case "riscv64":
		p := abi.CPUProfile{}
		if cpu.RISCV64.HasZba {
			p.Extensions = append(p.Extensions, "zba")
		}
		if cpu.RISCV64.HasZbb {
			p.Extensions = append(p.Extensions, "zbb")
		}
		if cpu.RISCV64.HasZbs {
			p.Extensions = append(p.Extensions, "zbs")
		}
		if cpu.RISCV64.HasV {
			p.Extensions = append(p.Extensions, "v")
		}
		host, _ = p.Resolve("riscv64")
	case "ppc64le":
		level, err := ppc64.HostCPU()
		if err == nil {
			host, _ = (abi.CPUProfile{Level: level.String()}).Resolve(runtime.GOARCH)
		}
	}
}
