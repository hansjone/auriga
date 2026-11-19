import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import json
import shutil

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("login_preference", ROOT / "scripts/login-preference.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class LoginPreferenceTest(unittest.TestCase):
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
