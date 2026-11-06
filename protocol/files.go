package protocol

import (
	"path"
	"strings"
)

// DocumentMode distinguishes path-only viewers, checked text, and an explicit
// user request to decode a resource in the code editor.
type DocumentMode uint32

const (
	DocumentFile DocumentMode = iota
	DocumentText
	DocumentTextForced
)

func FileMode(name string) DocumentMode {
	if IsTextFile(name) {
		return DocumentText
	}
	return DocumentFile
}

// IsTextFile recognizes built-in text associations. Unknown types need an
// extension or an explicit choice; content is checked again by the file service.
func IsTextFile(name string) bool {
	name = path.Base(name)
	var folded [256]byte
	if len(name) > len(folded) {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		folded[i] = c
	}
	name = string(folded[:len(name)])
	switch path.Ext(name) {
	case ".txt", ".text", ".log", ".md", ".markdown", ".mdx", ".rst", ".adoc", ".org",
		".go", ".mod", ".sum", ".work", ".rs", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hxx", ".m", ".mm",
		".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".vue", ".svelte", ".astro",
		".html", ".htm", ".xml", ".svg", ".css", ".scss", ".sass", ".less",
		".json", ".jsonc", ".jsonl", ".yaml", ".yml", ".toml", ".ini", ".conf", ".cfg", ".properties", ".env",
		".py", ".pyi", ".rb", ".php", ".java", ".kt", ".kts", ".scala", ".swift", ".dart", ".cs", ".fs", ".fsx", ".vb",
		".sh", ".bash", ".zsh", ".fish", ".ps1", ".bat", ".cmd", ".lua", ".pl", ".pm", ".r", ".jl", ".ex", ".exs", ".erl", ".hrl", ".hs", ".lhs", ".clj", ".cljs", ".cljc", ".edn", ".elm", ".ml", ".mli",
		".sql", ".graphql", ".gql", ".proto", ".csv", ".tsv", ".tex", ".bib", ".diff", ".patch", ".s", ".asm", ".cmake", ".mk", ".nix", ".tf", ".tfvars", ".hcl", ".lock":
		return true
	}
	switch name {
	case "dockerfile", "containerfile", "makefile", "gnumakefile", "cmakelists.txt", "gemfile", "rakefile", "justfile", "procfile", "vagrantfile", "jenkinsfile", "license", "licence", "copying", "readme", "authors", "changelog", "notice",
		".gitignore", ".gitattributes", ".gitmodules", ".gitconfig", ".dockerignore", ".editorconfig", ".npmrc", ".yarnrc", ".bashrc", ".bash_profile", ".zshrc", ".profile", ".env":
		return true
	}
	return strings.HasPrefix(name, ".env.") || strings.HasPrefix(name, "dockerfile.") || strings.HasPrefix(name, "containerfile.")
}

// IsBinaryFile is a cheap search prefilter. Unknown types still undergo content
// checks during search, even though opening them requires an editor choice.
func IsBinaryFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".ttf", ".otf", ".woff", ".woff2", ".mp3", ".wav", ".ogg", ".mp4", ".mov", ".webm", ".mkv", ".avi", ".bin", ".glb", ".zip", ".gz", ".pdf", ".exe", ".dll", ".so", ".a", ".o", ".class", ".jar", ".wasm", ".7z", ".rar", ".bz2", ".xz", ".sqlite", ".db":
		return true
	}
	return false
}
