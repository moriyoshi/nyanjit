//go:build !linux || (!arm64 && !amd64 && !386 && !riscv64 && !ppc64 && !ppc64le)

package compile

func goUnwindFromProgram(physicalProgram) (goUnwindInfo, error) {
	return goUnwindInfo{}, ErrUnsupported
}
