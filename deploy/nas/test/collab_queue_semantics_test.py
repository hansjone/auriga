"""Offline policy: reject a changed plan before any private-root operation."""
from copy import deepcopy
import base64
from datetime import datetime, timezone
import importlib.util
from pathlib import Path
import sys
import unittest

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("signer", Path(__file__).parents[1] / "scripts/sign-collab-queue.py")
signer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(signer)

def spki(byte):
    return base64.urlsafe_b64encode(bytes.fromhex("302a300506032b6570032100") + bytes([byte]) * 32).decode().rstrip("=")


class QueuePolicyTests(unittest.TestCase):
    def setUp(self):
        self.at = 1800000000
        self.stamp = lambda n: datetime.fromtimestamp(n, timezone.utc).isoformat().replace("+00:00", "Z")
        self.old = {"schema_version": 1, "realm": "1", "keyset_version": "7",
            "issued_at": self.stamp(self.at - 43200), "valid_until": self.stamp(self.at + 43200),
            "keys": [{"kid": "old", "alg": "Ed25519", "purpose": "business_delivery", "state": "active",
                "public_key_spki": spki(1),
                "not_before": self.stamp(self.at - 432000), "not_after": self.stamp(self.at + 172800)}]}
        self.new = deepcopy(self.old)
        self.new.update(keyset_version="10", issued_at=self.stamp(self.at), valid_until=self.stamp(self.at + 86400))
        key = deepcopy(self.old["keys"][0])
        key.update(kid="nas-business-delivery-" + "a" * 24, state="next",
            public_key_spki=spki(2),
            not_before=self.stamp(self.at), not_after=self.stamp(self.at + 604800))
        self.new["keys"].append(key)

    def test_new_key_summary_and_unchanged_history(self):
        changes = signer.review_transition(self.old, self.new)
        self.assertEqual(len(changes), 1)
        self.assertEqual(changes[0]["kid"], self.new["keys"][1]["kid"])
        self.assertTrue(changes[0]["spkiFingerprint"].startswith("sha256:"))

    def test_unsafe_proposals_are_refused(self):
        for case in ("missing", "material", "purpose", "lifetime", "lifetime-nanos", "state", "early", "two", "algorithm", "kid", "duplicate", "root-reuse"):
            with self.subTest(case=case):
                old, new = deepcopy(self.old), deepcopy(self.new)
                if case == "missing": new["keys"].pop(0)
                elif case == "material": new["keys"][0]["public_key_spki"] = new["keys"][1]["public_key_spki"]
                elif case == "purpose": new["keys"][0]["purpose"] = "management_state"
                elif case == "lifetime": new["keys"][1]["not_after"] = self.stamp(self.at + 604801)
                elif case == "lifetime-nanos": new["keys"][1]["not_after"] = self.stamp(self.at + 604800).replace("Z", ".000000001Z")
                elif case == "state": old["keys"][0]["state"] = "revoked"
                elif case == "early":
                    old["keys"][0]["not_after"] = self.stamp(self.at + 172801)
                    new["keys"][0]["not_after"] = old["keys"][0]["not_after"]
                elif case == "two":
                    k = deepcopy(new["keys"][1]); k.update(kid="nas-business-delivery-"+"c"*24, public_key_spki=spki(3)); new["keys"].append(k)
                elif case == "algorithm": new["keys"][1]["alg"] = "none"
                elif case == "kid": new["keys"][1]["kid"] = "attacker"
                elif case == "duplicate": new["keys"].append(deepcopy(new["keys"][0]))
                elif case == "root-reuse": new["keys"][1]["public_key_spki"] = new["keys"][0]["public_key_spki"]
                with self.assertRaises(ValueError): signer.review_transition(old, new)

    def test_terminal_states_cannot_be_resurrected(self):
        for state in ("retired", "revoked"):
            old = deepcopy(self.old); new = deepcopy(self.new)
            old["keys"][0]["state"] = state
            with self.assertRaises(ValueError): signer.review_transition(old, new)

    def test_promotion_retirement_and_revocation_preserve_material(self):
        old = deepcopy(self.old)
        old["keys"].append(deepcopy(self.new["keys"][1]))
        promoted = deepcopy(self.new)
        promoted["keys"][0]["state"] = "retired"
        promoted["keys"][1]["state"] = "active"
        self.assertEqual(signer.review_transition(old, promoted), [])
        revoked = deepcopy(promoted)
        revoked.update(keyset_version="11", issued_at=self.stamp(self.at+43200), valid_until=self.stamp(self.at+129600))
        revoked["keys"][0]["state"] = "revoked"
        self.assertEqual(signer.review_transition(promoted, revoked), [])


if __name__ == "__main__": unittest.main()
