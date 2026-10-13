load("targets.star", "targets")

def run_pie(t):
    if t.goarch == "ppc64" and t.go_version.startswith("1.26."):
        skip(t, "ppc-pie", "packages", "Go 1.26 does not support big-endian PPC64 PIE")
        return
    binaries = build_tests(t, packages(t), buildmode = "pie", timeout = 1200)
    for binary in binaries:
        result = run(t, binary, args = ["-test.v", "-test.timeout=10m"], timeout = 1200)
        check(t, "ppc-pie", binary, result)

def run_cgo(t):
    if t.goarch == "ppc64" and t.go_version.startswith("1.26."):
        skip(t, "ppc-cgo", "integration", "Go 1.26 does not support big-endian PPC64 cgo")
        return
    cc = "/opt/nyanjit/toolchains/powerpc64-linux-musl-cross/bin/powerpc64-linux-musl-gcc" if t.goarch == "ppc64" else "powerpc64le-linux-gnu-gcc"
    directory = "/cache/ppc-cgo-" + t.goarch + "-" + t.go_version + "-" + t.GOPPC64
    result = sh(["mkdir", "-p", directory], timeout = 30)
    check(t, "ppc-cgo", "prepare directory", result)
    library = directory + "/libfixture.so"
    result = sh([cc, "-mcpu=" + t.GOPPC64, "-shared", "-fPIC", "-o", library, "e2e/ppc64cgo/fixture/library.c"], timeout = 300)
    check(t, "ppc-cgo", "shared library build", result)
    if result.exit_code != 0:
        return
    for mode in ["exe", "pie"]:
        binary = directory + "/integration-" + mode + ".test"
        result = sh([
            "env", "GOOS=linux", "GOARCH=" + t.goarch, "GOPPC64=" + t.GOPPC64,
            "CGO_ENABLED=1", "CC=" + cc,
            "/opt/go" + t.go_version + "/bin/go", "-C", "e2e/ppc64cgo",
            "test", "-mod=readonly", "-c", "-buildmode=" + mode, "-o", binary, ".",
        ], timeout = 1200)
        check(t, "ppc-cgo", "Go " + mode + " build", result)
        if result.exit_code == 0:
            result = run(t, binary, args = ["-test.v", "-test.timeout=5m"], env = ["NYANJIT_PPC_C_FIXTURE=" + library], timeout = 600)
            check(t, "ppc-cgo", mode + " C/callback integration", result)

def main():
    # Native Go cross-compiles; QEMU executes the resulting target programs.
    tasks = []
    for t in targets():
        if t.goos == "linux" and t.goarch in ["ppc64", "ppc64le"]:
            if selected(t, "ppc-pie"):
                tasks.append(go(run_pie, t))
            if selected(t, "ppc-cgo"):
                tasks.append(go(run_cgo, t))
    for task in tasks:
        join(task, timeout = 7200)
