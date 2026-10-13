# Cross-compilation only: PAIRS and variants stay in targets.star.
load("targets.star", "targets")

def build_target(t):
    # go test -c type-checks/builds packages even when they have no tests;
    # packages(t) also includes the examples and platform stub packages.
    return build_tests(t, packages(t), timeout = 1200)

def main():
    tasks = [go(build_target, t) for t in targets() if selected(t, "build")]
    for task in tasks:
        join(task, timeout = 7200)
