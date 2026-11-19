import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import json
import shutil
import hashlib

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("login_preference", ROOT / "scripts/login-preference.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class LoginPreferenceTest(unittest.TestCase):
    def custom_recipe(self):
        raw = (ROOT / "docker-compose.yml").read_bytes().replace(MODULE.MAPPING.encode(), b"")
        mount = b"      - ${AURIGA_DATA_ROOT}/config/materials/edge.config.json:/etc/auriga/edge.config.json:ro\n"
        prefix, edge = raw.split(b"\n  edge:\n", 1)
        self.assertEqual(edge.count(mount), 1)
        return prefix + b"\n  edge:\n" + edge.replace(mount, b"      - /synthetic/reviewed-assets:/srv/synthetic-assets:ro\n" + mount, 1)

    def test_explicit_pin_checks_complete_bytes_including_crlf_and_existing_mapping(self):
        raw = self.custom_recipe()
        with self.assertRaises(ValueError):
            MODULE.compose_projection((ROOT / "docker-compose.yml").read_bytes(), "0" * 64)
        with self.assertRaises(ValueError):
            MODULE.compose_projection(raw)
        with self.assertRaises(ValueError):
            MODULE.compose_projection(raw, "0" * 64)
        with self.assertRaises(ValueError):
            MODULE.compose_projection(raw, "not-a-sha256")
        pin = hashlib.sha256(raw).hexdigest()
        with self.assertRaises(ValueError):
            MODULE.compose_projection(raw + b"\n# changed after review\n", pin)
        patched = MODULE.compose_projection(raw, pin.upper())
        self.assertEqual(patched.replace(MODULE.MAPPING.encode(), b""), raw)
        with self.assertRaises(ValueError):
            MODULE.compose_projection(patched, pin)
        patched_pin = hashlib.sha256(patched).hexdigest()
        self.assertEqual(MODULE.compose_projection(patched, patched_pin), patched)
        crlf = raw.replace(b"\n", b"\r\n")
        with self.assertRaises(ValueError):
            MODULE.compose_projection(crlf, pin)
        crlf_patched = MODULE.compose_projection(crlf, hashlib.sha256(crlf).hexdigest())
        self.assertEqual(crlf_patched.replace(MODULE.MAPPING.replace("\n", "\r\n").encode(), b""), crlf)

    def test_correct_pin_never_bypasses_structure_duplicate_or_mixed_newline_guards(self):
        raw = self.custom_recipe()
        bad_inputs = [
            raw + MODULE.MAPPING.encode(),
            raw.replace(b"    environment:\n", b"    environment:\n    'environment': {}\n"),
            raw + b"\n  'edge': {}\n",
            raw + b"\n'services': {}\n",
            raw.replace(b"      AURIGA_EDGE_ALLOW_LOCAL_INGRESS: 'true'\n", b"      AURIGA_EDGE_ALLOW_LOCAL_INGRESS: 'true'\n" * 2),
            raw.replace(b"      AURIGA_EDGE_ALLOW_LOCAL_INGRESS: 'true'\n", b"      <<: *unknown\n"),
            raw.replace(b"      AURIGA_EDGE_ALLOW_LOCAL_INGRESS: 'true'\n", b"      AURIGA_EDGE_ALLOW_LOCAL_INGRESS: {nested: true}\n"),
            raw.replace(b"      AURIGA_EDGE_ALLOW_LOCAL_INGRESS: 'true'\n", ("      '" + MODULE.KEY + "': team\n").encode()),
            raw.replace(b"\n", b"\r\n", 1),
        ]
        for bad in bad_inputs:
            with self.subTest(bad=hashlib.sha256(bad).hexdigest()), self.assertRaises(ValueError):
                MODULE.compose_projection(bad, hashlib.sha256(bad).hexdigest())

    def test_custom_pinned_cli_repeats_and_rolls_back_with_a_new_current_pin(self):
        with tempfile.TemporaryDirectory() as directory:
            compose = Path(directory) / "docker-compose.yml"
            stack = Path(directory) / "stack.env"
            raw = self.custom_recipe()
            initial_stack = b"OTHER=preserved\n"
            compose.write_bytes(raw)
            stack.write_bytes(initial_stack)
            compose.chmod(0o640)
            stack.chmod(0o600)
            command = [sys.executable, str(ROOT / "scripts/login-preference.py"), "--compose", str(compose.resolve()), "--stack-env", str(stack.resolve()), "--preference", "team"]
            pin = hashlib.sha256(raw).hexdigest()
            for suffix in [[], ["--reviewed-compose-sha256", "0" * 64]]:
                self.assertNotEqual(subprocess.run(command + suffix, capture_output=True).returncode, 0)
                self.assertEqual(compose.read_bytes(), raw)
                self.assertEqual(stack.read_bytes(), initial_stack)
                self.assertEqual(list(Path(directory).glob("*.bak")), [])
            subprocess.run(command + ["--reviewed-compose-sha256", pin, "--check"], check=True, capture_output=True)
            self.assertEqual(compose.read_bytes(), raw)
            subprocess.run(command + ["--reviewed-compose-sha256", pin], check=True, capture_output=True)
            patched = compose.read_bytes()
            self.assertEqual(patched.replace(MODULE.MAPPING.encode(), b""), raw)
            self.assertEqual(compose.stat().st_mode & 0o777, 0o640)
            self.assertEqual(stack.stat().st_mode & 0o777, 0o600)
            self.assertNotEqual(subprocess.run(command + ["--reviewed-compose-sha256", pin], capture_output=True).returncode, 0)
            current_pin = hashlib.sha256(patched).hexdigest()
            subprocess.run(command + ["--reviewed-compose-sha256", current_pin], check=True, capture_output=True)
            subprocess.run(command[:-1] + ["company", "--reviewed-compose-sha256", current_pin], check=True, capture_output=True)
            self.assertEqual(compose.read_bytes(), patched)
            self.assertEqual(stack.read_bytes(), b"OTHER=preserved\nAURIGA_EDGE_LOGIN_PREFERENCE=company\n")
            self.assertIn(raw, [path.read_bytes() for path in Path(directory).glob("docker-compose.yml.*.bak")])
            stack.write_bytes(b"AURIGA_EDGE_LOGIN_PREFERENCE=team\n" * 2)
            self.assertNotEqual(subprocess.run(command + ["--reviewed-compose-sha256", current_pin], capture_output=True).returncode, 0)
            self.assertEqual(compose.read_bytes(), patched)
            stack.write_bytes(initial_stack)
            linked = Path(directory) / "compose-link.yml"
            linked.symlink_to(compose)
            linked_command = command.copy()
            linked_command[3] = str(linked)
            self.assertNotEqual(subprocess.run(linked_command + ["--reviewed-compose-sha256", current_pin], capture_output=True).returncode, 0)
            hardlink = Path(directory) / "compose-hardlink.yml"
            os.link(compose, hardlink)
            self.assertNotEqual(subprocess.run(command + ["--reviewed-compose-sha256", current_pin], capture_output=True).returncode, 0)
            self.assertEqual(compose.read_bytes(), patched)
            self.assertEqual(stack.read_bytes(), initial_stack)

    def test_atomic_write_refuses_input_changed_after_review(self):
        with tempfile.TemporaryDirectory() as directory:
            compose = Path(directory) / "docker-compose.yml"
            compose.write_bytes(self.custom_recipe())
            path, info, raw = MODULE.regular(str(compose.resolve()))
            patched = MODULE.compose_projection(raw, hashlib.sha256(raw).hexdigest())
            changed = raw + b"\n# concurrent change\n"
            compose.write_bytes(changed)
            with self.assertRaises(ValueError):
                MODULE.replace(path, info, raw, patched)
            self.assertEqual(compose.read_bytes(), changed)

    def test_real_legacy_env_writer_and_compose_interpolation_keep_the_preference(self):
        render = os.environ.get("AURIGA_TEST_LEGACY_RENDER")
        if not render or not shutil.which("docker"):
            self.skipTest("supply the public legacy renderer and Compose CLI for the offline projection check")
        probe = subprocess.run(["docker", "compose", "version"], capture_output=True)
        if probe.returncode:
            self.skipTest("Compose CLI unavailable")
        lines = [line for line in Path(render).read_text().splitlines() if line.startswith("printf ") and line.endswith('>"$CONFIG/edge.env"')]
        self.assertEqual(len(lines), 1)
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory)
            subprocess.run(["sh", "-c", lines[0]], env={**os.environ, "CONFIG": directory, "NATS_EDGE": "synthetic"}, check=True, capture_output=True)
            self.assertNotIn(MODULE.KEY, (folder / "edge.env").read_text())
            stack = folder / "stack.env"
            stack.write_bytes(MODULE.stack_projection(b"", "team"))
            compose = folder / "projection.yml"
            compose.write_text("services:\n  edge:\n    image: scratch\n    env_file: [./edge.env]\n    environment:\n" + MODULE.MAPPING)
            result = subprocess.run(["docker", "compose", "--env-file", str(stack), "-f", str(compose), "config", "--format", "json"], check=True, capture_output=True, text=True)
            self.assertEqual(json.loads(result.stdout)["services"]["edge"]["environment"][MODULE.KEY], "team")

    def test_current_and_supplied_legacy_recipe_only_gain_one_environment_line(self):
        sources = [ROOT / "docker-compose.yml"]
        if os.environ.get("AURIGA_TEST_LEGACY_COMPOSE"):
            sources.append(Path(os.environ["AURIGA_TEST_LEGACY_COMPOSE"]))
        for source in sources:
            with self.subTest(source=source), tempfile.TemporaryDirectory() as directory:
                compose = Path(directory) / "docker-compose.yml"
                stack = Path(directory) / "stack.env"
                raw = source.read_bytes()
                compose.write_bytes(raw)
                stack.write_text("AURIGA_IMAGE_EDGE=fixture\nOTHER=preserved\n")
                command = [sys.executable, str(ROOT / "scripts/login-preference.py"), "--compose", str(compose.resolve()), "--stack-env", str(stack.resolve()), "--preference", "team"]
                subprocess.run(command + ["--check"], check=True, capture_output=True)
                self.assertEqual(compose.read_bytes(), raw)
                subprocess.run(command, check=True, capture_output=True)
                patched = compose.read_bytes()
                self.assertEqual(patched, MODULE.compose_projection(raw))
                self.assertEqual(MODULE.compose_projection(patched), patched)
                self.assertIn("OTHER=preserved\n", stack.read_text())
                self.assertEqual(stack.read_text().count(MODULE.KEY + "=team"), 1)
                # The unchanged legacy renderer may replace edge.env on every start.
                edge_env = Path(directory) / "edge.env"
                edge_env.write_text("AURIGA_EDGE_NATS_URL=fixture\n")
                self.assertIn(MODULE.MAPPING, compose.read_text())
                self.assertIn(MODULE.KEY + "=team", stack.read_text())
                subprocess.run(command, check=True, capture_output=True)
                self.assertEqual(compose.read_bytes(), patched)
                subprocess.run(command[:-1] + ["company"], check=True, capture_output=True)
                self.assertIn(MODULE.KEY + "=company", stack.read_text())
                self.assertTrue(list(Path(directory).glob("*.bak")))

    def test_ambiguous_drifted_or_linked_inputs_are_refused(self):
        raw = (ROOT / "docker-compose.yml").read_bytes()
        with self.assertRaises(ValueError):
            MODULE.compose_projection(raw + b"\n# unreviewed recipe drift\n")
        with self.assertRaises(ValueError):
            MODULE.compose_projection(raw.replace(b"\n", b"\r\n", 1))
        with self.assertRaises(ValueError):
            MODULE.compose_projection(raw + MODULE.MAPPING.encode())
        with self.assertRaises(ValueError):
            MODULE.stack_projection(b"AURIGA_EDGE_LOGIN_PREFERENCE=team\nAURIGA_EDGE_LOGIN_PREFERENCE=company\n", "team")
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "target"
            target.write_text("fixture")
            link = Path(directory) / "link"
            try:
                link.symlink_to(target)
            except OSError:
                self.skipTest("symlinks unavailable")
            with self.assertRaises(ValueError):
                MODULE.regular(str(link))


if __name__ == "__main__":
    unittest.main()
