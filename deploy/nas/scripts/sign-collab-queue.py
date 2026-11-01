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
    private_directory(out)  # CREATE_NEW: never re-sign a distributed version.
    (out / "batch.json").write_bytes(batch_raw)
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
