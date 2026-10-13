/* Kernels matched one for one with ../nyanjit/main.go, built with sljit.
   Each compile creates a compiler, emits, generates code (sljit's executable
   allocator) and frees it, as one end-to-end JIT compile. */
#define SLJIT_CONFIG_AUTO 1
#include "sljitLir.c"
#include <stdio.h>
#include <string.h>
#include <time.h>

typedef sljit_uw (*fn1)(sljit_uw);
typedef sljit_uw (*fn2)(sljit_uw, sljit_uw);

static double now(void) {
	struct timespec t;
	clock_gettime(CLOCK_MONOTONIC, &t);
	return t.tv_sec * 1e9 + t.tv_nsec;
}

static void tiny(struct sljit_compiler *c) {
	sljit_emit_enter(c, 0, SLJIT_ARGS2(W, W, W), 1, 2, 0);
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_R0, 0, SLJIT_S0, 0, SLJIT_S1, 0);
	sljit_emit_return(c, SLJIT_MOV, SLJIT_R0, 0);
}

static int chain_n;
static void chain(struct sljit_compiler *c) {
	sljit_emit_enter(c, 0, SLJIT_ARGS1(W, W), 2, 1, 0);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_R0, 0, SLJIT_S0, 0);
	for (int i = 0; i < chain_n; i++) {
		sljit_emit_op2(c, SLJIT_MUL, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_IMM, 3);
		sljit_emit_op2(c, SLJIT_LSHR, SLJIT_R1, 0, SLJIT_R0, 0, SLJIT_IMM, 5);
		sljit_emit_op2(c, SLJIT_XOR, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_R1, 0);
		sljit_emit_op2(c, SLJIT_ADD, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_IMM, i);
	}
	sljit_emit_return(c, SLJIT_MOV, SLJIT_R0, 0);
}
static void chain100(struct sljit_compiler *c) { chain_n = 100; chain(c); }
static void chain1000(struct sljit_compiler *c) { chain_n = 1000; chain(c); }

/* i in R1, acc in R0, n in S0 */
static void loop(struct sljit_compiler *c) {
	sljit_emit_enter(c, 0, SLJIT_ARGS1(W, W), 4, 1, 0);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_R0, 0, SLJIT_IMM, 0);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_R1, 0, SLJIT_IMM, 0);
	struct sljit_label *top = sljit_emit_label(c);
	struct sljit_jump *exit = sljit_emit_cmp(c, SLJIT_GREATER_EQUAL, SLJIT_R1, 0, SLJIT_S0, 0);
	sljit_emit_op2(c, SLJIT_MUL, SLJIT_R2, 0, SLJIT_R1, 0, SLJIT_R1, 0);
	sljit_emit_op2(c, SLJIT_LSHR, SLJIT_R3, 0, SLJIT_R1, 0, SLJIT_IMM, 3);
	sljit_emit_op2(c, SLJIT_XOR, SLJIT_R2, 0, SLJIT_R2, 0, SLJIT_R3, 0);
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_R2, 0);
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_R1, 0, SLJIT_R1, 0, SLJIT_IMM, 1);
	sljit_set_label(sljit_emit_jump(c, SLJIT_JUMP), top);
	sljit_set_label(exit, sljit_emit_label(c));
	sljit_emit_return(c, SLJIT_MOV, SLJIT_R0, 0);
}

#define VARS 16
/* a_k in R0..R15, i in R16, tmp R17, n in S0 */
static void pressure(struct sljit_compiler *c) {
	sljit_emit_enter(c, 0, SLJIT_ARGS1(W, W), VARS + 2, 1, 0);
	for (int k = 0; k < VARS; k++)
		sljit_emit_op1(c, SLJIT_MOV, SLJIT_R(k), 0, SLJIT_IMM, k + 1);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_R(VARS), 0, SLJIT_IMM, 0);
	struct sljit_label *top = sljit_emit_label(c);
	struct sljit_jump *exit = sljit_emit_cmp(c, SLJIT_GREATER_EQUAL, SLJIT_R(VARS), 0, SLJIT_S0, 0);
	/* a_k += a_{k+1} ^ i, in order, with a_15 reading the new a_0 as the
	   nyanjit kernel's sequential updates do... except that kernel reads the
	   updated a[(k+1)%16] only for k = 15. */
	for (int k = 0; k < VARS; k++) {
		sljit_emit_op2(c, SLJIT_XOR, SLJIT_R(VARS + 1), 0, SLJIT_R((k + 1) % VARS), 0, SLJIT_R(VARS), 0);
		sljit_emit_op2(c, SLJIT_ADD, SLJIT_R(k), 0, SLJIT_R(k), 0, SLJIT_R(VARS + 1), 0);
	}
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_R(VARS), 0, SLJIT_R(VARS), 0, SLJIT_IMM, 1);
	sljit_set_label(sljit_emit_jump(c, SLJIT_JUMP), top);
	sljit_set_label(exit, sljit_emit_label(c));
	for (int k = 1; k < VARS; k++)
		sljit_emit_op2(c, SLJIT_XOR, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_R(k), 0);
	sljit_emit_return(c, SLJIT_MOV, SLJIT_R0, 0);
}

/* x in S1, total in S2, y in R0, n in S0 */
static void collatz(struct sljit_compiler *c) {
	sljit_emit_enter(c, 0, SLJIT_ARGS1(W, W), 2, 3, 0);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_S1, 0, SLJIT_IMM, 1);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_S2, 0, SLJIT_IMM, 0);
	struct sljit_label *outer = sljit_emit_label(c);
	struct sljit_jump *done = sljit_emit_cmp(c, SLJIT_GREATER, SLJIT_S1, 0, SLJIT_S0, 0);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_R0, 0, SLJIT_S1, 0);
	struct sljit_label *inner = sljit_emit_label(c);
	struct sljit_jump *one = sljit_emit_cmp(c, SLJIT_EQUAL, SLJIT_R0, 0, SLJIT_IMM, 1);
	sljit_emit_op2u(c, SLJIT_AND | SLJIT_SET_Z, SLJIT_R0, 0, SLJIT_IMM, 1);
	struct sljit_jump *even = sljit_emit_jump(c, SLJIT_ZERO);
	sljit_emit_op2(c, SLJIT_MUL, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_IMM, 3);
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_IMM, 1);
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_S2, 0, SLJIT_S2, 0, SLJIT_IMM, 1);
	sljit_set_label(sljit_emit_jump(c, SLJIT_JUMP), inner);
	sljit_set_label(even, sljit_emit_label(c));
	sljit_emit_op2(c, SLJIT_LSHR, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_IMM, 1);
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_S2, 0, SLJIT_S2, 0, SLJIT_IMM, 1);
	sljit_set_label(sljit_emit_jump(c, SLJIT_JUMP), inner);
	sljit_set_label(one, sljit_emit_label(c));
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_S1, 0, SLJIT_S1, 0, SLJIT_IMM, 1);
	sljit_set_label(sljit_emit_jump(c, SLJIT_JUMP), outer);
	sljit_set_label(done, sljit_emit_label(c));
	sljit_emit_return(c, SLJIT_MOV, SLJIT_S2, 0);
}

static const double coefficients[] = {0.1, -0.2, 0.3, -0.4, 0.5, -0.6, 0.7, -0.8, 0.9};
#define NCOEF (sizeof coefficients / sizeof coefficients[0])
/* i in R1, count in R0, x in FR0, y in FR1; constants hoisted into
   FR2 (0.001), FR3 (0.5) and FR4.. (coefficients), as an sljit user would. */
static void fp(struct sljit_compiler *c) {
	sljit_emit_enter(c, 0, SLJIT_ARGS1(W, W), 2 | SLJIT_ENTER_FLOAT(4 + NCOEF), 1, 0);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_R0, 0, SLJIT_IMM, 0);
	sljit_emit_op1(c, SLJIT_MOV, SLJIT_R1, 0, SLJIT_IMM, 0);
	sljit_emit_fset64(c, SLJIT_FR2, 0.001);
	sljit_emit_fset64(c, SLJIT_FR3, 0.5);
	for (unsigned k = 0; k < NCOEF; k++)
		sljit_emit_fset64(c, SLJIT_FR(4 + k), coefficients[k]);
	struct sljit_label *top = sljit_emit_label(c);
	struct sljit_jump *exit = sljit_emit_cmp(c, SLJIT_GREATER_EQUAL, SLJIT_R1, 0, SLJIT_S0, 0);
	sljit_emit_fop1(c, SLJIT_CONV_F64_FROM_SW, SLJIT_FR0, 0, SLJIT_R1, 0);
	sljit_emit_fop2(c, SLJIT_MUL_F64, SLJIT_FR0, 0, SLJIT_FR0, 0, SLJIT_FR2, 0);
	sljit_emit_fop1(c, SLJIT_MOV_F64, SLJIT_FR1, 0, SLJIT_FR4, 0);
	for (unsigned k = 1; k < NCOEF; k++) {
		sljit_emit_fop2(c, SLJIT_MUL_F64, SLJIT_FR1, 0, SLJIT_FR1, 0, SLJIT_FR0, 0);
		sljit_emit_fop2(c, SLJIT_ADD_F64, SLJIT_FR1, 0, SLJIT_FR1, 0, SLJIT_FR(4 + k), 0);
	}
	struct sljit_jump *miss = sljit_emit_fcmp(c, SLJIT_UNORDERED_OR_LESS_EQUAL, SLJIT_FR1, 0, SLJIT_FR3, 0);
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_R0, 0, SLJIT_R0, 0, SLJIT_IMM, 1);
	sljit_set_label(miss, sljit_emit_label(c));
	sljit_emit_op2(c, SLJIT_ADD, SLJIT_R1, 0, SLJIT_R1, 0, SLJIT_IMM, 1);
	sljit_set_label(sljit_emit_jump(c, SLJIT_JUMP), top);
	sljit_set_label(exit, sljit_emit_label(c));
	sljit_emit_return(c, SLJIT_MOV, SLJIT_R0, 0);
}

struct kernel {
	const char *name;
	int ops;
	void (*emit)(struct sljit_compiler *);
	int args;
	sljit_uw a, b;
};

static const struct kernel kernels[] = {
	{"tiny", 1, tiny, 2, 3, 4},
	{"chain100", 400, chain100, 1, 12345, 0},
	{"chain1000", 4000, chain1000, 1, 12345, 0},
	{"loop", 6, loop, 1, 10000000, 0},
	{"pressure", 2 * VARS + 3, pressure, 1, 1000000, 0},
	{"collatz", 12, collatz, 1, 100000, 0},
	{"fp", 2 * NCOEF + 6, fp, 1, 1000000, 0},
};

static int cmpd(const void *x, const void *y) {
	double a = *(const double *)x, b = *(const double *)y;
	return a < b ? -1 : a > b;
}

int main(int argc, char **argv) {
	if (getenv("DUMP")) { for (unsigned k = 0; k < sizeof kernels / sizeof kernels[0]; k++) if (!strcmp(kernels[k].name, getenv("DUMP"))) { struct sljit_compiler *c = sljit_create_compiler(NULL); kernels[k].emit(c); void *code = sljit_generate_code(c, 0, NULL); fwrite(code, 1, sljit_get_generated_code_size(c), stdout); return 0; } return 1; }
	double budget = 300e6;
	int rounds = 7;
	if (argc > 1)
		budget = atof(argv[1]) * 1e6;
	printf("%-10s %10s %10s %9s %8s %12s %s\n", "kernel", "emit", "total", "ns/op", "bytes", "run", "result");
	for (unsigned k = 0; k < sizeof kernels / sizeof kernels[0]; k++) {
		const struct kernel *kn = &kernels[k];
		if (getenv("K") && strcmp(kn->name, getenv("K"))) continue;
		double emit[16], total[16];
		sljit_uw size = 0;
		for (int r = 0; r < rounds; r++) {
			double sum_emit = 0, sum_total = 0;
			long n = 0;
			for (double start = now(); now() - start < budget || n == 0; n++) {
				double t0 = now();
				struct sljit_compiler *c = sljit_create_compiler(NULL);
				kn->emit(c);
				double t1 = now();
				void *code = sljit_generate_code(c, 0, NULL);
				size = sljit_get_generated_code_size(c);
				sljit_free_compiler(c);
				double t2 = now();
				sljit_free_code(code, NULL);
				sum_emit += t1 - t0;
				sum_total += t2 - t0;
			}
			emit[r] = sum_emit / n;
			total[r] = sum_total / n;
		}
		qsort(emit, rounds, sizeof emit[0], cmpd);
		qsort(total, rounds, sizeof total[0], cmpd);
		struct sljit_compiler *c = sljit_create_compiler(NULL);
		kn->emit(c);
		void *code = sljit_generate_code(c, 0, NULL);
		sljit_free_compiler(c);
		double best = 1e30;
		sljit_uw result = 0;
		for (int r = 0; r < rounds; r++) {
			double t0 = now();
			result = kn->args == 2 ? ((fn2)code)(kn->a, kn->b) : ((fn1)code)(kn->a);
			double t = now() - t0;
			if (t < best)
				best = t;
		}
		sljit_free_code(code, NULL);
		printf("%-10s %9.2fus %9.2fus %9.1f %8lu %10.3fus %lu\n", kn->name, emit[rounds / 2] / 1e3, total[rounds / 2] / 1e3,
			total[rounds / 2] / kn->ops, (unsigned long)size, best / 1e3, (unsigned long)result);
	}
	return 0;
}
