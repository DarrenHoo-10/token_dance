from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[3]
CHECKER = ROOT / "collector/packaging/windows/check_pe_gui.py"


def pe_bytes(machine=0x8664, magic=0x20B, subsystem=2):
    data = bytearray(160)
    data[:2] = b"MZ"
    data[60:64] = (64).to_bytes(4, "little")
    data[64:68] = b"PE\0\0"
    data[68:70] = machine.to_bytes(2, "little")
    data[88:90] = magic.to_bytes(2, "little")
    data[156:158] = subsystem.to_bytes(2, "little")
    return bytes(data)


class PeGuiTests(unittest.TestCase):
    def check(self, data):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "TokenDance.exe"
            path.write_bytes(data)
            return subprocess.run(
                [sys.executable, str(CHECKER), str(path)],
                capture_output=True,
                text=True,
            )

    def test_gui_x64_pe_is_accepted(self):
        result = self.check(pe_bytes())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Windows x64 GUI executable", result.stdout)

    def test_console_subsystem_is_rejected(self):
        result = self.check(pe_bytes(subsystem=3))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Windows GUI subsystem", result.stderr)

    def test_non_x64_pe_is_rejected(self):
        result = self.check(pe_bytes(machine=0x14C))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Windows x64", result.stderr)


if __name__ == "__main__":
    unittest.main()
