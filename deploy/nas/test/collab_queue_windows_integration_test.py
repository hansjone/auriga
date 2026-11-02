#!/usr/bin/env python3
"""Windows: Python weekly CLI -> production offline signer -> production verifier.

Build the operator jar first (deploy/nas/bin/collab-key-operator.jar), or set
AURIGA_TEST_OPERATOR_JAR. A requested Windows integration run fails if it is absent.
"""
from datetime import datetime, timezone
from copy import deepcopy
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
        # Historical NAS binding remains immutable; a separately approved new
        # signer provides verify-archived and the stricter signing policy.
        self.nas_jar_hash = hashlib.sha256(b"historical-nas-operator-jar").hexdigest()
        self.assertNotEqual(jar_hash, self.nas_jar_hash)
        source = self.base / "request"
        source.mkdir()
        now = int(datetime.now(timezone.utc).timestamp()) - 2
        stamp = lambda t: datetime.fromtimestamp(t, timezone.utc).isoformat().replace("+00:00", "Z")
        # Signed current base stays installable; the new batch starts on a
        # future UTC grid, exercising archived verification without time travel.
        first = ((now + 21600 + 43199) // 43200) * 43200
        entries = []
        self.count = 14 if self._testMethodName == 'test_week_with_distinct_nas_binding' else 2
        base_keys = [{"kid": "fixture-business", "alg": "Ed25519", "purpose": "business_delivery", "state": "active",
            "public_key_spki": spki, "not_before": stamp(now), "not_after": stamp(now + 7 * 86400)}]
        current_keys = deepcopy(base_keys)
        if self.count == 14:
            generated = subprocess.run(["java", str(Path(__file__).with_name("QueueSigningFixture.java")),
                str(self.base), "public-keys"], capture_output=True, text=True, check=True)
            public_keys = generated.stdout.strip().splitlines()
            purposes = ["business_delivery", "protocol_response", "management_state"]
            base_keys = [{"kid": "old-" + purpose, "alg": "Ed25519", "purpose": purpose, "state": "active",
                "public_key_spki": public_keys[i], "not_before": stamp(first - 5 * 86400),
                "not_after": stamp(first + 36 * 3600)} for i, purpose in enumerate(purposes)]
            current_keys = deepcopy(base_keys)
        for i in range(self.count):
            if self.count == 14:
                if i in (0, 11):
                    for j, purpose in enumerate(purposes):
                        current_keys.append({"kid": "nas-" + purpose.replace("_", "-") + "-" + ("a" if i == 0 else "b") * 24,
                            "alg": "Ed25519", "purpose": purpose, "state": "next",
                            "public_key_spki": public_keys[(3 if i == 0 else 6) + j],
                            "not_before": stamp(first + i * 43200), "not_after": stamp(first + i * 43200 + 7 * 86400)})
                if i == 3:
                    for key in current_keys:
                        key["state"] = "retired" if key["state"] == "active" else "active"
            version = str(i + 2)
            dest = source / version
            dest.mkdir()
            issued, until = stamp(first + i * 43200), stamp(first + i * 43200 + 86400)
            files = {"payload.json": {"schema_version": 1, "realm": "1", "keyset_version": version,
                "issued_at": issued, "valid_until": until, "keys": deepcopy(current_keys)},
                "trust.json": {"rootKid": "fixture-root", "rootFingerprint": self.fingerprint,
                    "operatorJarSha256": self.nas_jar_hash, "publicationMode": "regular", "realm": "1"},
                "input.json": {"privateFiles": {}}}
            hashes = {}
            for name, content in files.items():
                raw = json.dumps(content).encode()
                (dest / name).write_bytes(raw)
                hashes[name] = hashlib.sha256(raw).hexdigest()
            entries.append({"version": version, "issuedAt": issued, "validUntil": until, "files": hashes})
        base = source / "base"
        base.mkdir()
        payload = json.loads((source / "2/payload.json").read_text())
        payload.update(keyset_version="1", issued_at=stamp(now), valid_until=stamp(now+86400))
        payload["keys"] = base_keys
        (base / "payload.json").write_text(json.dumps(payload))
        (base / "root.der").write_bytes((self.base / "public.der").read_bytes())
        loader = self.base / "empty-loader.properties"
        loader.write_bytes(b"")
        self.launch = ["java", "-Dloader.main=auriga.collab.network.keys.OfflineKeySigning",
            "-Dloader.config.location=" + loader.as_uri(), "-cp", str(self.jar),
            "org.springframework.boot.loader.launch.PropertiesLauncher"]
        made = subprocess.run(self.launch + ["sign",str(base / "payload.json"),"fixture-root",str(self.base / "private.der"),
            str(base / "signed.json"), str(base / "announcement.json")],capture_output=True,text=True)
        self.assertEqual(made.returncode,0,made.stderr)
        base_files={name:hashlib.sha256((base/name).read_bytes()).hexdigest() for name in ("signed.json","announcement.json","root.der")}
        (source / "batch.json").write_text(json.dumps({"baseVersion": "1", "baseSignedSha256":base_files["signed.json"],"baseFiles":base_files,"entries": entries}))
        self.command = [sys.executable, "-I", "-X", "utf8", str(self.repo / "deploy/nas/scripts/sign-collab-queue.py"),
            "--review-state", str(self.base / "local-review"),
            "--request", str(source), "--output", str(self.base / "output"), "--jar", str(self.jar),
            "--jar-sha256", jar_hash, "--expected-nas-jar-sha256", self.nas_jar_hash,
            "--root-kid", "fixture-root", "--root-fingerprint", self.fingerprint,
            "--root-private", str(self.base / "private.der"), "--approved-by", "alice",
            "--confirm-batch-sha256",hashlib.sha256((source / "batch.json").read_bytes()).hexdigest()]

    def test_real_signer_and_verifier_and_no_overwrite(self):
        reviewed = subprocess.run(self.command + ["--review-only"], capture_output=True, text=True)
        self.assertEqual(reviewed.returncode, 0, reviewed.stderr)
        signed = subprocess.run(self.command, capture_output=True, text=True)
        self.assertEqual(signed.returncode, 0, signed.stderr)
        self.assertEqual(json.loads(signed.stdout.splitlines()[-1])["count"], 2)
        review = json.loads(signed.stdout.splitlines()[0])
        self.assertEqual(review["nasJarSha256"], self.nas_jar_hash)
        self.assertNotEqual(review["nasJarSha256"], review["signingJarSha256"])
        out = self.base / "output"
        # A genuine production verifier checks both root signatures and writes state.
        loader = self.base / "empty-loader.properties"
        loader.write_bytes(b"")
        verified = subprocess.run(["java", "-Dloader.main=auriga.collab.network.keys.OfflineKeySigning",
            "-Dloader.config.location=" + loader.as_uri(), "-cp", str(self.jar),
            "org.springframework.boot.loader.launch.PropertiesLauncher", "install",
            str(out / "base/signed.json"), str(out / "base/announcement.json"), "fixture-root",
            str(self.base / "public.der"), self.fingerprint, str(self.base / "state.json")],
            capture_output=True, text=True)
        self.assertEqual(verified.returncode, 0, verified.stderr)
        self.assertEqual(json.loads((self.base / "state.json").read_text())["version"], "1")
        for version in ("2", "3"):
            verified = subprocess.run(self.launch+["verify-archived",str(out/version/"signed.json"),
                str(out/version/"announcement.json"),"fixture-root",str(self.base/"public.der"),self.fingerprint],capture_output=True,text=True)
            self.assertEqual(verified.returncode,0,verified.stderr)
        original = (out / "2/signed.json").read_bytes()
        repeated = subprocess.run(self.command, capture_output=True, text=True)
        self.assertIsInstance(repeated.stderr, str, "CLI diagnostics must be decoded without reader-thread errors")
        self.assertNotEqual(repeated.returncode, 0)
        self.assertEqual((out / "2/signed.json").read_bytes(), original)
        self.assertEqual(len(list(out.rglob("approval.json"))), 2)
        self.assertEqual(list(out.rglob("*.der")),[out / "base/root.der"])
        # All generated artifacts inherit only the actual user, not OWNER RIGHTS.
        inspect = subprocess.run(["java", str(Path(__file__).with_name("QueueSigningFixture.java")),
            str(out), "inspect"], capture_output=True, text=True)
        self.assertEqual(inspect.returncode, 0, inspect.stderr)

    def test_week_with_distinct_nas_binding(self):
        source = self.base / "request"
        batch_path = source / "batch.json"
        batch = json.loads(batch_path.read_text())
        nas_hash = hashlib.sha256(b"independently approved historical NAS jar").hexdigest()
        for entry in batch["entries"]:
            path = source / entry["version"] / "trust.json"
            trust = json.loads(path.read_text())
            trust["operatorJarSha256"] = nas_hash
            path.write_text(json.dumps(trust))
            entry["files"]["trust.json"] = hashlib.sha256(path.read_bytes()).hexdigest()
        batch_path.write_text(json.dumps(batch))
        command = self.command[:-1] + [hashlib.sha256(batch_path.read_bytes()).hexdigest(),
            "--expected-nas-jar-sha256", nas_hash]
        review = subprocess.run(command + ["--review-only"], capture_output=True, text=True)
        self.assertEqual(review.returncode, 0, review.stderr)
        self.assertFalse((self.base / "output").exists())
        signed = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(signed.returncode, 0, signed.stderr)
        self.assertEqual(json.loads(signed.stdout.splitlines()[-1])["count"], 14)
        for entry in batch["entries"]:
            dest = self.base / "output" / entry["version"]
            verified = subprocess.run(self.launch + ["verify-archived", str(dest / "signed.json"),
                str(dest / "announcement.json"), "fixture-root", str(self.base / "public.der"),
                self.fingerprint], capture_output=True, text=True)
            self.assertEqual(verified.returncode, 0, verified.stderr)
        wrong = command[:-1] + ["0" * 64, "--review-only"]
        refused = subprocess.run(wrong, capture_output=True, text=True)
        self.assertNotEqual(refused.returncode, 0)

    def test_exposed_root_is_still_rejected(self):
        reviewed = subprocess.run(self.command + ["--review-only"], capture_output=True, text=True)
        self.assertEqual(reviewed.returncode, 0, reviewed.stderr)
        expose = subprocess.run(["icacls", str(self.base / "private.der"), "/grant", "*S-1-1-0:(R)"],
            capture_output=True, text=True)
        self.assertEqual(expose.returncode, 0, expose.stderr)
        signed = subprocess.run(self.command, capture_output=True, text=True)
        self.assertNotEqual(signed.returncode, 0)
        self.assertFalse((self.base / "output/2/signed.json").exists())
        self.assertFalse((self.base / "output/3/signed.json").exists())

    def test_tampered_base_and_unconfirmed_plan_never_sign(self):
        reviewed = subprocess.run(self.command + ["--review-only"], capture_output=True, text=True)
        self.assertEqual(reviewed.returncode, 0, reviewed.stderr)
        wrong = self.command[:-1]+["0"*64]
        signed = subprocess.run(wrong,capture_output=True,text=True)
        self.assertNotEqual(signed.returncode,0)
        self.assertFalse((self.base/"output").exists())
        base=self.base/"request/base/signed.json"
        value=json.loads(base.read_text());value["root_auth"]["signature"]="A"*86
        base.write_text(json.dumps(value))
        batch=self.base/"request/batch.json";plan=json.loads(batch.read_text())
        plan["baseFiles"]["signed.json"]=hashlib.sha256(base.read_bytes()).hexdigest()
        plan["baseSignedSha256"]=plan["baseFiles"]["signed.json"]
        batch.write_text(json.dumps(plan))
        tampered=self.command[:-1]+[hashlib.sha256(batch.read_bytes()).hexdigest()]
        signed=subprocess.run(tampered,capture_output=True,text=True)
        self.assertNotEqual(signed.returncode,0)
        self.assertFalse((self.base/"output").exists())

    def test_changed_history_refused_even_with_matching_export_hashes(self):
        source=self.base/"request"
        payload=source/"2/payload.json"
        value=json.loads(payload.read_text())
        # A compromised NAS can recompute every unsigned export hash. It still
        # cannot cause a change to a key already authenticated by the base root.
        value["keys"][0]["not_after"]=value["valid_until"]
        payload.write_text(json.dumps(value))
        batch=source/"batch.json";plan=json.loads(batch.read_text())
        plan["entries"][0]["files"]["payload.json"]=hashlib.sha256(payload.read_bytes()).hexdigest()
        batch.write_text(json.dumps(plan))
        changed=self.command[:-1]+[hashlib.sha256(batch.read_bytes()).hexdigest()]
        signed=subprocess.run(changed,capture_output=True,text=True)
        self.assertNotEqual(signed.returncode,0)
        self.assertIn("old key changed",signed.stderr)
        self.assertFalse((self.base/"output").exists())


if __name__ == "__main__":
    unittest.main()
