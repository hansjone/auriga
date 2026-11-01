#!/usr/bin/env python3
"""Offline weekly signer. No NAS connection and no private key copying."""
import argparse
import base64
import calendar
from decimal import Decimal
import hashlib
import json
import os
import re
from pathlib import Path
import stat
import subprocess
import tempfile
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
    if not isinstance(value, str):
        raise ValueError("UTC timestamp required")
    match = re.fullmatch(r"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z", value)
    if not match:
        raise ValueError("UTC timestamp required")
    whole = calendar.timegm(datetime.fromisoformat(match[1]).timetuple())
    # Do not truncate nanos through datetime/float: seven days plus 1ns is
    # still over the offline signing limit, just as it is in Java Instant.
    return Decimal(whole) + Decimal("0." + (match[2] or "0"))


def decode_json(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate JSON field refused")
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=unique)


def review_transition(old, new):
    """Review NAS proposals without granting them authority over the offline root."""
    fields = {"kid", "alg", "purpose", "state", "public_key_spki", "not_before", "not_after"}
    purposes = {"business_delivery", "protocol_response", "management_state"}
    if (set(new) != {"schema_version", "realm", "keyset_version", "issued_at", "valid_until", "keys"}
            or type(new["schema_version"]) is not int or new["schema_version"] != 1 or new["realm"] != old["realm"]
            or int(new["keyset_version"]) <= int(old["keyset_version"])
            or timestamp(new["issued_at"]) <= timestamp(old["issued_at"])):
        raise ValueError("keyset transition refused")
    previous = {k["kid"]: k for k in old["keys"]}
    if len(previous) != len(old["keys"]) or not 1 <= len(new["keys"]) <= 128:
        raise ValueError("key history refused")
    entries, material, active = {}, set(), set()
    changes, added = [], set()
    issued = timestamp(new["issued_at"])
    for key in new["keys"]:
        if (set(key) != fields or any(not isinstance(v, str) for v in key.values())
                or key["kid"] in entries or key["alg"] != "Ed25519"
                or key["purpose"] not in purposes or key["state"] not in {"active", "next", "retired", "revoked"}):
            raise ValueError("key shape/algorithm refused")
        raw = base64.urlsafe_b64decode(key["public_key_spki"] + "=")
        if (len(raw) != 44 or raw[:12] != bytes.fromhex("302a300506032b6570032100")
                or base64.urlsafe_b64encode(raw).decode().rstrip("=") != key["public_key_spki"]
                or key["public_key_spki"] in material):
            raise ValueError("key material refused")
        material.add(key["public_key_spki"])
        entries[key["kid"]] = key
        before, after = timestamp(key["not_before"]), timestamp(key["not_after"])
        if after <= before:
            raise ValueError("key interval refused")
        if key["state"] == "active":
            if key["purpose"] in active or not before <= issued < after:
                raise ValueError("active key refused")
            active.add(key["purpose"])
        prior = previous.get(key["kid"])
        if prior:
            if any(key[f] != prior[f] for f in fields - {"state"}):
                raise ValueError("old key changed")
            if (key["state"] != prior["state"] and key["state"] != "revoked"
                    and (prior["state"], key["state"]) not in {("next", "active"), ("active", "retired")}):
                raise ValueError("key state transition refused")
            continue
        purpose = key["purpose"]
        old_active = [k for k in old["keys"] if k["purpose"] == purpose and k["state"] == "active"]
        if (purpose in added or key["state"] != "next"
                or not re.fullmatch("nas-" + purpose.replace("_", "-") + "-[0-9a-f]{24}", key["kid"])
                or before != issued or after - before > 7 * 86400 or after > issued + 7 * 86400
                or len(old_active) != 1 or timestamp(old_active[0]["not_after"]) - issued > 48 * 3600
                or any(k["purpose"] == purpose and k["state"] == "next" for k in old["keys"])):
            raise ValueError("new key policy refused")
        added.add(purpose)
        changes.append({"kid": key["kid"], "purpose": purpose, "spkiFingerprint": "sha256:" + digest(raw),
                        "notBefore": key["not_before"], "notAfter": key["not_after"]})
    if not previous.keys() <= entries.keys():
        raise ValueError("old key missing")
    if active != {k["purpose"] for k in old["keys"] if k["state"] == "active"}:
        raise ValueError("active purpose missing")
    return changes


def private_directory(path):
    """CREATE_NEW with an owner-only inheritable DACL, from the first instant.

    Python 3.13+ mkdir(0700) on Windows grants OWNER RIGHTS instead of the
    concrete owner principal. The production Java checker deliberately requires
    concrete trusted principals. Do not alter its protection rules or the root.
    """
    if os.name != "nt":
        path.mkdir(mode=0o700)
        return
    import ctypes
    from ctypes import wintypes

    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    advapi = ctypes.WinDLL("advapi32", use_last_error=True)
    kernel.GetCurrentProcess.restype = wintypes.HANDLE
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.LocalFree.argtypes = [ctypes.c_void_p]
    advapi.OpenProcessToken.argtypes = [wintypes.HANDLE, wintypes.DWORD, ctypes.POINTER(wintypes.HANDLE)]
    advapi.GetTokenInformation.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p,
                                          wintypes.DWORD, ctypes.POINTER(wintypes.DWORD)]
    advapi.ConvertSidToStringSidW.argtypes = [ctypes.c_void_p, ctypes.POINTER(wintypes.LPWSTR)]
    advapi.ConvertStringSecurityDescriptorToSecurityDescriptorW.argtypes = [
        wintypes.LPCWSTR, wintypes.DWORD, ctypes.POINTER(ctypes.c_void_p), ctypes.c_void_p]

    class SecurityAttributes(ctypes.Structure):
        _fields_ = [("length", wintypes.DWORD), ("descriptor", ctypes.c_void_p),
                    ("inherit_handle", wintypes.BOOL)]

    kernel.CreateDirectoryW.argtypes = [wintypes.LPCWSTR, ctypes.POINTER(SecurityAttributes)]
    token = wintypes.HANDLE()
    sid_string = wintypes.LPWSTR()
    descriptor = ctypes.c_void_p()
    try:
        if not advapi.OpenProcessToken(kernel.GetCurrentProcess(), 0x0008, ctypes.byref(token)):
            raise ctypes.WinError(ctypes.get_last_error())
        size = wintypes.DWORD()
        advapi.GetTokenInformation(token, 1, None, 0, ctypes.byref(size))  # TokenUser
        if ctypes.get_last_error() != 122:  # ERROR_INSUFFICIENT_BUFFER
            raise ctypes.WinError(ctypes.get_last_error())
        info = ctypes.create_string_buffer(size.value)
        if not advapi.GetTokenInformation(token, 1, info, size, ctypes.byref(size)):
            raise ctypes.WinError(ctypes.get_last_error())
        sid = ctypes.cast(info, ctypes.POINTER(ctypes.c_void_p))[0]
        if not advapi.ConvertSidToStringSidW(sid, ctypes.byref(sid_string)):
            raise ctypes.WinError(ctypes.get_last_error())
        # Protected DACL: no inherited grants, including OWNER RIGHTS/Users.
        sddl = f"O:{sid_string.value}D:P(A;OICI;FA;;;{sid_string.value})"
        if not advapi.ConvertStringSecurityDescriptorToSecurityDescriptorW(
                sddl, 1, ctypes.byref(descriptor), None):
            raise ctypes.WinError(ctypes.get_last_error())
        attributes = SecurityAttributes(ctypes.sizeof(SecurityAttributes), descriptor, False)
        if not kernel.CreateDirectoryW(str(path), ctypes.byref(attributes)):
            raise ctypes.WinError(ctypes.get_last_error())
    finally:
        if descriptor:
            kernel.LocalFree(descriptor)
        if sid_string:
            kernel.LocalFree(ctypes.cast(sid_string, ctypes.c_void_p))
        if token:
            kernel.CloseHandle(token)


def sign(args):
    os.umask(0o077)
    source = Path(args.request).absolute()
    batch_raw = read(source / "batch.json")
    batch = decode_json(batch_raw)
    entries = batch["entries"]
    if (not 1 <= len(entries) <= 28 or digest(read(args.jar)) != args.jar_sha256
            or not re.fullmatch(r"[0-9a-f]{64}", args.expected_nas_jar_sha256)):
        raise ValueError("queue count or independently approved jar digest refused")
    first = timestamp(entries[0]["issuedAt"])
    if first % 43200 or not time.time() - 300 <= first <= time.time() + 86400:
        raise ValueError("first issue outside offline signing window; prepare a fresh batch")
    base_files = {name: read(source / "base" / name) for name in ("signed.json", "announcement.json", "root.der")}
    if (batch["baseFiles"] != {name: digest(raw) for name, raw in base_files.items()}
            or "sha256:" + digest(base_files["root.der"]) != args.root_fingerprint
            or digest(base_files["signed.json"]) != batch["baseSignedSha256"]):
        raise ValueError("independent base root pin or digest refused")
    base = decode_json(base_files["signed.json"])["keyset"]
    if base["keyset_version"] != batch["baseVersion"]:
        raise ValueError("base version differs")
    # Public verification only: production Java authenticates the archived base,
    # including both signatures and the independent root pin. No private root read.
    with tempfile.TemporaryDirectory(prefix="auriga-weekly-public-") as temp:
        verified = Path(temp) / "verified"
        private_directory(verified)
        for name, raw in base_files.items():
            (verified / name).write_bytes(raw)
        loader = verified / "empty-loader.properties"
        loader.write_bytes(b"")
        verify = [args.java, "-Dloader.main=auriga.collab.network.keys.OfflineKeySigning",
                  "-Dloader.config.location=" + loader.as_uri(),
                  "-cp", str(Path(args.jar).absolute()), "org.springframework.boot.loader.launch.PropertiesLauncher",
                  "verify-archived", str(verified / "signed.json"), str(verified / "announcement.json"),
                  args.root_kid, str(verified / "root.der"), args.root_fingerprint]
        result = subprocess.run(verify, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if result.returncode:
        raise ValueError("production archived base verification refused")
    # Validate the complete exported plan before touching the offline root.
    material = []
    previous = 0
    changes = []
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
        trust = decode_json(files["trust.json"])
        payload = decode_json(files["payload.json"])
        if (trust["rootKid"] != args.root_kid or trust["rootFingerprint"] != args.root_fingerprint
                or trust["operatorJarSha256"] != args.expected_nas_jar_sha256 or trust["publicationMode"] != "regular"
                or payload["keyset_version"] != version or payload["issued_at"] != entry["issuedAt"]
                or payload["valid_until"] != entry["validUntil"] or payload["realm"] != trust["realm"]):
            raise ValueError("independent root pin or payload binding differs")
        changes.extend({"version": version, **c} for c in review_transition(base, payload))
        root_spki = base64.urlsafe_b64encode(base_files["root.der"]).decode().rstrip("=")
        if any(k["public_key_spki"] == root_spki for k in payload["keys"]):
            raise ValueError("root reused as business key")
        base = payload
        material.append((version, files))
    batch_hash = digest(batch_raw)
    print(json.dumps({"status": "review_required", "batchSha256": batch_hash,
                      "signingJarSha256": args.jar_sha256, "nasJarSha256": args.expected_nas_jar_sha256,
                      "instruction": "Run --review-only and review this exact plan before confirming its digest.",
                      "newKeys": changes}, ensure_ascii=False), flush=True)
    if getattr(args, "review_only", False):
        return
    confirmation = getattr(args, "confirm_batch_sha256", None)
    if confirmation is None:
        confirmation = input("Review the new keys above. Enter the exact batch SHA256 to authorize signing: ").strip()
    if confirmation != batch_hash:
        raise ValueError("operator did not confirm this exact batch")
    out = Path(args.output).absolute()
    private_directory(out)  # CREATE_NEW: never re-sign a distributed version.
    (out / "batch.json").write_bytes(batch_raw)
    private_directory(out / "base")
    for name, raw in base_files.items():
        (out / "base" / name).write_bytes(raw)
    loader = out / "empty-loader.properties"
    loader.write_bytes(b"")
    for version, files in material:
        dest = out / version
        private_directory(dest)
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
        audit = {"approvedBy": args.approved_by,
                 "reason": "weekly pre-signed queue; original root; scheduled publication; signing jar sha256:"
                           + args.jar_sha256 + "; NAS jar sha256:" + args.expected_nas_jar_sha256,
                 "payloadSha256": digest(files["payload.json"]),
                 "signedSha256": digest(read(dest / "signed.json")),
                 "announcementSha256": digest(read(dest / "announcement.json"))}
        (dest / "approval.json").write_text(json.dumps(audit, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")
    loader.unlink()
    print(json.dumps({"status": "weekly_queue_signed", "count": len(entries), "coverageUntil": entries[-1]["validUntil"]}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("request", "jar", "jar-sha256", "expected-nas-jar-sha256", "root-kid", "root-fingerprint", "root-private", "approved-by", "output"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--java", default="java")
    parser.add_argument("--review-only", action="store_true")
    parser.add_argument("--confirm-batch-sha256", help="Explicit approval of the SHA256 printed by --review-only")
    try:
        sign(parser.parse_args())
    except (ValueError, OSError, KeyError, TypeError, EOFError) as error:
        parser.exit(1, "weekly signing refused: " + str(error) + "; verify the public plan and pins; preserve partial output\n")
