#!/usr/bin/env python3
"""Offline weekly signer. No NAS connection and no private key copying."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import time


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def read(path):
    path = Path(path).absolute()
    for p in (path, *path.parents):
        if p.is_symlink():
            raise ValueError("symlink input refused")
    st = path.stat()
    if not stat.S_ISREG(st.st_mode) or st.st_nlink != 1 or st.st_size > 256 * 1024 * 1024:
        raise ValueError("input metadata refused")
    if os.name != "nt" and st.st_mode & 0o022:
        raise ValueError("writable input refused")
    return path.read_bytes()


def timestamp(value):
    from datetime import datetime
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def sign(args):
    os.umask(0o077)
    source = Path(args.request).absolute()
    batch_raw = read(source / "batch.json")
    batch = json.loads(batch_raw)
    entries = batch["entries"]
    if not 1 <= len(entries) <= 28 or digest(read(args.jar)) != args.jar_sha256:
        raise ValueError("queue count or independently approved jar digest refused")
    first = timestamp(entries[0]["issuedAt"])
    if abs(time.time() - first) > 300:
        raise ValueError("first scheduled issue must be within five minutes; prepare a fresh batch")
    # Validate the complete exported plan before touching the offline root.
    material = []
    previous = 0
    for i, entry in enumerate(entries):
        version = entry["version"]
        if str(int(version)) != version or int(version) <= int(batch["baseVersion"]) or (i and int(version) != previous + 1):
            raise ValueError("queue version sequence refused")
        previous = int(version)
        issued = timestamp(entry["issuedAt"])
        if issued != first + i * 43200 or timestamp(entry["validUntil"]) != issued + 86400:
            raise ValueError("queue schedule refused")
        files = {name: read(source / version / name) for name in ("payload.json", "trust.json", "input.json")}
        if any(digest(raw) != entry["files"][name] for name, raw in files.items()):
            raise ValueError("request digest differs")
        trust = json.loads(files["trust.json"])
        payload = json.loads(files["payload.json"])
        if (trust["rootKid"] != args.root_kid or trust["rootFingerprint"] != args.root_fingerprint
                or trust["operatorJarSha256"] != args.jar_sha256 or trust["publicationMode"] != "regular"
                or payload["keyset_version"] != version or payload["issued_at"] != entry["issuedAt"]
                or payload["valid_until"] != entry["validUntil"] or payload["realm"] != trust["realm"]):
            raise ValueError("independent root pin or payload binding differs")
        material.append((version, files))
    out = Path(args.output).absolute()
    out.mkdir(mode=0o700)  # CREATE_NEW throughout: never re-sign a distributed version.
    (out / "batch.json").write_bytes(batch_raw)
    loader = out / "empty-loader.properties"
    loader.write_bytes(b"")
    for version, files in material:
        dest = out / version
        dest.mkdir(mode=0o700)
        for name, raw in files.items():
            (dest / name).write_bytes(raw)
        cmd = [args.java, "-Dloader.main=auriga.collab.network.keys.OfflineKeySigning",
               "-Dloader.config.location=" + loader.as_uri(), "-cp", str(Path(args.jar).absolute()),
               "org.springframework.boot.loader.launch.PropertiesLauncher", "sign",
               str(dest / "payload.json"), args.root_kid, str(Path(args.root_private).absolute()),
               str(dest / "signed.json"), str(dest / "announcement.json")]
        result = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        if result.returncode:
            raise ValueError("production offline signer rejected the operation; preserve partial output")
        audit = {"approvedBy": args.approved_by, "reason": "weekly pre-signed queue; original root; scheduled publication",
                 "payloadSha256": digest(files["payload.json"]),
                 "signedSha256": digest(read(dest / "signed.json")),
                 "announcementSha256": digest(read(dest / "announcement.json"))}
        (dest / "approval.json").write_text(json.dumps(audit, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")
    loader.unlink()
    print(json.dumps({"status": "weekly_queue_signed", "count": len(entries), "coverageUntil": entries[-1]["validUntil"]}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("request", "jar", "jar-sha256", "root-kid", "root-fingerprint", "root-private", "approved-by", "output"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--java", default="java")
    try:
        sign(parser.parse_args())
    except (ValueError, OSError, KeyError, TypeError):
        parser.exit(1, "weekly signing refused; verify pins/metadata; if older than five minutes, run queue-prepare again; preserve partial output\n")
