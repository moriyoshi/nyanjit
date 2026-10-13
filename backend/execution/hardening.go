package execution

import (
	"encoding/binary"
	"io"
	"runtime"
)

// mappingBody returns the complete allocation image and the body offset. Only
// the body is relocated/registered; Code retains the full image for Unmap.
// Sixteen-byte slots preserve instruction and literal alignment on every target.
func mappingBody(code []byte, harden, registered bool, random io.Reader) ([]byte, int, error) {
	if len(code) == 0 {
		return nil, 0, ErrEmptyCode
	}
	offset := 0
	if harden {
		var b [1]byte
		if _, err := io.ReadFull(random, b[:]); err != nil {
			return nil, 0, err
		}
		offset = (int(b[0]) + 1) * 16
	}
	extra := 0
	if registered {
		extra = 1
	} // Keep inclusive runtime text endpoints disjoint.
	if len(code) > int(^uint(0)>>1)-offset-extra {
		return nil, 0, ErrInvalidCodeOptions
	}
	if offset == 0 && extra == 0 {
		return code, 0, nil
	}
	image := make([]byte, offset+len(code)+extra)
	if harden {
		trap := []byte{0xcc} // INT3 on either x86 target.
		switch runtime.GOARCH {
		case "arm64":
			trap = []byte{0, 0, 0x20, 0xd4} // BRK #0
		case "riscv64":
			trap = []byte{0x73, 0, 0x10, 0} // EBREAK
		case "loong64":
			trap = []byte{0, 0, 0x2a, 0} // BREAK 0
		case "ppc64le":
			trap = make([]byte, 4)
			binary.LittleEndian.PutUint32(trap, 0x7fe00008) // TRAP
		}
		for n := 0; n < offset; n++ {
			image[n] = trap[n%len(trap)]
		}
	}
	copy(image[offset:], code)
	return image, offset, nil
}
