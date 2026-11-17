"""Offline wheel materialization. Invoked with the pinned Python using -I -S.

Only library/data files are copied: no pip, dependency resolution, entry-point
generation, bytecode compilation, package import, or installation scripts.
"""
import email.parser
import hashlib
import json
import pathlib
import re
import stat
import sys
import zipfile


def normalize(name):
    return re.sub(r"[-_.]+", "_", name.lower())


def install(lock, wheels, runtime):
    site = runtime / "Lib" / "site-packages"
    site.mkdir(parents=True, exist_ok=True)
    result = []
    written = set()
    for row in lock["wheels"]:
        path = wheels / row["filename"]
        if path.stat().st_size != row["bytes"] or hashlib.sha256(path.read_bytes()).hexdigest() != row["sha256"]:
            raise ValueError("wheel digest mismatch: " + row["filename"])
        info_root = normalize(row["name"]) + "-" + row["version"] + ".dist-info/"
        licenses = []
        omitted_scripts = []
        with zipfile.ZipFile(path) as archive:
            metadata = email.parser.Parser().parsestr(archive.read(info_root + "METADATA").decode("utf-8"))
            if normalize(metadata["Name"]) != normalize(row["name"]) or metadata["Version"] != row["version"]:
                raise ValueError("wheel metadata differs from pin")
            for entry in archive.infolist():
                name = entry.filename
                parts = name.rstrip("/").split("/")
                if (not all(parts) or any(p in (".", "..") or re.search(r'[\\:<>"|?*\x00-\x1f]', p) or p.endswith((".", " ")) for p in parts)
                        or name.startswith("/") or stat.S_ISLNK(entry.external_attr >> 16)):
                    raise ValueError("unsafe wheel path: " + name)
                if entry.is_dir():
                    continue
                # XlsxWriter carries optional CLI utilities. Do not install or execute them.
                if parts[0] == info_root.removesuffix(".dist-info/") + ".data" and len(parts) > 2 and parts[1] == "scripts":
                    omitted_scripts.append(name)
                    continue
                # These library wheels require no .data relocation or startup hooks.
                if parts[0].endswith(".data") or name.lower().endswith(".pth") or pathlib.PurePosixPath(name).name in ("sitecustomize.py", "usercustomize.py"):
                    raise ValueError("unsupported wheel installation hook: " + name)
                key = name.casefold()
                dest = site.joinpath(*parts)
                if key in written or dest.exists():
                    raise ValueError("wheel file collision: " + name)
                written.add(key)
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_bytes(archive.read(entry))
                if name.startswith(info_root) and re.search(r"licen[cs]e|copying|notice|authors", name, re.I):
                    licenses.append("Lib/site-packages/" + name)
        if not licenses:
            raise ValueError("wheel license missing: " + row["filename"])
        result.append({"name": row["name"], "version": row["version"], "licenses": licenses, "omittedScripts": omitted_scripts})
    return result


if __name__ == "__main__":
    lock_path, wheels_path, runtime_path = map(pathlib.Path, sys.argv[1:])
    lock = json.loads(lock_path.read_text(encoding="utf-8"))
    if sys.version_info[:3] != tuple(map(int, lock["version"].split("."))):
        raise ValueError("Python version differs from pin")
    print(json.dumps(install(lock, wheels_path, runtime_path)))
