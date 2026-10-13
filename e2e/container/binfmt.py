"""Choose stock LoongArch QEMU in the entrypoint's private binfmt namespace."""

import pathlib
import sys


def configure_loong64(directory, interpreter):
    directory = pathlib.Path(directory)
    interpreter = pathlib.Path(interpreter)
    if not interpreter.is_file():
        raise RuntimeError(f"missing stock LoongArch emulator: {interpreter}")
    entries = [p for p in directory.iterdir() if "loong" in p.name]
    if len(entries) != 1:
        raise RuntimeError(f"expected one LoongArch binfmt entry, found {entries}")
    previous = entries[0]
    fields = dict(
        line.split(" ", 1)
        for line in previous.read_text().splitlines()
        if " " in line
    )

    def escaped(value):
        return "".join(f"\\x{byte:02x}" for byte in bytes.fromhex(value))

    # Reuse the installer's ELF match, but stock QEMU must not receive P's
    # extra original argv[0]. F holds the interpreter for child reexecution.
    record = (
        f":qemu-loong64-debian:M:{int(fields['offset'])}:"
        f"{escaped(fields['magic'])}:{escaped(fields['mask'])}:"
        f"{interpreter}:F"
    )
    previous.write_text("-1")
    (directory / "register").write_text(record)


if __name__ == "__main__":
    configure_loong64("/proc/sys/fs/binfmt_misc", sys.argv[1])
