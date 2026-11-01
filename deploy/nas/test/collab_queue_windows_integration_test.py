#!/usr/bin/env python3
"""Windows: Python weekly CLI -> production offline signer -> production verifier.

Build the operator jar first (deploy/nas/bin/collab-key-operator.jar), or set
AURIGA_TEST_OPERATOR_JAR. A requested Windows integration run fails if it is absent.
"""
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


@unittest.skipUnless(os.name == "nt", "Windows ACL integration")
class WindowsQueueIntegrationTests(unittest.TestCase):
    def setUp(self):
        self.repo = Path(__file__).resolve().parents[3]
        self.jar = Path(os.environ.get("AURIGA_TEST_OPERATOR_JAR",
            self.repo / "deploy/nas/bin/collab-key-operator.jar")).resolve()
        self.assertTrue(self.jar.is_file(), "build the production operator jar first")
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        generated = subprocess.run(["java", str(Path(__file__).with_name("QueueSigningFixture.java")),
            str(self.base)], capture_output=True, text=True, check=True)
        fingerprint, spki = generated.stdout.strip().splitlines()
        self.fingerprint = "sha256:" + fingerprint
        jar_hash = hashlib.sha256(self.jar.read_bytes()).hexdigest()
        source = self.base / "request"
        source.mkdir()
        now = int(datetime.now(timezone.utc).timestamp()) - 2
        stamp = lambda t: datetime.fromtimestamp(t, timezone.utc).isoformat().replace("+00:00", "Z")
        entries = []
        for i in range(2):
            version = str(i + 2)
            dest = source / version
            dest.mkdir()
            issued, until = stamp(now + i * 43200), stamp(now + i * 43200 + 86400)
            files = {"payload.json": {"schema_version": 1, "realm": "1", "keyset_version": version,
                "issued_at": issued, "valid_until": until, "keys": [{"kid": "fixture-business",
                "alg": "Ed25519", "purpose": "business_delivery", "state": "active",
                "public_key_spki": spki, "not_before": stamp(now), "not_after": stamp(now + 604800)}]},
                "trust.json": {"rootKid": "fixture-root", "rootFingerprint": self.fingerprint,
                    "operatorJarSha256": jar_hash, "publicationMode": "regular", "realm": "1"},
                "input.json": {"privateFiles": {}}}
            hashes = {}
            for name, content in files.items():
                raw = json.dumps(content).encode()
                (dest / name).write_bytes(raw)
                hashes[name] = hashlib.sha256(raw).hexdigest()
            entries.append({"version": version, "issuedAt": issued, "validUntil": until, "files": hashes})
        (source / "batch.json").write_text(json.dumps({"baseVersion": "1", "entries": entries}))
        self.command = [sys.executable, "-I", str(self.repo / "deploy/nas/scripts/sign-collab-queue.py"),
            "--request", str(source), "--output", str(self.base / "output"), "--jar", str(self.jar),
            "--jar-sha256", jar_hash, "--root-kid", "fixture-root", "--root-fingerprint", self.fingerprint,
            "--root-private", str(self.base / "private.der"), "--approved-by", "alice"]

    def test_real_signer_and_verifier_and_no_overwrite(self):
        signed = subprocess.run(self.command, capture_output=True, text=True)
        self.assertEqual(signed.returncode, 0, signed.stderr)
        self.assertEqual(json.loads(signed.stdout)["count"], 2)
        out = self.base / "output"
        # A genuine production verifier checks both root signatures and writes state.
        loader = self.base / "empty-loader.properties"
        loader.write_bytes(b"")
        verified = subprocess.run(["java", "-Dloader.main=auriga.collab.network.keys.OfflineKeySigning",
            "-Dloader.config.location=" + loader.as_uri(), "-cp", str(self.jar),
            "org.springframework.boot.loader.launch.PropertiesLauncher", "install",
            str(out / "2/signed.json"), str(out / "2/announcement.json"), "fixture-root",
            str(self.base / "public.der"), self.fingerprint, str(self.base / "state.json")],
            capture_output=True, text=True)
        self.assertEqual(verified.returncode, 0, verified.stderr)
        self.assertEqual(json.loads((self.base / "state.json").read_text())["version"], "2")
        original = (out / "2/signed.json").read_bytes()
        repeated = subprocess.run(self.command, capture_output=True, text=True)
        self.assertNotEqual(repeated.returncode, 0)
        self.assertEqual((out / "2/signed.json").read_bytes(), original)
        self.assertEqual(len(list(out.rglob("approval.json"))), 2)
        self.assertFalse(list(out.rglob("*.der")))
        # All generated artifacts inherit only the actual user, not OWNER RIGHTS.
        inspect = subprocess.run(["java", str(Path(__file__).with_name("QueueSigningFixture.java")),
            str(out), "inspect"], capture_output=True, text=True)
        self.assertEqual(inspect.returncode, 0, inspect.stderr)

    def test_exposed_root_is_still_rejected(self):
        expose = subprocess.run(["icacls", str(self.base / "private.der"), "/grant", "*S-1-1-0:(R)"],
            capture_output=True, text=True)
        self.assertEqual(expose.returncode, 0, expose.stderr)
        signed = subprocess.run(self.command, capture_output=True, text=True)
        self.assertNotEqual(signed.returncode, 0)
        self.assertFalse(list((self.base / "output").rglob("signed.json")))


if __name__ == "__main__":
    unittest.main()
