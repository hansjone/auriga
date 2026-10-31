"""Create independent UI/reader credentials; never inspect or rotate master."""
import os
from pathlib import Path
import re
import secrets
import stat
import sys


def prepare(root):
    metadata = root.lstat()
    if not stat.S_ISDIR(metadata.st_mode):
        raise RuntimeError('invalid UI secret directory')
    names = {'password', 'reader'}
    present = {entry.name for entry in root.iterdir()}
    if present:
        if present != names or (metadata.st_uid, metadata.st_gid, stat.S_IMODE(metadata.st_mode)) != (0, 0, 0o700):
            raise RuntimeError('incomplete UI secrets or directory metadata drift')
        for name in names:
            leaf = root / name
            info = leaf.lstat()
            if not stat.S_ISREG(info.st_mode) or (info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode), info.st_nlink) != (10001, 10004, 0o640, 1):
                raise RuntimeError('UI secret metadata drift')
            if not re.fullmatch(('sk-' if name == 'reader' else '') + '[0-9a-f]{64}', leaf.read_text()):
                raise RuntimeError('invalid UI credential')
        return
    os.chown(root, 0, 0)
    root.chmod(0o700)
    for name in sorted(names):
        descriptor = os.open(root / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(descriptor, 'w') as stream:
            stream.write(('sk-' if name == 'reader' else '') + secrets.token_hex(32))
            stream.flush()
            os.fchown(stream.fileno(), 10001, 10004)
            os.fchmod(stream.fileno(), 0o640)


if __name__ == '__main__':
    try:
        prepare(Path(sys.argv[1]))
    except Exception:
        raise SystemExit('UI secret preparation failed; inspect directory metadata without printing credentials') from None
