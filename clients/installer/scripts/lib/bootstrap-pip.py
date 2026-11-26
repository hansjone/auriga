"""Materialize the verified pip wheel into the user's venv, without ensurepip."""
import hashlib
import pathlib
import shutil
import sys
import tempfile
import zipfile

wheel, environment, digest = sys.argv[1:]
wheel = pathlib.Path(wheel)
environment = pathlib.Path(environment)
if hashlib.sha256(wheel.read_bytes()).hexdigest() != digest:
    raise ValueError("pip wheel digest mismatch")
# Windows venv: Lib/site-packages; POSIX venv: lib/pythonX.Y/site-packages.
if sys.platform == "win32":
    site = environment / "Lib" / "site-packages"
else:
    major, minor = sys.version_info[:2]
    site = environment / "lib" / f"python{major}.{minor}" / "site-packages"
with tempfile.TemporaryDirectory(prefix="pip-bootstrap-", dir=environment) as temporary:
    staging = pathlib.Path(temporary)
    with zipfile.ZipFile(wheel) as archive:
        for entry in archive.infolist():
            path = pathlib.PurePosixPath(entry.filename)
            if path.is_absolute() or ".." in path.parts or "\\" in entry.filename or ":" in entry.filename:
                raise ValueError("unsafe pip wheel path")
            if path.parts[0] != "pip" and not path.parts[0].startswith("pip-"):
                raise ValueError("unexpected pip wheel content")
        archive.extractall(staging)
    site.mkdir(parents=True, exist_ok=True)
    for path in staging.iterdir():
        target = site / path.name
        if target.is_symlink() or target.is_junction():
            raise ValueError("linked pip directory")
        if target.exists():
            shutil.rmtree(target)
        shutil.move(str(path), target)
