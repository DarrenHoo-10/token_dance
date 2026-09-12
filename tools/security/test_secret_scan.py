"""Behavioral regression tests for password formats missed by generic scanners."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
GITLEAKS = os.environ.get("GITLEAKS_BIN", "gitleaks")


class SecretScanTests(unittest.TestCase):
    def scan(self, content):
        with tempfile.TemporaryDirectory(prefix="secret-scan-test-") as folder:
            root = Path(folder)
            shutil.copyfile(ROOT / ".gitleaks.toml", root / ".gitleaks.toml")
            subprocess.run(["git", "init", "--quiet", folder], check=True)
            subprocess.run(["git", "-C", folder, "config", "core.autocrlf", "false"], check=True)
            subprocess.run(["git", "-C", folder, "-c", "user.name=Security Test",
                            "-c", "user.email=test@example.invalid", "commit", "--quiet",
                            "--allow-empty", "-m", "Initialize fixture"], check=True)
            (root / "operation.sh").write_text(content, encoding="utf-8")
            subprocess.run(["git", "-C", folder, "add", "."], check=True)
            report = root / "report.json"
            result = subprocess.run(
                [GITLEAKS, "git", folder, "--pre-commit", "--staged",
                 "--config", str(root / ".gitleaks.toml"), "--redact=100",
                 "--no-banner", "--log-level", "error", "--ignore-gitleaks-allow",
                 "--report-format", "json", "--report-path", str(report)],
                capture_output=True,
            )
            self.assertIn(result.returncode, (0, 1), "Scanner failed to execute")
            findings = json.loads(report.read_text()) if report.exists() else []
            return result.returncode, {finding["RuleID"] for finding in findings}

    def test_low_entropy_shell_passwords(self):
        # Construct synthetic values so the test source itself contains no literal secret.
        value = "aaab" * 4 + "!9"
        for key in ("SSH_PASSWORD", "NEW_ROOT", "NEW_APP", "MYSQL_ROOT_PASSWORD"):
            for quote, expected in (("'", "infrastructure-password-literal"),
                                    ('"', "infrastructure-password-literal"),
                                    ("", "infrastructure-password-unquoted")):
                with self.subTest(key=key, quote=quote):
                    code, rules = self.scan(key + "=" + quote + value + quote + "\n")
                    self.assertEqual(code, 1)
                    self.assertIn(expected, rules)

    def test_credentialed_dsn(self):
        code, rules = self.scan("dsn='app:" + "bbac" * 4 + "!8" + "@tcp(localhost:3306)/app'\n")
        self.assertEqual(code, 1)
        self.assertIn("mysql-dsn-password", rules)

    def test_login_payload(self):
        payload = json.dumps({"email": "person@example.invalid", "password": "ccab" * 4 + "!7"})
        code, rules = self.scan("curl --data '" + payload + "'\n")
        self.assertEqual(code, 1)
        self.assertIn("login-json-password", rules)

    def test_inline_allow_comment_does_not_bypass(self):
        code, rules = self.scan("NEW_ROOT='" + "ddac" * 4 + "!6" + "' # gitleaks:allow\n")
        self.assertEqual(code, 1)
        self.assertIn("infrastructure-password-literal", rules)

    def test_environment_reference_and_placeholder(self):
        code, rules = self.scan('SSH_PASSWORD="${DEPLOY_PASSWORD}"\nNEW_ROOT="REPLACE_WITH_SECRET"\n')
        self.assertEqual(code, 0)
        self.assertEqual(rules, set())


if __name__ == "__main__":
    unittest.main()
