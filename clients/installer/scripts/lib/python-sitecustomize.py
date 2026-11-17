"""The managed venv shares office packages, with the packaged versions first."""
import pathlib
import sys

if sys.prefix != sys.base_prefix and (pathlib.Path(sys.prefix) / "auriga-python.json").is_file():
    import site

    bundled = site.getsitepackages([sys.base_prefix])
    personal = site.getsitepackages([sys.prefix])
    sites = {str(pathlib.Path(p).resolve()).casefold() for p in bundled + personal}
    ordinary = [p for p in sys.path if str(pathlib.Path(p).resolve()).casefold() not in sites]
    sys.path[:] = ordinary + [p for p in bundled + personal if pathlib.Path(p).is_dir()]
