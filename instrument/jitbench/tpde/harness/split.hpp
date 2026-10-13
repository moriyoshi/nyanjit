// Thin access to TPDE-LLVM's aarch64 compiler that splits compile_and_map().
#pragma once
#include <cstddef>
#include "tpde-llvm/LLVMCompiler.hpp"

namespace llvm { class Module; }

namespace bench {
struct Tpde;
Tpde *tpde_create();
// First half of LLVMCompilerBase::compile_and_map: reset() (if the compiler
// was used before) + compile(mod). Code ends up in TPDE's in-memory ELF
// assembler.
bool tpde_compile(Tpde *, llvm::Module &);
// Size of .text after tpde_compile (the module has one function).
std::size_t tpde_code_bytes(Tpde *);
// Second half: JITMapperImpl::map (mmap, copy, relocate, mprotect,
// __register_frame), exactly as compile_and_map does it.
tpde_llvm::JITMapper tpde_map(Tpde *);
// The unsplit public API call, for cross-checking compile+bind.
tpde_llvm::JITMapper tpde_compile_and_map(Tpde *, llvm::Module &);
// Raw bytes of the executable section (for DUMP).
const unsigned char *tpde_text(Tpde *, std::size_t *size);
} // namespace bench
