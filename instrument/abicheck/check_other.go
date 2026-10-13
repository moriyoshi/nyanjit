//go:build !linux || !(386 || amd64 || arm64 || ppc64le || riscv64 || loong64)

package abicheck

import "github.com/moriyoshi/nyanjit/backend/execution"

func checkPreserved(*execution.Template, []uintptr) (Result, error) { return Result{}, ErrUnsupported }
