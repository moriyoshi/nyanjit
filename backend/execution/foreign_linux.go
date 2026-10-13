//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package execution

import (
	"github.com/ebitengine/purego"
)

func foreignSupported() bool { return true }

func registerForeignCallback(fn any) (address uintptr, err error) {
	defer func() {
		if recover() != nil {
			address, err = 0, ErrInvalidCallback
		}
	}()
	return purego.NewCallback(fn), nil
}
