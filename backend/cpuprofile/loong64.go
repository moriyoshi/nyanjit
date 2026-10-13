package cpuprofile

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"os"
)

// Linux LoongArch UAPI asm/hwcap.h: LSX=1<<4, LASX=1<<5. Discovery failure
// leaves baseline only. Auxv is process-local; /proc/cpuinfo is not consulted.
func detectLoong64() abi.CPUProfile {
	p := abi.CPUProfile{}
	if data, err := os.ReadFile("/proc/self/auxv"); err == nil {
		for off := 0; off+16 <= len(data); off += 16 {
			if binary.LittleEndian.Uint64(data[off:]) == 16 {
				p = loong64HWCAP(binary.LittleEndian.Uint64(data[off+8:]))
				break
			}
		}
	}
	p, _ = p.Resolve("loong64")
	return p
}
func loong64HWCAP(v uint64) abi.CPUProfile {
	p := abi.CPUProfile{}
	if v&(1<<4) != 0 {
		p.Extensions = append(p.Extensions, "lsx")
	}
	if v&(1<<5) != 0 {
		p.Extensions = append(p.Extensions, "lasx")
	}
	return p
}
