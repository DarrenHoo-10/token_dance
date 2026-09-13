import datetime
import hashlib
import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "macos/prepare-keychain-signing.py"
spec = importlib.util.spec_from_file_location("keychain_signing", SCRIPT)
signing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(signing)
TEAM = "TESTTEAM01"
APP = "io.tokendance.desktop"
CERTIFICATE = b"test-only certificate bytes, no private key"
HASH = hashlib.sha1(CERTIFICATE).hexdigest()
NOW = datetime.datetime(2026, 1, 1, tzinfo=datetime.timezone.utc)


def profile():
    return {
        "TeamIdentifier": [TEAM], "Platform": ["OSX"], "ProvisionsAllDevices": True,
        "ExpirationDate": NOW + datetime.timedelta(days=1), "DeveloperCertificates": [CERTIFICATE],
        "Entitlements": {
            "com.apple.application-identifier": f"{TEAM}.{APP}",
            "com.apple.developer.team-identifier": TEAM,
            "keychain-access-groups": [f"{TEAM}.*"],
        },
    }


class KeychainSigningTests(unittest.TestCase):
    def prepare(self, value, **kwargs):
        return signing.build_entitlements({"com.apple.security.app-sandbox": False}, value, TEAM, APP, HASH, now=NOW, **kwargs)

    def test_derives_exact_application_group_and_keeps_template_policy(self):
        result = self.prepare(profile())
        self.assertEqual(result["com.apple.application-identifier"], f"{TEAM}.{APP}")
        self.assertEqual(result["com.apple.developer.team-identifier"], TEAM)
        self.assertEqual(result["keychain-access-groups"], [f"{TEAM}.{APP}"])
        self.assertFalse(result["com.apple.security.app-sandbox"])

    def test_rejects_missing_profile_authorization(self):
        for field in ("com.apple.application-identifier", "com.apple.developer.team-identifier", "keychain-access-groups"):
            with self.subTest(field=field):
                value = profile()
                del value["Entitlements"][field]
                with self.assertRaises(ValueError): self.prepare(value)

    def test_rejects_other_team_bundle_certificate_and_platform(self):
        for mutate in (
            lambda p: p.update(TeamIdentifier=["OTHERTEAM1"]),
            lambda p: p["Entitlements"].update({"com.apple.application-identifier": f"{TEAM}.other.app"}),
            lambda p: p.update(DeveloperCertificates=[b"another certificate"]),
            lambda p: p.update(Platform=["iOS"]),
        ):
            value = profile(); mutate(value)
            with self.assertRaises(ValueError): self.prepare(value)

    def test_rejects_expired_profile_and_development_release(self):
        for changes in ({"ExpirationDate": NOW}, {"ProvisionsAllDevices": False}):
            value = profile(); value.update(changes)
            with self.assertRaises(ValueError): self.prepare(value)
        value = profile(); value["Entitlements"]["get-task-allow"] = True
        with self.assertRaises(ValueError): self.prepare(value)

    def test_development_profile_requires_explicit_opt_in(self):
        value = profile(); value["ProvisionsAllDevices"] = False
        result = self.prepare(value, allow_development=True)
        self.assertEqual(result["keychain-access-groups"], [f"{TEAM}.{APP}"])
        self.assertNotIn("get-task-allow", result)

    def test_only_explicit_team_and_scoped_wildcards_are_accepted(self):
        for team in ("", "$(TeamIdentifierPrefix)", "testteam01", "OTHERTEAM1"):
            with self.assertRaises(ValueError):
                signing.build_entitlements({}, profile(), team, APP, HASH, now=NOW)
        self.assertFalse(signing.authorized("*", f"{TEAM}.{APP}"))
        self.assertFalse(signing.authorized("TEST*.io.*", f"{TEAM}.{APP}"))


if __name__ == "__main__":
    unittest.main()
