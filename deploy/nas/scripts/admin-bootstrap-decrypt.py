"""Decrypt NAS bootstrap checkpoints on the operator computer; never prints secrets."""
import argparse
import csv
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess

from cryptography.hazmat.primitives import serialization


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', required=True)
    parser.add_argument('--private-key', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--allow-partial', action='store_true')
    args = parser.parse_args()
    spec = importlib.util.spec_from_file_location('bootstrap', Path(__file__).with_name('admin-bootstrap.py'))
    bootstrap = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(bootstrap)
    private = serialization.load_pem_private_key(Path(args.private_key).read_bytes(), password=None)
    raw = Path(args.input).read_text(encoding='utf-8-sig').replace('\r', '').replace('\n', '')
    candidates = re.findall(r'AURIGA_BOOTSTRAP_ENVELOPE\s+(\{[^{}]*\})', raw)
    if not candidates:
        raise ValueError('no encrypted checkpoint found')
    payload = bootstrap.unseal(json.loads(candidates[-1]), private)
    if payload.get('stage') != 'complete' and not args.allow_partial:
        raise ValueError('bootstrap incomplete; use --allow-partial to recover before retrying')
    # The parent must already be operator-private. Exclusive create prevents
    # overwrite/link attacks; Windows restricts the empty leaf before any write.
    destination = Path(args.output).absolute()
    if destination.is_symlink() or destination.exists() or destination.parent.resolve() != destination.parent:
        raise ValueError('output must be a new file under a canonical private parent')
    descriptor = os.open(destination, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    try:
        if os.name == 'nt':
            row = next(csv.reader(subprocess.check_output(['whoami', '/user', '/fo', 'csv', '/nh'], text=True).splitlines()))
            sid = row[1]
            if not re.fullmatch(r'S-\d+(?:-\d+)+', sid):
                raise ValueError('cannot identify operator SID')
            subprocess.run(['icacls', str(destination), '/inheritance:r', '/grant:r', '*' + sid + ':(F)'],
                           check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        with os.fdopen(descriptor, 'w', encoding='utf-8') as stream:
            descriptor = None
            json.dump(payload, stream, indent=2)
            stream.write('\n')
    finally:
        if descriptor is not None:
            os.close(descriptor)
            destination.unlink()
    print('Private bootstrap handoff written; stage=' + payload['stage'])


if __name__ == '__main__':
    main()
