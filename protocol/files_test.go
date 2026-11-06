package protocol

import "testing"

func TestTextFileAssociations(t *testing.T) {
	for _, name := range []string{"src/main.go", "README", "Dockerfile", "Dockerfile.dev", ".env.local", ".gitignore", "A.JSON", "view.svg", "go.sum"} {
		if !IsTextFile(name) || FileMode(name) != DocumentText {
			t.Errorf("missing text association: %s", name)
		}
	}
	for _, name := range []string{"pencode", "unknown.custom", "image.PNG", "archive.tar", "data.sqlite", "tool.exe", "font.woff2"} {
		if IsTextFile(name) || FileMode(name) != DocumentFile {
			t.Errorf("unexpected text association: %s", name)
		}
	}
	if n := testing.AllocsPerRun(100, func() { IsTextFile("src/UPPERCASE.JSON"); IsTextFile("Dockerfile.dev") }); n != 0 {
		t.Fatalf("file association allocated %g times", n)
	}
}
