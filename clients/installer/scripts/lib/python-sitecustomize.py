"""The managed venv shares office packages, with the packaged versions first."""
import pathlib
import sys

if sys.prefix != sys.base_prefix and (pathlib.Path(sys.prefix) / "auriga-python.json").is_file():
    import site

    bundled = site.getsitepackages([sys.base_prefix])
    personal = site.getsitepackages([sys.prefix])
    sites = {str(pathlib.Path(p).resolve()).casefold() for p in bundled + personal}
    personal_root = pathlib.Path(sys.prefix).resolve()
    ordinary = []
    personal_paths = []
    for entry in sys.path:
        resolved = pathlib.Path(entry).resolve()
        if resolved.is_relative_to(personal_root):
            personal_paths.append(entry)
        elif str(resolved).casefold() not in sites:
            ordinary.append(entry)
    sys.path[:] = ordinary + [p for p in bundled if pathlib.Path(p).is_dir()] + personal_paths
