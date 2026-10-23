"""Small Docker-save fixtures for loader unit tests; no real Engine is used."""
import hashlib
import io
import json
import pathlib
import shutil
import sys
import tarfile

root = pathlib.Path(sys.argv[1])
binary = pathlib.Path(sys.argv[2])
(root / "images").mkdir(parents=True)
(root / "deploy-nas/bin").mkdir(parents=True)
shutil.copyfile(binary, root / "deploy-nas/bin/auriga-nas-bootstrap")
(root / "deploy-nas/bin/auriga-nas-bootstrap").chmod(0o755)
(root / "deploy-nas/bin/runtime-release").write_bytes(b"unit-test tool only\n")
(root / "versions.txt").write_text("unit-test version\n")


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


images = {}
for name in ("platform", "edge", "agent"):
    layer = ("unit test layer " + name).encode()
    config = json.dumps({"os": "linux", "architecture": "amd64", "rootfs": {
        "type": "layers", "diff_ids": ["sha256:" + digest(layer)]}}).encode()
    manifest = json.dumps([{"Config": "config.json", "RepoTags": [f"auriga/{name}:nas"],
                            "Layers": ["layer.tar"]}]).encode()
    with tarfile.open(root / f"images/{name}.tar", "w", format=tarfile.USTAR_FORMAT) as archive:
        for filename, raw in (("config.json", config), ("layer.tar", layer), ("manifest.json", manifest)):
            info = tarfile.TarInfo(filename)
            info.size = len(raw)
            archive.addfile(info, io.BytesIO(raw))
    images[name] = {"id": "sha256:" + digest(config), "tar": name + ".tar",
                    "config_id": "sha256:" + digest(config), "os": "linux", "arch": "amd64"}
tools = {"bootstrap": {"file": "deploy-nas/bin/auriga-nas-bootstrap"},
         "runtime_release": {"file": "deploy-nas/bin/runtime-release", "os": "linux", "arch": "amd64"}}
for tool in tools.values():
    tool["sha256"] = digest((root / tool["file"]).read_bytes())
(root / "images/manifest.json").write_text(json.dumps({"commit": "a" * 40,
    "source_tree": "b" * 40, "images": images, **tools}))
(root / "images/SHA256SUMS").write_text("".join(
    f"{digest((root / 'images' / (name + '.tar')).read_bytes())}  ./{name}.tar\n" for name in images))
(root / "SHA256SUMS").write_text("".join(
    f"{digest((root / file).read_bytes())}  {file}\n" for file in (
        "deploy-nas/bin/auriga-nas-bootstrap", "deploy-nas/bin/runtime-release", "images/manifest.json", "versions.txt")))
