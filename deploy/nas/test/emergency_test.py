"""Display formatting and failure boundaries; never use real enrollment material."""
import importlib.util
import io
import json
import os
from pathlib import Path
import secrets
import stat
import subprocess
import tempfile
import unittest
from unittest import mock
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('emergency', ROOT / 'scripts/emergency.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class Terminal(io.StringIO):
    def isatty(self):
        return True


class DisplayTests(unittest.TestCase):
    def setUp(self):
        self.credentials = {
            'username': 'emergency-' + secrets.token_hex(5),
            'password': secrets.token_urlsafe(32),
            'totpSecret': __import__('base64').b32encode(os.urandom(20)).decode(),
        }

    def test_domain_ip_proxy_origins_and_totp_uri(self):
        for mode in ('domain', 'ip', 'proxy'):
            with self.subTest(mode=mode):
                access = json.loads((ROOT / ('access-' + mode + '.example.json')).read_text())
                output = Terminal()
                module.display(access, self.credentials, output)
                text = output.getvalue()
                origin = access['adminOrigin']
                self.assertIn(f"{origin['scheme']}://{origin['host']}:{origin['port']}", text)
                self.assertNotIn(str(access['listeners']['admin']['port']) + '/', text)
                for value in self.credentials.values():
                    self.assertIn(value, text)
                uri = next(line for line in text.splitlines() if line.startswith('otpauth://'))
                self.assertEqual(urlsplit(uri).scheme, 'otpauth')
                self.assertEqual(parse_qs(urlsplit(uri).query), {
                    'secret': [self.credentials['totpSecret']], 'issuer': ['Auriga'],
                    'algorithm': ['SHA1'], 'digits': ['6'], 'period': ['30'],
                })
                self.assertIn('成员 → 邀请', text)
                self.assertIn('回环', text)

    def test_redirect_is_rejected_without_output(self):
        output = io.StringIO()
        with self.assertRaises(module.DisplayError):
            module.display({}, self.credentials, output)
        self.assertEqual(output.getvalue(), '')

    def test_invalid_material_never_partially_prints(self):
        access = json.loads((ROOT / 'access-ip.example.json').read_text())
        for key, value in (('password', '\x1b[2J'), ('username', 'bad\nuser'),
                           ('totpSecret', 'not-base32'), ('password', '')):
            with self.subTest(key=key, value=value):
                output = Terminal()
                with self.assertRaises(module.DisplayError):
                    module.display(access, {**self.credentials, key: value}, output)
                self.assertEqual(output.getvalue(), '')
        for origin in ({'scheme': 'file', 'host': 'admin.example.com', 'port': 443},
                       {'scheme': 'https', 'host': 'evil\x1b.example.com', 'port': 443},
                       {'scheme': 'https', 'host': 'admin.example.com', 'port': 0}):
            output = Terminal()
            with self.assertRaises(module.DisplayError):
                module.display({'adminOrigin': origin}, self.credentials, output)
            self.assertEqual(output.getvalue(), '')


@unittest.skipUnless(os.name == 'posix', 'requires Linux directory flock and fixed UID metadata')
class HandoffTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.directory.chmod(0o700)
        self.access = self.directory / 'access.json'
        self.access.write_text((ROOT / 'access-ip.example.json').read_text())
        self.enrollment = self.directory / 'emergency-enrollment.json'
        self.credentials = {
            'username': 'emergency-' + secrets.token_hex(5),
            'password': secrets.token_urlsafe(32),
            'totpSecret': __import__('base64').b32encode(os.urandom(20)).decode(),
        }
        self.enrollment.write_text(json.dumps(self.credentials))
        self.enrollment.chmod(0o600)
        self.marker = self.directory / '.emergency-displayed'
        # Exercise real Linux file/lock semantics; only emulate the container UID
        # if this unit test is running outside its fixed runtime identity.
        uid = os.geteuid()
        self.identity = mock.patch.object(module.os, 'geteuid', return_value=10001)
        self.identity.start()
        self.addCleanup(self.identity.stop)
        real_fstat, real_lstat = os.fstat, Path.lstat

        def with_uid(metadata):
            values = list(metadata)
            values[4] = 10001
            return os.stat_result(values)

        if uid != 10001:
            fstat_patch = mock.patch.object(module.os, 'fstat', side_effect=lambda fd: with_uid(real_fstat(fd)))
            lstat_patch = mock.patch.object(Path, 'lstat', lambda path: with_uid(real_lstat(path)))
            fstat_patch.start()
            lstat_patch.start()
            self.addCleanup(fstat_patch.stop)
            self.addCleanup(lstat_patch.stop)

    def handoff(self, once=True, output=None):
        output = Terminal() if output is None else output
        module.handoff(self.directory, self.access, once, output)
        return output.getvalue()

    def test_once_then_explicit_show_and_marker_has_no_credentials(self):
        self.assertIn(self.credentials['password'], self.handoff())
        self.assertEqual(self.handoff(), '')
        self.assertIn(self.credentials['password'], self.handoff(once=False))
        self.assertEqual(self.marker.read_text(), 'displayed\n')
        self.assertEqual(stat.S_IMODE(self.marker.stat().st_mode), 0o600)
        self.assertEqual(json.loads(self.enrollment.read_text()), self.credentials)
        self.assertEqual(set(path.name for path in self.directory.iterdir()),
                         {'access.json', 'emergency-enrollment.json', '.emergency-displayed'})

    def test_non_tty_and_flush_failure_leave_retryable_handoff(self):
        with self.assertRaises(module.DisplayError):
            self.handoff(output=io.StringIO())
        self.assertFalse(self.marker.exists())
        output = Terminal()
        output.flush = mock.Mock(side_effect=BrokenPipeError)
        with self.assertRaises(BrokenPipeError):
            self.handoff(output=output)
        self.assertFalse(self.marker.exists())
        self.assertIn(self.credentials['password'], self.handoff())

    def test_reject_unsafe_enrollment_and_marker(self):
        self.enrollment.chmod(0o644)
        with self.assertRaises(module.DisplayError):
            self.handoff()
        self.assertFalse(self.marker.exists())
        self.enrollment.chmod(0o600)
        link = self.directory / 'hardlink'
        os.link(self.enrollment, link)
        with self.assertRaises(module.DisplayError):
            self.handoff()
        link.unlink()
        self.handoff()
        self.marker.unlink()
        self.marker.symlink_to(self.enrollment)
        with self.assertRaises(OSError):
            self.handoff()
        self.assertEqual(json.loads(self.enrollment.read_text()), self.credentials)

    def test_missing_or_malformed_seed_has_no_output_or_marker(self):
        for content in ('', '{', '{}'):
            self.enrollment.write_text(content)
            output = Terminal()
            with self.assertRaises((module.DisplayError, json.JSONDecodeError)):
                self.handoff(output=output)
            self.assertEqual(output.getvalue(), '')
            self.assertFalse(self.marker.exists())
        self.enrollment.unlink()
        with self.assertRaises(FileNotFoundError):
            self.handoff()
        self.assertFalse(self.marker.exists())

    def test_wrong_identity_and_public_directory_are_rejected(self):
        with mock.patch.object(module.os, 'geteuid', return_value=65534):
            with self.assertRaises(module.DisplayError):
                self.handoff()
        self.directory.chmod(0o777)
        with self.assertRaises(module.DisplayError):
            self.handoff()
        self.assertFalse(self.marker.exists())

    def test_concurrent_first_starts_display_exactly_once(self):
        from concurrent.futures import ThreadPoolExecutor
        with ThreadPoolExecutor(max_workers=2) as executor:
            outputs = list(executor.map(lambda _: self.handoff(), range(2)))
        self.assertEqual(sum(self.credentials['password'] in output for output in outputs), 1)
        self.assertEqual(self.marker.read_text(), 'displayed\n')


@unittest.skipUnless(os.name == 'posix', 'requires a native POSIX pseudo-terminal')
class ShellTerminalTests(unittest.TestCase):
    def test_real_tty_cli_and_startup(self):
        import pty
        import select
        import time
        master, slave = pty.openpty()
        process = subprocess.Popen(['sh', str(ROOT / 'test/emergency.sh')], stdout=slave, stderr=slave)
        os.close(slave)
        output = bytearray()
        try:
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if select.select([master], [], [], .1)[0]:
                    try:
                        block = os.read(master, 65536)
                    except OSError:
                        break
                    if not block:
                        break
                    output.extend(block)
                elif process.poll() is not None:
                    break
            self.assertEqual(process.wait(timeout=2), 0, output.decode(errors='replace'))
            self.assertIn(b'PASS emergency TTY helper boundary and exit propagation', output)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)


if __name__ == '__main__':
    unittest.main()
