"""Generate a Go overlay for an isolated, on-demand memory diagnostic build.

No probe files enter a normal build. Run the resulting executable with
PENCODE_MEMORY_DIRECTORY set to a fresh directory; endpoint files and snapshots
are written there. GUI commands run on the application thread.
"""

import argparse
import json
from pathlib import Path


def prepare(root: Path, output: Path) -> Path:
    output.mkdir(parents=True, exist_ok=True)
    replacements = {}

    def replace(name: str, source: str) -> None:
        target = output / (name.replace("/", "-") + ".txt")
        target.write_text(source)
        replacements[str(root / name)] = str(target)

    main = (root / "main.go").read_text()
    marker = "\tif err := app.Run(); err != nil {"
    assert main.count(marker) == 1
    main = main.replace(marker, '\tif err := session.StartMemoryProbe(app); err != nil { panic(err) }\n' + marker)
    main = main.replace("registry, err := OnixRegistry()", 'plan.Spec.Window.Title = "pencode — memory diagnostic"\n\tregistry, err := OnixRegistry()')
    main = main.replace("server.Options{}", 'server.Options{ExtensionDirectory: os.Getenv("PENCODE_MEMORY_EXTENSIONS"), ConfigurationDirectory: os.Getenv("PENCODE_MEMORY_DIRECTORY") + "/resources"}')
    replace("main.go", main)
    session = (root / "workbench/session.go").read_text()
    start = session.index("func NewSession(")
    end = session.index("\nfunc newSession(", start)
    session = session[:start] + '''func NewSession(backend *client.Client) *Session {
        directory := os.Getenv("PENCODE_MEMORY_DIRECTORY")
        return newSession(backend, filepath.Join(directory, "cache"), filepath.Join(directory, "config"))
    }
''' + session[end:]
    replace("workbench/session.go", session)
    configuration = (root / "configuration/configuration.go").read_text()
    configuration = configuration.replace("func Directory() (string, error) {", '''func Directory() (string, error) {
        if directory := os.Getenv("PENCODE_MEMORY_DIRECTORY"); directory != "" {
            return filepath.Join(directory, "resources"), nil
        }
''')
    replace("configuration/configuration.go", configuration)
    for name, template in [("memory_probe.go", "runtime.go.txt"), ("workbench/memory_probe.go", "workbench.go.txt")]:
        replace(name, (root / "tools/memory_probe" / template).read_text())
    overlay = output / "overlay.json"
    overlay.write_text(json.dumps({"Replace": replacements}, indent=2) + "\n")
    return overlay


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    print(prepare(Path(__file__).resolve().parents[1], args.output.resolve()))
