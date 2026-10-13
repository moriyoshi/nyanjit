package i386

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/backend"
)

// registerNames names registers by ordinal for RegisterName and the
// policy's register sets; "" marks an ordinal that is no 386 register.
var registerNames = func() (names [256]string) {
	copy(names[:], []string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI"})
	for n := range uint8(32) {
		names[backend.VectorRegister(n)] = "XMM" + strconv.Itoa(int(n))
	}
	return
}()
