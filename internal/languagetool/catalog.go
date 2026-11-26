// Package languagetool describes the built-in language servers and debug adapters on a workspace host.
package languagetool

import (
	"strings"

	"github.com/pencode-labs/pencode/peb"
)

type Tool struct {
	ID, Label string
	// An empty Manager means the tool is installed outside Pencode.
	Manager, Package string
	Args             []string
}

type Language struct {
	ID, Label, Icon string
	Selectors       []peb.DocumentSelector
	Tools           []string
}

// Installation describes an executable on the workspace machine. Entry is the
// script or module directory passed to the runtime for managed packages.
type Installation struct {
	Command, Entry, Problem string
	Managed                 bool
}

var stdio = []string{"--stdio"}

const vscode = "vscode-langservers-extracted"

const (
	Delve      = "Delve"
	Xdebug     = "Xdebug"
	Debugpy    = "debugpy"
	LLDB       = "lldb-dap"
	DartDAP    = "dart-dap"
	NetCoreDbg = "netcoredbg"
)

func IsDebugAdapter(id string) bool {
	switch id {
	case Delve, Xdebug, Debugpy, LLDB, DartDAP, NetCoreDbg:
		return true
	}
	return false
}

var DebugLanguages = [...]Language{
	language("go", "Go", "go", "", Delve),
	language("php", "PHP", "php", "", Xdebug),
	language("python", "Python", "python", "", Debugpy),
	language("cpp", "C / C++ / Objective-C / Objective-C++", "cpp", "", LLDB),
	language("rust", "Rust", "rust", "", LLDB),
	language("zig", "Zig", "zig", "", LLDB),
	language("swift", "Swift", "swift", "", LLDB),
	language("dart", "Dart", "dart", "", DartDAP),
	language("csharp", "C#", "csharp", "", NetCoreDbg),
}

var Tools = [...]Tool{
	{Delve, "Delve", "go", "github.com/go-delve/delve/cmd/dlv", nil},
	{Xdebug, "Xdebug", "VSIX", "xdebug/vscode-php-debug", nil},
	{Debugpy, "debugpy", "pip", "debugpy", nil},
	{LLDB, "LLDB DAP", "", "lldb-dap", nil},
	{DartDAP, "Dart Debug Adapter", "", "dart", []string{"debug_adapter"}},
	{NetCoreDbg, "NetCoreDbg", "", "netcoredbg", []string{"--interpreter=vscode"}},
	{"gopls", "gopls", "go", "golang.org/x/tools/gopls", nil},
	{"clangd", "clangd", "", "clangd", nil},
	{"sourcekit-lsp", "SourceKit-LSP", "", "sourcekit-lsp", nil},
	{"dart", "Dart Analysis Server", "", "dart", []string{"language-server"}},
	{"csharp-ls", "C# Language Server", "dotnet", "csharp-ls", nil},
	{"rust-analyzer", "rust-analyzer", "", "rust-analyzer", nil},
	{"lua-language-server", "Lua Language Server", "", "lua-language-server", nil},
	{"zls", "Zig Language Server", "", "zls", nil},
	{"ruby-lsp", "Ruby LSP", "", "ruby-lsp", nil},
	{"neocmakelsp", "neocmakelsp", "cargo", "neocmakelsp", []string{"stdio"}},
	{"taplo", "Taplo", "", "taplo", []string{"lsp", "stdio"}},
	{"sqls", "sqls", "go", "github.com/sqls-server/sqls", nil},
	{"graphql-lsp", "GraphQL Language Service", "npm", "graphql-language-service-cli", []string{"server", "--method", "stream"}},
	{"intelephense", "Intelephense", "npm", "intelephense", stdio},
	{"devsense-php-ls", "PHP Tools", "npm", "devsense-php-ls", stdio},
	{"phpactor", "Phpactor", "composer", "phpactor/phpactor", []string{"language-server"}},
	{"phpantom_lsp", "PHPantom", "cargo", "phpantom_lsp", nil},
	{"pyright-langserver", "Pyright", "npm", "pyright", stdio},
	{"basedpyright-langserver", "basedpyright", "npm", "basedpyright", stdio},
	{"typescript-language-server", "TypeScript Language Server", "npm", "typescript-language-server", stdio},
	{"vscode-html-language-server", "HTML Language Server", "npm", vscode, stdio},
	{"vscode-css-language-server", "CSS Language Server", "npm", vscode, stdio},
	{"vscode-json-language-server", "JSON Language Server", "npm", vscode, stdio},
	{"yaml-language-server", "YAML Language Server", "npm", "yaml-language-server", stdio},
	{"bash-language-server", "Bash Language Server", "npm", "bash-language-server", []string{"start"}},
	{"docker-langserver", "Dockerfile Language Server", "npm", "dockerfile-language-server-nodejs", stdio},
	{"svelteserver", "Svelte Language Server", "npm", "svelte-language-server", stdio},
	{"vscode-markdown-language-server", "Markdown Language Server", "npm", vscode, stdio},
}

// Each pattern may specify a more precise LSP document language after '='.
func language(id, label, icon, patterns string, tools ...string) Language {
	l := Language{ID: id, Label: label, Icon: "icons/languages/" + icon + ".svg", Tools: tools}
	for pattern := range strings.FieldsSeq(patterns) {
		name, documentID, _ := strings.Cut(pattern, "=")
		match := peb.FileName
		if strings.HasPrefix(name, ".") {
			match = peb.FileSuffix
		}
		l.Selectors = append(l.Selectors, peb.DocumentSelector{Pattern: name, LanguageID: documentID, Match: match})
	}
	return l
}

var Languages = [...]Language{
	language("go", "Go", "go", ".go go.mod=go.mod go.work=go.work", "gopls"),
	language("cpp", "C / C++ / Objective-C / Objective-C++", "cpp", ".c=c .C .cc .cpp .cxx .c++ .h .hh .hpp .hxx .h++ .ipp .inl .ixx .cppm .m=objective-c .mm=objective-cpp", "clangd"),
	language("rust", "Rust", "rust", ".rs", "rust-analyzer"),
	language("lua", "Lua", "lua", ".lua", "lua-language-server"),
	language("zig", "Zig", "zig", ".zig .zon", "zls"),
	language("ruby", "Ruby", "ruby", ".rb .rake .gemspec Gemfile Rakefile", "ruby-lsp"),
	language("php", "PHP", "php", ".blade.php=blade .php .phtml .phpt", "intelephense", "devsense-php-ls", "phpactor", "phpantom_lsp"),
	language("python", "Python", "python", ".py .pyi .pyw", "pyright-langserver", "basedpyright-langserver"),
	language("javascript", "JavaScript", "javascript", ".js .mjs .cjs .jsx=javascriptreact", "typescript-language-server"),
	language("typescript", "TypeScript", "typescript", ".ts .mts .cts .tsx=typescriptreact", "typescript-language-server"),
	language("html", "HTML", "html", ".html .htm", "vscode-html-language-server"),
	language("css", "CSS", "css", ".css .scss=scss .less=less", "vscode-css-language-server"),
	language("json", "JSON", "json", ".json .jsonc=jsonc", "vscode-json-language-server"),
	language("yaml", "YAML", "yaml", ".yaml .yml", "yaml-language-server"),
	language("toml", "TOML", "toml", ".toml", "taplo"),
	language("cmake", "CMake", "cmake", "CMakeLists.txt .cmake", "neocmakelsp"),
	language("sql", "SQL", "database", ".sql", "sqls"),
	language("graphql", "GraphQL", "graphql", ".graphql .gql .graphqls", "graphql-lsp"),
	language("shellscript", "Bash", "console", ".sh .bash .bashrc .bash_profile", "bash-language-server"),
	language("dockerfile", "Dockerfile", "docker", "Dockerfile Containerfile .dockerfile", "docker-langserver"),
	language("svelte", "Svelte", "svelte", ".svelte", "svelteserver"),
	language("markdown", "Markdown", "markdown", ".md .markdown", "vscode-markdown-language-server"),
	language("swift", "Swift", "swift", ".swift", "sourcekit-lsp"),
	language("dart", "Dart", "dart", ".dart", "dart"),
	language("csharp", "C#", "csharp", ".cs", "csharp-ls"),
}

func Find(id string) *Tool {
	for i := range Tools {
		if Tools[i].ID == id {
			return &Tools[i]
		}
	}
	return nil
}
