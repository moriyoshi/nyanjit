import pathlib
import tempfile
import unittest

from binfmt import configure_loong64


class BinfmtSelectionTest(unittest.TestCase):
    def test_stock_match_preserves_elf_magic_without_extra_argv0(self):
        with tempfile.TemporaryDirectory() as name:
            root = pathlib.Path(name)
            interpreter = root / "emulator"
            interpreter.touch()
            previous = root / "qemu-loong64"
            previous.write_text(
                "enabled\ninterpreter /usr/bin/qemu-loongarch64\n"
                "flags: POF\noffset 0\nmagic 7f454c4602010100\nmask ffffffffffffffff\n"
            )
            configure_loong64(root, interpreter)
            self.assertEqual(previous.read_text(), "-1")
            self.assertEqual(
                (root / "register").read_text(),
                r":qemu-loong64-debian:M:0:\x7f\x45\x4c\x46\x02\x01\x01\x00:"
                + r"\xff\xff\xff\xff\xff\xff\xff\xff:"
                + str(interpreter)
                + ":F",
            )

    def test_invalid_setup_keeps_previous_handler(self):
        for mode in ["missing-binary", "bad-magic", "missing-match", "multiple-handlers"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as name:
                root = pathlib.Path(name)
                interpreter = root / "emulator"
                if mode != "missing-binary":
                    interpreter.touch()
                previous = root / "qemu-loong64"
                original = "enabled\noffset 0\nmagic xx\nmask ff\n"
                if mode != "missing-match":
                    previous.write_text(original)
                if mode == "multiple-handlers":
                    (root / "qemu-loongarch64").write_text(original)
                with self.assertRaises((RuntimeError, ValueError)):
                    configure_loong64(root, interpreter)
                if previous.exists():
                    self.assertEqual(previous.read_text(), original)
                self.assertFalse((root / "register").exists())
