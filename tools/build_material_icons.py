"""Build the Material Design Icons Pencode extension.

Clones https://github.com/material-extensions/vscode-material-icon-theme into a
temporary directory (or reuses --source), reads the generated
dist/material-icons.json manifest, and writes the extension payload:

  resources/icons/<name>.svg   only the icons the rule table selects
  resources/rules.psv          peb icon rules embedded in the Go extension
  resources/LICENSE.txt        upstream MIT license

Upstream icons keep their design size as an intrinsic width/height pair or as a
viewBox. Pencode rasterizes SVG textures at their intrinsic size, so every icon
is rewritten to a fixed 16x16 intrinsic size; the viewBox and all drawing stay
untouched, which keeps rendering identical and bounds texture memory.
"""

import argparse
import json
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

REPOSITORY = "https://github.com/material-extensions/vscode-material-icon-theme.git"
ICON_SIZE = 16
# Unit separator keeps patterns, icon names, and empty default patterns distinct.
SEPARATOR = "\x1f"
DEFAULT_FILE = 0
FILE_NAME = 1
FILE_SUFFIX = 2
DEFAULT_FOLDER = 3
FOLDER_NAME = 4
ROOT_TAG = re.compile(rb"<svg\b[^>]*>")
ATTRIBUTE = re.compile(rb'\s([\w:.-]+)\s*=\s*"([^"]*)"')


def prepare(source: Path | None) -> Path:
    """Return an upstream checkout that has dist/material-icons.json."""
    if source is not None:
        return source
    temporary = Path(tempfile.mkdtemp(prefix="material-icon-theme-"))
    subprocess.run(["git", "clone", "--depth", "1", REPOSITORY, temporary], check=True)
    return temporary


def build(source: Path) -> None:
    """Generate the manifest and the icon clones upstream ships in its package."""
    subprocess.run(["npm", "install", "--no-audit", "--no-fund"], cwd=source, check=True)
    subprocess.run(["npm", "run", "compile"], cwd=source, check=True)


def normalize(source: bytes) -> bytes:
    """Pin the intrinsic size to ICON_SIZE without touching the viewBox."""
    match = ROOT_TAG.search(source)
    if match is None:
        raise ValueError("missing <svg> root element")
    attributes = [
        (name, value)
        for name, value in ATTRIBUTE.findall(match.group(0))
        if name.lower() not in (b"width", b"height")
    ]
    attributes.append((b"width", str(ICON_SIZE).encode()))
    attributes.append((b"height", str(ICON_SIZE).encode()))
    root = b"<svg " + b" ".join(b'%s="%s"' % pair for pair in attributes) + b">"
    return source[: match.start()] + root + source[match.end() :]


def collect(manifest: dict, table: list[tuple[int, str, str]]) -> dict[str, str]:
    """Return the upstream icon path for every icon a rule uses.

    Paths are relative to dist/, where the manifest is generated, so only the
    icons this extension can actually select are copied.
    """
    return {
        name: manifest["iconDefinitions"][name]["iconPath"].replace("./../", "")
        for _, _, name in table
    }


def rules(manifest: dict) -> list[tuple[int, str, str]]:
    """Translate the VS Code manifest into peb icon rules.

    Folder names and file names containing a path separator cannot be matched by
    a basename lookup and are dropped.
    """
    table: list[tuple[int, str, str]] = []
    for extension, name in manifest["fileExtensions"].items():
        pattern = extension if extension.startswith(".") else "." + extension
        table.append((FILE_SUFFIX, pattern, name))
    for file, name in manifest["fileNames"].items():
        if "/" not in file:
            table.append((FILE_NAME, file, name))
    for folder, name in manifest["folderNames"].items():
        if "/" not in folder:
            table.append((FOLDER_NAME, folder, name))
    for match, pattern, name in table:
        if SEPARATOR in pattern or "\n" in pattern or "\n" in name:
            raise ValueError(f"rule cannot be encoded: {pattern!r} {name!r}")
    table.append((DEFAULT_FILE, "", manifest["file"]))
    table.append((DEFAULT_FOLDER, "", manifest["folder"]))
    seen: dict[tuple[int, str], str] = {}
    for match, pattern, name in table:
        previous = seen.get((match, pattern))
        if previous is not None and previous != name:
            raise ValueError(f"conflicting rules for {pattern!r}: {previous} and {name}")
        seen[(match, pattern)] = name
    return table


def write(source: Path, output: Path) -> None:
    manifest = json.loads((source / "dist" / "material-icons.json").read_text())
    table = rules(manifest)
    icons = collect(manifest, table)

    icons_directory = output / "resources" / "icons"
    if icons_directory.exists():
        shutil.rmtree(icons_directory)
    icons_directory.mkdir(parents=True)
    for name, relative in icons.items():
        (icons_directory / f"{name}.svg").write_bytes(normalize((source / relative).read_bytes()))

    lines = "".join(
        SEPARATOR.join((str(match), pattern, name)) + "\n" for match, pattern, name in table
    )
    (output / "resources" / "rules.psv").write_text(lines, encoding="utf-8")

    shutil.copyfile(source / "LICENSE", output / "resources" / "LICENSE.txt")
    print(f"icons={len(icons)} rules={len(table)}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, help="upstream checkout; cloned when omitted")
    parser.add_argument(
        "--output",
        type=Path,
        default=Path(__file__).resolve().parents[1] / "extensions" / "material-icons",
        help="extension directory (default: extensions/material-icons)",
    )
    parser.add_argument("--skip-build", action="store_true", help="reuse an existing dist/")
    args = parser.parse_args()
    source = prepare(args.source)
    try:
        if not args.skip_build:
            build(source)
        write(source, args.output)
    finally:
        if args.source is None:
            shutil.rmtree(source, ignore_errors=True)


if __name__ == "__main__":
    main()
