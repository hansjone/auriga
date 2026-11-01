#!/usr/bin/env python3
"""Offline CLI boundary tests. Cryptographic pair verification is in Go/Java."""
import argparse
import base64
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
        now = (int(signer.time.time()) // 43200 + 1) * 43200
        self.first = now
        timestamp = lambda t: datetime.fromtimestamp(t, timezone.utc).isoformat().replace("+00:00", "Z")
        entries = []
        for i in range(2):
            v = str(10 + i)
            d = self.source / v
            d.mkdir(mode=0o700)
            issued, until = timestamp(now + 43200 * i), timestamp(now + 43200 * i + 86400)
            files = {"payload.json": {"schema_version":1,"keyset_version": v, "realm": "1", "issued_at": issued, "valid_until": until,
                        "keys":[{"kid":"old","alg":"Ed25519","purpose":"business_delivery","state":"active",
                            "public_key_spki":base64.urlsafe_b64encode(bytes.fromhex("302a300506032b6570032100")+bytes(32)).decode().rstrip("="),
                            "not_before":timestamp(now-43200),"not_after":timestamp(now+6*86400)}]},
                     "trust.json": {"rootKid": "fixture-root", "rootFingerprint": "sha256:"+signer.digest(b"fixture-public-root"),
                                    "operatorJarSha256": signer.digest(self.jar.read_bytes()), "publicationMode": "regular", "realm": "1"},
                     "input.json": {"privateFiles": {}}}
            hashes = {}
            for name, content in files.items():
                raw = json.dumps(content).encode()
                (d / name).write_bytes(raw)
                (d / name).chmod(0o600)
                hashes[name] = signer.digest(raw)
            entries.append({"version": v, "issuedAt": issued, "validUntil": until, "files": hashes})
        base = self.source / "base"
        base.mkdir()
        payload=json.loads((self.source / "10/payload.json").read_text())
        payload.update(keyset_version="7",issued_at=timestamp(now-43200),valid_until=timestamp(now+43200))
        base_files={"signed.json":json.dumps({"keyset":payload}).encode(),"announcement.json":b"{}","root.der":b"fixture-public-root"}
        for name,raw in base_files.items(): (base/name).write_bytes(raw); (base/name).chmod(0o600)
        (self.source / "batch.json").write_text(json.dumps({"baseVersion": "7", "baseSignedSha256":signer.digest(base_files["signed.json"]),
            "baseFiles":{name:signer.digest(raw) for name,raw in base_files.items()},"entries": entries}))
        (self.source / "batch.json").chmod(0o600)
        self.args = argparse.Namespace(request=str(self.source), output=str(self.base / "output"), jar=str(self.jar),
            jar_sha256=signer.digest(self.jar.read_bytes()), root_kid="fixture-root", root_fingerprint="sha256:"+signer.digest(b"fixture-public-root"),
            root_private=str(self.base / "private.der"), approved_by="alice", java="java",
            confirm_batch_sha256=signer.digest((self.source/"batch.json").read_bytes()))

    def test_scheduled_future_and_audit_and_no_overwrite(self):
        calls = []
        def run(argv, **kwargs):
            calls.append(argv)
            if "verify-archived" in argv:
                return argparse.Namespace(returncode=0)
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
        self.assertEqual(sum("sign" in c for c in calls), 2)
        for v in ("10", "11"):
            audit = json.loads((Path(self.args.output) / v / "approval.json").read_text())
            self.assertEqual(audit["approvedBy"], "alice")
            self.assertEqual(audit["signedSha256"], signer.digest(b"signed fixture"))
        self.assertFalse((Path(self.args.output) / "private.der").exists())

    def test_exact_batch_confirmation_and_public_base_required_before_sign(self):
        for option in ("confirmation", "base-signature", "base-pin"):
            with self.subTest(option=option):
                args=argparse.Namespace(**vars(self.args))
                if option=="confirmation": args.confirm_batch_sha256="0"*64
                if option=="base-pin": args.root_fingerprint="sha256:wrong"
                with patch.object(signer.subprocess,"run",return_value=argparse.Namespace(returncode=1 if option=="base-signature" else 0)) as run:
                    with self.assertRaises(ValueError): signer.sign(args)
                    self.assertFalse(any("sign" in c.args[0] for c in run.call_args_list))
                    self.assertFalse(Path(args.output).exists())

    def test_stale_or_wrong_pin_never_invokes_root_signer(self):
        for option in ("stale", "pin", "jar"):
            with self.subTest(option=option), patch.object(signer.subprocess, "run") as run:
                args = argparse.Namespace(**vars(self.args))
                if option == "pin":
                    args.root_fingerprint = "sha256:wrong"
                elif option == "jar":
                    args.jar_sha256 = "wrong"
                with patch.object(signer.time, "time", return_value=self.first + 301 if option == "stale" else signer.time.time()):
                    with self.assertRaises(ValueError):
                        signer.sign(args)
                run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
