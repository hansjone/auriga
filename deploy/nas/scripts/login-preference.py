#!/usr/bin/env python3
"""Narrow, offline upgrade of known public recipes; never invokes Docker or IAM."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import stat
import tempfile

KEY = "AURIGA_EDGE_LOGIN_PREFERENCE"
MAPPING = "      " + KEY + ": ${" + KEY + ":-company}\n"
# Public docker-compose.yml blobs, not deployment-specific identities or secrets.
SUPPORTED = {
    "5ac935b3b8f144ed1229dd797baf267f00c1bf83db4347844c73212774b4235e",  # 8be9394
    "fc5eeb2c731d8dc5c05c12f06cd22dcc5fc95e88faf1c6eab5a116a68ed16cd8",  # de13e8f8
}


def compose_projection(raw):
    if b"\r\n" in raw and b"\n" in raw.replace(b"\r\n", b""):
        raise ValueError("mixed recipe line endings require review")
    text = raw.decode("utf-8").replace("\r\n", "\n")
    if text.count("\n  edge:\n") != 1 or text.count("\nservices:\n") != 1:
        raise ValueError("expected one public services/edge mapping")
    start = text.index("\n  edge:\n")
    # Search after the edge service name, not at its own header.
    body_start = start + len("\n  edge:\n")
    following = re.search(r"\n  [A-Za-z0-9_-]+:\n|\n[^ #\n][^\n]*:\n", text[body_start:])
    end = body_start + following.start() if following else len(text)
    block = text[start:end]
    if block.count("\n    environment:\n") != 1:
        raise ValueError("expected one edge environment mapping")
    existing = [line for line in text.splitlines(True) if re.match(r"\s*" + KEY + r"\s*:", line)]
    if existing and (existing != [MAPPING] or MAPPING not in block):
        raise ValueError("ambiguous or customized login preference mapping")
    original = text.replace(MAPPING, "")
    if hashlib.sha256(original.encode()).hexdigest() not in SUPPORTED:
        raise ValueError("unsupported recipe content; review its public recipe upgrade first")
    anchor = "\n    environment:\n"
    patched = block if existing else block.replace(anchor, anchor + MAPPING, 1)
    result = text[:start] + patched + text[end:]
    return result.replace("\n", "\r\n").encode() if b"\r\n" in raw else result.encode()


def stack_projection(raw, preference):
    lines = raw.decode("utf-8").splitlines(True)
    selected = [i for i, line in enumerate(lines) if line.startswith(KEY + "=")]
    if len(selected) > 1:
        raise ValueError("duplicate login preference in stack.env")
    newline = "\r\n" if b"\r\n" in raw else "\n"
    value = KEY + "=" + preference + newline
    if selected:
        lines[selected[0]] = value
    else:
        if lines and not lines[-1].endswith(("\n", "\r")):
            lines[-1] += newline
        lines.append(value)
    return "".join(lines).encode()


def regular(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise ValueError("use canonical absolute file paths without symlinks")
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError("input must be a regular file with one link")
    return path, info, path.read_bytes()


def replace(path, info, before, after):
    if before == after:
        return
    digest = hashlib.sha256(before).hexdigest()
    backup = path.with_name(path.name + ".login-preference-" + digest[:16] + ".bak")
    try:
        descriptor = os.open(backup, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        _, _, saved = regular(str(backup))
        if saved != before:
            raise ValueError("backup content mismatch")
    else:
        with os.fdopen(descriptor, "wb") as output:
            output.write(before)
    descriptor, temporary = tempfile.mkstemp(prefix=".login-preference-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(after)
            output.flush()
            os.fsync(output.fileno())
            os.fchmod(output.fileno(), stat.S_IMODE(info.st_mode))
            if hasattr(os, "fchown"):
                os.fchown(output.fileno(), info.st_uid, info.st_gid)
        current = path.lstat()
        if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino) or path.read_bytes() != before:
            raise ValueError("input changed during migration")
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    print("updated " + str(path) + "; backup " + str(backup))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compose", required=True)
    parser.add_argument("--stack-env", required=True)
    parser.add_argument("--preference", choices=["company", "team"], required=True)
    parser.add_argument("--check", action="store_true", help="validate and show whether a change is needed")
    args = parser.parse_args()
    compose, compose_info, compose_raw = regular(args.compose)
    stack, stack_info, stack_raw = regular(args.stack_env)
    if compose == stack:
        raise ValueError("compose and stack.env must be different files")
    compose_new = compose_projection(compose_raw)
    stack_new = stack_projection(stack_raw, args.preference)
    if args.check:
        print("valid; " + ("change required" if (compose_new, stack_new) != (compose_raw, stack_raw) else "already configured"))
        return
    # Both inputs validate before either changes; no runtime is restarted here.
    replace(compose, compose_info, compose_raw, compose_new)
    replace(stack, stack_info, stack_raw, stack_new)
    print("login preference configured; recreate edge with the existing public start/compose workflow")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, UnicodeError) as error:
        raise SystemExit("login preference migration refused: " + str(error)) from None
