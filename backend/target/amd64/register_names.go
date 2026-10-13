package amd64

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
)

// registerNames names registers by ordinal for RegisterName and the
// policy's register sets; "" marks an ordinal that is no amd64 register.
var registerNames = func() (names [256]string) {
	copy(names[:], []string{"RAX", "RCX", "RDX", "RBX", "RSP", "RBP", "RSI", "RDI", "R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15"})
	for n := range uint8(32) {
		names[backend.VectorRegister(n)] = "XMM" + strconv.Itoa(int(n))
	}
	return
}()
