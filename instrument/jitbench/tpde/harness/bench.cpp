// TPDE-LLVM counterpart of ../../sljit/bench.c and ../../nyanjit/main.go.
// Each kernel is built with LLVM's IRBuilder in two styles:
//   alloca: what a simple -O0 frontend emits (every mutable local is an entry
//           block alloca, every use a load, every assignment a store);
//   ssa:    the same function after mem2reg (phis, no memory).
// No LLVM passes run. Per iteration, three phases are timed:
//   build:   new Module (shared LLVMContext) + Function + IR via IRBuilder;
//   compile: TPDE compiler reset() + compile(module) into its ELF assembler;
//   bind:    JITMapperImpl::map: mmap, copy, relocate, mprotect,
//            __register_frame (second half of compile_and_map).
// Teardown (JITMapper dtor: __deregister_frame + munmap; Module delete) is
// untimed, like sljit_free_code in bench.c.
#include <llvm/IR/BasicBlock.h>
#include <llvm/IR/Constants.h>
#include <llvm/IR/DataLayout.h>
#include <llvm/IR/Function.h>
#include <llvm/IR/IRBuilder.h>
#include <llvm/IR/LLVMContext.h>
#include <llvm/IR/Module.h>
#include <llvm/IR/Verifier.h>
#include <llvm/Support/raw_ostream.h>

#include <algorithm>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <ctime>
#include <memory>
#include <string>
#include <vector>

#include "split.hpp"

using namespace llvm;

static double now() {
  timespec t;
  clock_gettime(CLOCK_MONOTONIC, &t);
  return t.tv_sec * 1e9 + t.tv_nsec;
}

static LLVMContext *ctx;
static DataLayout *layout;

struct Env {
  Module *M;
  Function *F;
  IRBuilder<> B;
  Type *I64;
  Type *F64;
  BasicBlock *entry;
  Env(Module *M, Function *F)
      : M(M), F(F), B(M->getContext()), I64(Type::getInt64Ty(M->getContext())),
        F64(Type::getDoubleTy(M->getContext())) {
    entry = BasicBlock::Create(M->getContext(), "", F);
    B.SetInsertPoint(entry);
  }
  BasicBlock *block() { return BasicBlock::Create(M->getContext(), "", F); }
  Value *c(uint64_t v) { return ConstantInt::get(I64, v); }
  Value *fc(double v) { return ConstantFP::get(F64, v); }
  Value *arg(unsigned i) { return F->getArg(i); }
  // -O0 style local: entry-block alloca.
  Value *local(Type *t) {
    IRBuilderBase::InsertPointGuard g(B);
    B.SetInsertPoint(entry, entry->begin());
    return B.CreateAlloca(t);
  }
  Value *ld(Value *p) { return B.CreateLoad(I64, p); }
  Value *ldf(Value *p) { return B.CreateLoad(F64, p); }
  void st(Value *v, Value *p) { B.CreateStore(v, p); }
};

// ---- kernels -------------------------------------------------------------

// uint64_t f(uint64_t a, uint64_t b) { return a + b; }  (no locals: both
// variants are identical)
static void tiny(Env &e, bool) {
  e.B.CreateRet(e.B.CreateAdd(e.arg(0), e.arg(1)));
}

// x = a; repeat n: { x *= 3; x ^= x >> 5; x += i; } return x;
static int chain_n;
static void chain(Env &e, bool ssa) {
  auto &B = e.B;
  if (ssa) {
    Value *x = e.arg(0);
    for (int i = 0; i < chain_n; i++) {
      x = B.CreateMul(x, e.c(3));
      x = B.CreateXor(x, B.CreateLShr(x, e.c(5)));
      x = B.CreateAdd(x, e.c(i));
    }
    B.CreateRet(x);
    return;
  }
  Value *px = e.local(e.I64);
  e.st(e.arg(0), px);
  for (int i = 0; i < chain_n; i++) {
    e.st(B.CreateMul(e.ld(px), e.c(3)), px);
    Value *r = B.CreateLShr(e.ld(px), e.c(5));
    e.st(B.CreateXor(e.ld(px), r), px);
    e.st(B.CreateAdd(e.ld(px), e.c(i)), px);
  }
  B.CreateRet(e.ld(px));
}

// acc = 0; i = 0; while (i < n) { acc += (i * i) ^ (i >> 3); i += 1; }
static void loop(Env &e, bool ssa) {
  auto &B = e.B;
  Value *n = e.arg(0);
  BasicBlock *cond = e.block(), *body = e.block(), *end = e.block();
  if (ssa) {
    B.CreateBr(cond);
    B.SetInsertPoint(cond);
    PHINode *i = B.CreatePHI(e.I64, 2), *acc = B.CreatePHI(e.I64, 2);
    B.CreateCondBr(B.CreateICmpUGE(i, n), end, body);
    B.SetInsertPoint(body);
    Value *t = B.CreateXor(B.CreateMul(i, i), B.CreateLShr(i, e.c(3)));
    Value *acc2 = B.CreateAdd(acc, t);
    Value *i2 = B.CreateAdd(i, e.c(1));
    B.CreateBr(cond);
    i->addIncoming(e.c(0), e.entry);
    i->addIncoming(i2, body);
    acc->addIncoming(e.c(0), e.entry);
    acc->addIncoming(acc2, body);
    B.SetInsertPoint(end);
    B.CreateRet(acc);
    return;
  }
  Value *acc = e.local(e.I64), *i = e.local(e.I64);
  e.st(e.c(0), acc);
  e.st(e.c(0), i);
  B.CreateBr(cond);
  B.SetInsertPoint(cond);
  B.CreateCondBr(B.CreateICmpUGE(e.ld(i), n), end, body);
  B.SetInsertPoint(body);
  Value *m = B.CreateMul(e.ld(i), e.ld(i));
  Value *t = B.CreateXor(m, B.CreateLShr(e.ld(i), e.c(3)));
  e.st(B.CreateAdd(e.ld(acc), t), acc);
  e.st(B.CreateAdd(e.ld(i), e.c(1)), i);
  B.CreateBr(cond);
  B.SetInsertPoint(end);
  B.CreateRet(e.ld(acc));
}

// a[k] = k + 1; i = 0;
// while (i < n) { for k in 0..15: a[k] += a[(k + 1) % 16] ^ i; i += 1; }
// return a[0] ^ a[1] ^ ... ^ a[15];   (a[k] are 16 scalar locals)
enum { VARS = 16 };
static void pressure(Env &e, bool ssa) {
  auto &B = e.B;
  Value *n = e.arg(0);
  BasicBlock *cond = e.block(), *body = e.block(), *end = e.block();
  if (ssa) {
    B.CreateBr(cond);
    B.SetInsertPoint(cond);
    PHINode *a[VARS];
    for (int k = 0; k < VARS; k++)
      a[k] = B.CreatePHI(e.I64, 2);
    PHINode *i = B.CreatePHI(e.I64, 2);
    B.CreateCondBr(B.CreateICmpUGE(i, n), end, body);
    B.SetInsertPoint(body);
    Value *na[VARS];
    for (int k = 0; k < VARS; k++) {
      Value *next = k + 1 < VARS ? (Value *)a[k + 1] : na[0];
      na[k] = B.CreateAdd(a[k], B.CreateXor(next, i));
    }
    Value *i2 = B.CreateAdd(i, e.c(1));
    B.CreateBr(cond);
    for (int k = 0; k < VARS; k++) {
      a[k]->addIncoming(e.c(k + 1), e.entry);
      a[k]->addIncoming(na[k], body);
    }
    i->addIncoming(e.c(0), e.entry);
    i->addIncoming(i2, body);
    B.SetInsertPoint(end);
    Value *x = a[0];
    for (int k = 1; k < VARS; k++)
      x = B.CreateXor(x, a[k]);
    B.CreateRet(x);
    return;
  }
  Value *i = e.local(e.I64);
  Value *a[VARS];
  for (int k = 0; k < VARS; k++)
    a[k] = e.local(e.I64);
  for (int k = 0; k < VARS; k++)
    e.st(e.c(k + 1), a[k]);
  e.st(e.c(0), i);
  B.CreateBr(cond);
  B.SetInsertPoint(cond);
  B.CreateCondBr(B.CreateICmpUGE(e.ld(i), n), end, body);
  B.SetInsertPoint(body);
  for (int k = 0; k < VARS; k++) {
    Value *r = B.CreateXor(e.ld(a[(k + 1) % VARS]), e.ld(i));
    e.st(B.CreateAdd(e.ld(a[k]), r), a[k]);
  }
  e.st(B.CreateAdd(e.ld(i), e.c(1)), i);
  B.CreateBr(cond);
  B.SetInsertPoint(end);
  Value *x = e.ld(a[0]);
  for (int k = 1; k < VARS; k++)
    x = B.CreateXor(x, e.ld(a[k]));
  B.CreateRet(x);
}

// x = 1; total = 0;
// while (x <= n) {
//   y = x;
//   while (y != 1) {
//     if (y & 1) { y = y * 3 + 1; total += 1; } else { y >>= 1; total += 1; }
//   }
//   x += 1;
// }
// return total;
// Both arms jump straight back to the inner test, as in bench.c.
static void collatz(Env &e, bool ssa) {
  auto &B = e.B;
  Value *n = e.arg(0);
  BasicBlock *outer = e.block(), *obody = e.block(), *inner = e.block(),
             *ibody = e.block(), *odd = e.block(), *even = e.block(),
             *next = e.block(), *done = e.block();
  if (ssa) {
    B.CreateBr(outer);
    B.SetInsertPoint(outer);
    PHINode *x = B.CreatePHI(e.I64, 2), *tot = B.CreatePHI(e.I64, 2);
    B.CreateCondBr(B.CreateICmpUGT(x, n), done, obody);
    B.SetInsertPoint(obody);
    B.CreateBr(inner);
    B.SetInsertPoint(inner);
    PHINode *y = B.CreatePHI(e.I64, 3), *tin = B.CreatePHI(e.I64, 3);
    B.CreateCondBr(B.CreateICmpEQ(y, e.c(1)), next, ibody);
    B.SetInsertPoint(ibody);
    B.CreateCondBr(B.CreateICmpEQ(B.CreateAnd(y, e.c(1)), e.c(0)), even, odd);
    B.SetInsertPoint(odd);
    Value *y3 = B.CreateAdd(B.CreateMul(y, e.c(3)), e.c(1));
    Value *t1 = B.CreateAdd(tin, e.c(1));
    B.CreateBr(inner);
    B.SetInsertPoint(even);
    Value *yh = B.CreateLShr(y, e.c(1));
    Value *t2 = B.CreateAdd(tin, e.c(1));
    B.CreateBr(inner);
    B.SetInsertPoint(next);
    Value *x2 = B.CreateAdd(x, e.c(1));
    B.CreateBr(outer);
    x->addIncoming(e.c(1), e.entry);
    x->addIncoming(x2, next);
    tot->addIncoming(e.c(0), e.entry);
    tot->addIncoming(tin, next);
    y->addIncoming(x, obody);
    y->addIncoming(y3, odd);
    y->addIncoming(yh, even);
    tin->addIncoming(tot, obody);
    tin->addIncoming(t1, odd);
    tin->addIncoming(t2, even);
    B.SetInsertPoint(done);
    B.CreateRet(tot);
    return;
  }
  Value *x = e.local(e.I64), *y = e.local(e.I64), *tot = e.local(e.I64);
  e.st(e.c(1), x);
  e.st(e.c(0), tot);
  B.CreateBr(outer);
  B.SetInsertPoint(outer);
  B.CreateCondBr(B.CreateICmpUGT(e.ld(x), n), done, obody);
  B.SetInsertPoint(obody);
  e.st(e.ld(x), y);
  B.CreateBr(inner);
  B.SetInsertPoint(inner);
  B.CreateCondBr(B.CreateICmpEQ(e.ld(y), e.c(1)), next, ibody);
  B.SetInsertPoint(ibody);
  B.CreateCondBr(B.CreateICmpEQ(B.CreateAnd(e.ld(y), e.c(1)), e.c(0)), even, odd);
  B.SetInsertPoint(odd);
  e.st(B.CreateAdd(B.CreateMul(e.ld(y), e.c(3)), e.c(1)), y);
  e.st(B.CreateAdd(e.ld(tot), e.c(1)), tot);
  B.CreateBr(inner);
  B.SetInsertPoint(even);
  e.st(B.CreateLShr(e.ld(y), e.c(1)), y);
  e.st(B.CreateAdd(e.ld(tot), e.c(1)), tot);
  B.CreateBr(inner);
  B.SetInsertPoint(next);
  e.st(B.CreateAdd(e.ld(x), e.c(1)), x);
  B.CreateBr(outer);
  B.SetInsertPoint(done);
  B.CreateRet(e.ld(tot));
}

// count = 0; i = 0;
// while (i < n) {
//   double x = (double)(int64_t)i * 0.001;
//   double y = c[0];
//   for k in 1..8: y = y * x + c[k];        (separate fmul/fadd, no fusion)
//   if (!(y <= 0.5 || isnan(y))) count += 1;  (fcmp ule -> miss)
//   i += 1;
// }
// return count;
static const double coefficients[] = {0.1, -0.2, 0.3, -0.4, 0.5, -0.6, 0.7, -0.8, 0.9};
enum { NCOEF = sizeof coefficients / sizeof coefficients[0] };
static void fpk(Env &e, bool ssa) {
  auto &B = e.B;
  Value *n = e.arg(0);
  BasicBlock *cond = e.block(), *body = e.block(), *hit = e.block(),
             *miss = e.block(), *end = e.block();
  if (ssa) {
    B.CreateBr(cond);
    B.SetInsertPoint(cond);
    PHINode *i = B.CreatePHI(e.I64, 2), *cnt = B.CreatePHI(e.I64, 2);
    B.CreateCondBr(B.CreateICmpUGE(i, n), end, body);
    B.SetInsertPoint(body);
    Value *x = B.CreateFMul(B.CreateSIToFP(i, e.F64), e.fc(0.001));
    Value *y = e.fc(coefficients[0]);
    for (int k = 1; k < NCOEF; k++)
      y = B.CreateFAdd(B.CreateFMul(y, x), e.fc(coefficients[k]));
    B.CreateCondBr(B.CreateFCmpULE(y, e.fc(0.5)), miss, hit);
    B.SetInsertPoint(hit);
    Value *cnt1 = B.CreateAdd(cnt, e.c(1));
    B.CreateBr(miss);
    B.SetInsertPoint(miss);
    PHINode *cm = B.CreatePHI(e.I64, 2);
    cm->addIncoming(cnt, body);
    cm->addIncoming(cnt1, hit);
    Value *i2 = B.CreateAdd(i, e.c(1));
    B.CreateBr(cond);
    i->addIncoming(e.c(0), e.entry);
    i->addIncoming(i2, miss);
    cnt->addIncoming(e.c(0), e.entry);
    cnt->addIncoming(cm, miss);
    B.SetInsertPoint(end);
    B.CreateRet(cnt);
    return;
  }
  Value *cnt = e.local(e.I64), *i = e.local(e.I64);
  Value *x = e.local(e.F64), *y = e.local(e.F64);
  e.st(e.c(0), cnt);
  e.st(e.c(0), i);
  B.CreateBr(cond);
  B.SetInsertPoint(cond);
  B.CreateCondBr(B.CreateICmpUGE(e.ld(i), n), end, body);
  B.SetInsertPoint(body);
  e.st(B.CreateFMul(B.CreateSIToFP(e.ld(i), e.F64), e.fc(0.001)), x);
  e.st(e.fc(coefficients[0]), y);
  for (int k = 1; k < NCOEF; k++)
    e.st(B.CreateFAdd(B.CreateFMul(e.ldf(y), e.ldf(x)), e.fc(coefficients[k])), y);
  B.CreateCondBr(B.CreateFCmpULE(e.ldf(y), e.fc(0.5)), miss, hit);
  B.SetInsertPoint(hit);
  e.st(B.CreateAdd(e.ld(cnt), e.c(1)), cnt);
  B.CreateBr(miss);
  B.SetInsertPoint(miss);
  e.st(B.CreateAdd(e.ld(i), e.c(1)), i);
  B.CreateBr(cond);
  B.SetInsertPoint(end);
  B.CreateRet(e.ld(cnt));
}

static void chain100(Env &e, bool ssa) { chain_n = 100; chain(e, ssa); }
static void chain1000(Env &e, bool ssa) { chain_n = 1000; chain(e, ssa); }

struct Kernel {
  const char *name;
  int ops;
  void (*build)(Env &, bool);
  int args;
  uint64_t a, b;
};

static const Kernel kernels[] = {
    {"tiny", 1, tiny, 2, 3, 4},
    {"chain100", 400, chain100, 1, 12345, 0},
    {"chain1000", 4000, chain1000, 1, 12345, 0},
    {"loop", 6, loop, 1, 10000000, 0},
    {"pressure", 2 * VARS + 3, pressure, 1, 1000000, 0},
    {"collatz", 12, collatz, 1, 100000, 0},
    {"fp", 2 * NCOEF + 6, fpk, 1, 1000000, 0},
};

// ---- harness -------------------------------------------------------------

static Function *build(const Kernel &k, bool ssa, std::unique_ptr<Module> &M) {
  M = std::make_unique<Module>("k", *ctx);
  M->setDataLayout(*layout);
  M->setTargetTriple("aarch64-unknown-linux-gnu");
  Type *I64 = Type::getInt64Ty(*ctx);
  Type *params[2] = {I64, I64};
  FunctionType *ft = FunctionType::get(I64, ArrayRef<Type *>(params, k.args), false);
  Function *F = Function::Create(ft, GlobalValue::ExternalLinkage, "f", M.get());
  Env e(M.get(), F);
  k.build(e, ssa);
  return F;
}

typedef uint64_t (*fn1)(uint64_t);
typedef uint64_t (*fn2)(uint64_t, uint64_t);

static long rss_kb() {
  FILE *f = fopen("/proc/self/statm", "r");
  long pages = 0, res = 0;
  if (f) {
    if (fscanf(f, "%ld %ld", &pages, &res) != 2)
      res = 0;
    fclose(f);
  }
  return res * 4;
}

int main(int argc, char **argv) {
  const char *only = nullptr, *variant = "alloca", *dump = nullptr;
  double budget = 300e6;
  int rounds = 7;
  bool combined = false;
  for (int i = 1; i < argc; i++) {
    if (!strcmp(argv[i], "-k") && i + 1 < argc) only = argv[++i];
    else if (!strcmp(argv[i], "-v") && i + 1 < argc) variant = argv[++i];
    else if (!strcmp(argv[i], "-t") && i + 1 < argc) budget = atof(argv[++i]) * 1e6;
    else if (!strcmp(argv[i], "-dump") && i + 1 < argc) dump = argv[++i];
    else if (!strcmp(argv[i], "-combined")) combined = true;
    else if (!strcmp(argv[i], "-header")) {
      printf("%-10s %-7s %10s %10s %10s %10s %9s %6s %12s %s\n", "kernel", "variant", "build",
             "compile", "bind", "total", "ns/op", "bytes", "run", "result");
      return 0;
    } else {
      fprintf(stderr, "usage: %s [-header] [-k kernel] [-v alloca|ssa] [-t ms] [-combined] [-dump file]\n", argv[0]);
      return 2;
    }
  }
  bool ssa = !strcmp(variant, "ssa");
  if (!ssa && strcmp(variant, "alloca")) {
    fprintf(stderr, "bad variant %s\n", variant);
    return 2;
  }
  ctx = new LLVMContext;
  layout = new DataLayout("e-m:e-p270:32:32-p271:32:32-p272:64:64-i8:8:32-i16:16:32-i64:64-i128:128-n32:64-S128-Fn32");
  bench::Tpde *tp = bench::tpde_create();

  for (const Kernel &k : kernels) {
    if (only && strcmp(only, k.name))
      continue;
    {
      std::unique_ptr<Module> M;
      Function *F = build(k, ssa, M);
      if (verifyFunction(*F, &errs())) {
        fprintf(stderr, "%s/%s: invalid IR\n", k.name, variant);
        return 1;
      }
    }
    double tb[16], tc[16], tm[16], tt[16], tcm[16], tfree[16];
    size_t bytes = 0;
    long rss0 = 0;
    long iters = 0;
    for (int r = 0; r < rounds; r++) {
      double sb = 0, sc = 0, sm = 0, sfree = 0;
      long n = 0;
      for (double start = now(); now() - start < budget || n == 0; n++) {
        std::unique_ptr<Module> M;
        double t0 = now();
        Function *F = build(k, ssa, M);
        double t1 = now();
        if (!bench::tpde_compile(tp, *M)) {
          fprintf(stderr, "%s/%s: TPDE compile failed\n", k.name, variant);
          return 1;
        }
        double t2 = now();
        bytes = bench::tpde_code_bytes(tp);
        double t2b = now();
        tpde_llvm::JITMapper map = bench::tpde_map(tp);
        double t3 = now();
        if (!map || !map.lookup_global(F)) {
          fprintf(stderr, "%s/%s: TPDE map failed\n", k.name, variant);
          return 1;
        }
        { tpde_llvm::JITMapper dead = std::move(map); }
        M.reset();
        double t4 = now();
        sb += t1 - t0;
        sc += t2 - t1;
        sm += t3 - t2b;
        sfree += t4 - t3;
      }
      iters += n;
      tb[r] = sb / n;
      tc[r] = sc / n;
      tm[r] = sm / n;
      tt[r] = (sb + sc + sm) / n;
      tfree[r] = sfree / n;
      if (r == 0)
        rss0 = rss_kb();
    }
    long rss1 = rss_kb();
    if (combined) {
      // Unsplit public API: tpde_llvm::LLVMCompiler::compile_and_map.
      for (int r = 0; r < rounds; r++) {
        double s = 0;
        long n = 0;
        for (double start = now(); now() - start < budget || n == 0; n++) {
          std::unique_ptr<Module> M;
          build(k, ssa, M);
          double t0 = now();
          tpde_llvm::JITMapper map = bench::tpde_compile_and_map(tp, *M);
          s += now() - t0;
          if (!map) return 1;
        }
        tcm[r] = s / n;
      }
      std::sort(tcm, tcm + rounds);
    }
    std::sort(tb, tb + rounds);
    std::sort(tc, tc + rounds);
    std::sort(tm, tm + rounds);
    std::sort(tt, tt + rounds);
    std::sort(tfree, tfree + rounds);

    std::unique_ptr<Module> M;
    Function *F = build(k, ssa, M);
    if (!bench::tpde_compile(tp, *M))
      return 1;
    if (dump) {
      size_t sz;
      const unsigned char *text = bench::tpde_text(tp, &sz);
      FILE *f = fopen(dump, "wb");
      fwrite(text, 1, sz, f);
      fclose(f);
    }
    tpde_llvm::JITMapper map = bench::tpde_map(tp);
    void *code = map.lookup_global(F);
    double best = 1e30;
    uint64_t result = 0;
    for (int r = 0; r < rounds; r++) {
      double t0 = now();
      result = k.args == 2 ? ((fn2)code)(k.a, k.b) : ((fn1)code)(k.a);
      double t = now() - t0;
      best = std::min(best, t);
    }
    int m = rounds / 2;
    printf("%-10s %-7s %8.2fus %8.2fus %8.2fus %8.2fus %9.1f %6zu %10.3fus %llu\n", k.name, variant,
           tb[m] / 1e3, tc[m] / 1e3, tm[m] / 1e3, tt[m] / 1e3, tt[m] / k.ops, bytes, best / 1e3,
           (unsigned long long)result);
    fprintf(stderr, "# %s/%s: iters=%ld teardown=%.2fus rss_after_round1=%ldkB rss_end=%ldkB", k.name,
            variant, iters, tfree[m] / 1e3, rss0, rss1);
    if (combined)
      fprintf(stderr, " compile_and_map=%.2fus (split compile+bind=%.2fus)", tcm[m] / 1e3,
              (tc[m] + tm[m]) / 1e3);
    fprintf(stderr, "\n");
    fflush(stdout);
  }
  return 0;
}
