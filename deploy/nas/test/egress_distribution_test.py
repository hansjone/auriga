"""Deployment startup invariants; uses the existing NAS test dependency PyYAML."""
from pathlib import Path
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[3]


class EgressStartupTest(unittest.TestCase):
    def test_platform_outage_cannot_block_egress_startup(self):
        for path in ("deploy/compose/docker-compose.yml", "deploy/nas/docker-compose.yml"):
            with self.subTest(path=path):
                services = yaml.safe_load((ROOT / path).read_text(encoding="utf-8"))["services"]
                pending = list(services["egress"].get("depends_on", {}))
                seen = set()
                while pending:
                    name = pending.pop()
                    self.assertNotEqual(name, "platform")
                    if name not in seen:
                        seen.add(name)
                        pending.extend(services[name].get("depends_on", {}))
                self.assertEqual(services["agent"]["depends_on"]["egress"]["condition"], "service_healthy")
                self.assertFalse(services["egress"].get("ports"))

    def test_relay_preserves_inherited_proxy_settings(self):
        for path in ("deploy/compose/docker-compose.yml", "deploy/nas/docker-compose.yml"):
            with self.subTest(path=path):
                relay = yaml.safe_load((ROOT / path).read_text(encoding="utf-8"))["services"]["bifrost"]
                for key in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
                    self.assertNotIn(key, relay.get("environment", {}))


if __name__ == "__main__":
    unittest.main()
