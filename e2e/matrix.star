load("targets.star", "targets", "capability")
load("smoke.star", "run_smoke")

def sweep_count():
    count = int(getenv("NYANJIT_E2E_VAR_SWEEP", "2000"))
    if count <= 0:
        fail("NYANJIT_E2E_VAR_SWEEP must be positive")
    return str(count)

def build_target(t):
    pkgs = packages(t)
    return build_tests(t, pkgs, timeout = 1200)

def run_target(t, binaries):
    if capability(t) == "no runner":
        for binary in binaries:
            skip(t, "matrix", binary, "no runner: " + runner_reason(t))
        return
    for binary in binaries:
        result = run(t, binary, args = ["-test.v", "-test.timeout=10m"], timeout = 2400)
        check(t, "matrix", binary, result)
        # Fixed differential sweeps share the native coverage-guided decoder.
        # Every applicable compiler/ABI/stack configuration is checked.
        if (t.goos == "linux" and t.go_version == "1.27.2" and
            (t.goarch not in ["ppc64", "ppc64le"] or t.GOPPC64 == "power8") and
            binary.endswith("_compile_varfuzz")):
            sweep = run(t, binary,
                        args = ["-test.v", "-test.run=^TestScopedVarsSweep$", "-test.timeout=30m"],
                        env = {"NYANJIT_VAR_SWEEP": sweep_count(), "GOMAXPROCS": "2"}, timeout = 1800)
            check(t, "matrix", binary + " scoped-variable sweep", sweep)

def main():
    # Gate 1: vet once, before cross-compilation.
    all_targets = targets()
    matrix_targets = [t for t in all_targets if selected(t, "matrix")]
    if matrix_targets:
        vet = sh(["go", "vet", "./..."], timeout = 900)
        check(matrix_targets[0], "matrix", "go vet", vet)

    # Gate 2: compile every package for every selected pair. The Go harness
    # limits simultaneous commands and records each failed package separately.
    built = []
    if matrix_targets:
        tasks = [go(build_target, t) for t in matrix_targets]
        for i in range(len(tasks)):
            built.append((matrix_targets[i], join(tasks[i], timeout = 7200)))

    # Gate 3: execute binaries only after all cross-builds have finished.
    tasks = [go(run_target, t, binaries) for t, binaries in built]
    for t in all_targets:
        if selected(t, "smoke"):
            tasks.append(go(run_smoke, t))
    for task in tasks:
        join(task, timeout = 7200)
