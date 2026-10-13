// tpde_compile and tpde_map reproduce parts of LLVMCompilerBase::compile_and_map
// from TPDE, which is licensed separately:
// SPDX-FileCopyrightText: 2025 Contributors to TPDE <https://tpde.org>
// SPDX-License-Identifier: Apache-2.0 WITH LLVM-exception
//
// Compiles TPDE's aarch64 LLVM compiler in this TU (verbatim #include of the
// pinned source) so its public members compile() / assembler / global_syms
// can be reached; the two helpers reproduce LLVMCompilerBase::compile_and_map
// line for line, just with a timing point between its halves.
#include "arm64/LLVMCompilerArm64.cpp"

#include "JITMapper.hpp"
#include "split.hpp"

#include <llvm/TargetParser/Triple.h>

namespace tpde_llvm {
LLVMCompiler::~LLVMCompiler() = default; // normally in LLVMCompiler.cpp
}

namespace bench {
using tpde_llvm::arm64::LLVMCompilerArm64;

struct Tpde {
  std::unique_ptr<tpde_llvm::LLVMCompiler> owner;
  LLVMCompilerArm64 *c;
};

Tpde *tpde_create() {
  auto *t = new Tpde;
  t->owner = tpde_llvm::arm64::create_compiler(llvm::Triple("aarch64-unknown-linux-gnu"));
  t->c = static_cast<LLVMCompilerArm64 *>(t->owner.get());
  return t;
}

bool tpde_compile(Tpde *t, llvm::Module &mod) {
  auto *c = t->c;
  if (c->adaptor->mod) {
    c->reset();
  }
  return c->compile(mod);
}

static constexpr auto no_symbols = [](std::string_view) -> void * { return nullptr; };

tpde_llvm::JITMapper tpde_map(Tpde *t) {
  auto *c = t->c;
  auto res = std::make_unique<tpde_llvm::JITMapperImpl>(std::move(c->global_syms));
  if (!res->map(c->assembler, no_symbols)) {
    return tpde_llvm::JITMapper{nullptr};
  }
  return tpde_llvm::JITMapper{std::move(res)};
}

tpde_llvm::JITMapper tpde_compile_and_map(Tpde *t, llvm::Module &mod) {
  return t->owner->compile_and_map(mod, no_symbols);
}

std::size_t tpde_code_bytes(Tpde *t) {
  auto &a = t->c->assembler;
  return a.get_section(a.get_default_section(tpde::SectionKind::Text)).size();
}

const unsigned char *tpde_text(Tpde *t, std::size_t *size) {
  auto &a = t->c->assembler;
  auto &sec = a.get_section(a.get_default_section(tpde::SectionKind::Text));
  *size = sec.size();
  return sec.data.data();
}
} // namespace bench
