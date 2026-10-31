"""TTY-only recovery handoff. Runs in a log-disabled, networkless fixed-UID helper."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import sys
from urllib.parse import quote, urlencode


class DisplayError(Exception):
    """Only fixed diagnostic messages may leave this helper."""


def display(access, credentials, output):
    if not output.isatty():
        raise DisplayError('stdout must be a terminal; run emergency.sh show in a private TTY')
    origin = access.get('adminOrigin', {})
    scheme, host, port = (origin.get(key) for key in ('scheme', 'host', 'port'))
    if (scheme not in ('http', 'https') or not isinstance(host, str)
            or not re.fullmatch(r'[a-z0-9.-]{1,253}', host)
            or type(port) is not int or not 0 < port < 65536):
        raise DisplayError('invalid installed admin origin')
    for key in ('username', 'password', 'totpSecret'):
        value = credentials.get(key)
        if not isinstance(value, str) or not re.fullmatch(r'[\x21-\x7e]{1,256}', value):
            raise DisplayError('invalid enrollment material')
    if (len(credentials['password']) < 16
            or not re.fullmatch(r'[A-Z2-7]{16,128}', credentials['totpSecret'])):
        raise DisplayError('invalid enrollment material')
    username = credentials['username']
    uri = 'otpauth://totp/' + quote('Auriga:' + username, safe='') + '?' + urlencode({
        'secret': credentials['totpSecret'], 'issuer': 'Auriga',
        'algorithm': 'SHA1', 'digits': 6, 'period': 30,
    })
    # Construct and validate everything before the first byte of sensitive output.
    text = (
        '\nAuriga 应急账号（请在私密终端查看并妥善保管）\n'
        f'管理台：{scheme}://{host}:{port}\n'
        f'用户名：{username}\n'
        f'口令：{credentials["password"]}\n'
        f'TOTP 密钥：{credentials["totpSecret"]}\n'
        'otpauth URI（可导入验证器；此 helper 不依赖二维码库）：\n'
        f'{uri}\n'
        '下一步：用它登录管理台 → 成员 → 邀请。\n'
        '应急登录仍限真实回环来源；远程浏览器直接登录会被拒绝。\n'
        '请使用已受信的回环访问通道；没有该通道时可选 admin-bootstrap 自动化。\n'
    )
    output.write(text)
    output.flush()


def private_file(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        metadata = os.fstat(fd)
        if (not stat.S_ISREG(metadata.st_mode) or stat.S_IMODE(metadata.st_mode) != 0o600
                or metadata.st_uid != os.geteuid() or metadata.st_nlink != 1
                or metadata.st_size > 16384):
            raise DisplayError('invalid private enrollment or display marker metadata')
        with os.fdopen(fd, encoding='utf-8') as stream:
            fd = None
            return stream.read(16385)
    finally:
        if fd is not None:
            os.close(fd)


def handoff(directory, access_file, once, output):
    # Check again inside Docker: a host check alone cannot protect direct invocation.
    if not output.isatty():
        raise DisplayError('stdout must be a terminal; run emergency.sh show in a private TTY')
    if os.geteuid() != 10001:
        raise DisplayError('helper must run as the fixed enrollment identity')
    metadata = directory.lstat()
    if (not stat.S_ISDIR(metadata.st_mode) or metadata.st_uid != os.geteuid()
            or stat.S_IMODE(metadata.st_mode) & 0o027):
        raise DisplayError('invalid enrollment directory metadata')
    import fcntl
    # Lock the directory itself, so concurrent starts cannot both display and no
    # new lock file is needed. All operators/helpers use the same host inode.
    fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX)
        marker = directory / '.emergency-displayed'
        exists = os.path.lexists(marker)
        if exists:
            if private_file(marker) != 'displayed\n':
                raise DisplayError('invalid display marker')
            if once:
                return
        enrollment = json.loads(private_file(directory / 'emergency-enrollment.json'))
        access = json.loads(access_file.read_text(encoding='utf-8'))
        display(access, enrollment, output)
        # Flush the terminal before committing the non-secret receipt. A failure
        # leaves the handoff retryable; no credential is written to a new file.
        if not exists:
            receipt = os.open(marker, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(receipt, 'w', encoding='ascii') as stream:
                stream.write('displayed\n')
                stream.flush()
                os.fsync(stream.fileno())
            os.fsync(fd)
    finally:
        os.close(fd)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--once', action='store_true')
    args = parser.parse_args()
    try:
        handoff(Path('/enrollment'), Path('/access.json'), args.once, sys.stdout)
    except DisplayError as error:
        print('emergency display refused: ' + str(error), file=sys.stderr)
        return 1
    except Exception:
        # Never let malformed secret JSON, filenames or exceptions echo content.
        print('emergency display failed: check seed completion and private material metadata; retry emergency.sh show', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
