#!/usr/bin/env python3
"""Offline CLI boundary tests. Cryptographic pair verification is in Go/Java."""
import argparse
from datetime import datetime, timezone
import importlib.util
import json
import sys
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("signer", Path(__file__).parents[1] / "scripts/sign-collab-queue.py")
signer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(signer)


class QueueSignerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.source = self.base / "request"
        self.source.mkdir(mode=0o700)
        self.jar = self.base / "operator.jar"
        self.jar.write_bytes(b"fixture-only-not-executable")
        self.jar.chmod(0o600)
        now = int(signer.time.time())
        timestamp = lambda t: datetime.fromtimestamp(t, timezone.utc).isoformat().replace("+00:00", "Z")
        entries = []
        for i in range(2):
            v = str(10 + i)
            d = self.source / v
            d.mkdir(mode=0o700)
            issued, until = timestamp(now + 43200 * i), timestamp(now + 43200 * i + 86400)
            files = {"payload.json": {"keyset_version": v, "realm": "1", "issued_at": issued, "valid_until": until},
                     "trust.json": {"rootKid": "fixture-root", "rootFingerprint": "sha256:fixture",
                                    "operatorJarSha256": signer.digest(self.jar.read_bytes()), "publicationMode": "regular", "realm": "1"},
                     "input.json": {"privateFiles": {}}}
            hashes = {}
            for name, content in files.items():
                raw = json.dumps(content).encode()
                (d / name).write_bytes(raw)
                (d / name).chmod(0o600)
                hashes[name] = signer.digest(raw)
            entries.append({"version": v, "issuedAt": issued, "validUntil": until, "files": hashes})
        (self.source / "batch.json").write_text(json.dumps({"baseVersion": "7", "entries": entries}))
        (self.source / "batch.json").chmod(0o600)
        self.args = argparse.Namespace(request=str(self.source), output=str(self.base / "output"), jar=str(self.jar),
            jar_sha256=signer.digest(self.jar.read_bytes()), root_kid="fixture-root", root_fingerprint="sha256:fixture",
            root_private=str(self.base / "private.der"), approved_by="alice", java="java")

    def test_scheduled_future_and_audit_and_no_overwrite(self):
        calls = []
        def run(argv, **kwargs):
            calls.append(argv)
            # Stand-in for the production CLI; signature correctness is tested in Go.
            Path(argv[-2]).write_bytes(b"signed fixture")
            Path(argv[-1]).write_bytes(b"announcement fixture")
            Path(argv[-2]).chmod(0o600)
            Path(argv[-1]).chmod(0o600)
            return argparse.Namespace(returncode=0)
        with patch.object(signer.subprocess, "run", run):
            signer.sign(self.args)
            with self.assertRaises(FileExistsError):
                signer.sign(self.args)
        self.assertEqual(len(calls), 2)
        for v in ("10", "11"):
            audit = json.loads((Path(self.args.output) / v / "approval.json").read_text())
            self.assertEqual(audit["approvedBy"], "alice")
            self.assertEqual(audit["signedSha256"], signer.digest(b"signed fixture"))
        self.assertFalse((Path(self.args.output) / "private.der").exists())

    def test_stale_or_wrong_pin_never_invokes_root_signer(self):
        for option in ("stale", "pin", "jar"):
            with self.subTest(option=option), patch.object(signer.subprocess, "run") as run:
                args = argparse.Namespace(**vars(self.args))
                if option == "pin":
                    args.root_fingerprint = "sha256:wrong"
                elif option == "jar":
                    args.jar_sha256 = "wrong"
                with patch.object(signer.time, "time", return_value=signer.time.time() + (301 if option == "stale" else 0)):
                    with self.assertRaises(ValueError):
                        signer.sign(args)
                run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
