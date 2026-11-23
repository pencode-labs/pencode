// Package languagetool describes the built-in stdio servers on a workspace host.
package languagetool

import (
	"strings"

	"github.com/pencode-labs/pencode/peb"
)

type Tool struct {
	ID, Label, Manager, Package string
	Args                        []string
}

type Language struct {
	ID, Label, Icon string
	Selectors       []peb.DocumentSelector
	Tools           []string
}

// Installation describes an executable on the workspace machine. Entry is the
// script passed to the runtime for packages distributed as JavaScript or PHP.
type Installation struct {
	Command, Entry, Problem string
	Managed                 bool
}

var stdio = []string{"--stdio"}

const vscode = "vscode-langservers-extracted"

var Tools = [...]Tool{
	{"gopls", "gopls", "go", "golang.org/x/tools/gopls", nil},
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
	language("php", "PHP", "php", ".blade.php=blade .php .phtml .phpt", "intelephense", "devsense-php-ls", "phpactor", "phpantom_lsp"),
	language("python", "Python", "python", ".py .pyi .pyw", "pyright-langserver", "basedpyright-langserver"),
	language("javascript", "JavaScript", "javascript", ".js .mjs .cjs .jsx=javascriptreact", "typescript-language-server"),
	language("typescript", "TypeScript", "typescript", ".ts .mts .cts .tsx=typescriptreact", "typescript-language-server"),
	language("html", "HTML", "html", ".html .htm", "vscode-html-language-server"),
	language("css", "CSS", "css", ".css .scss=scss .less=less", "vscode-css-language-server"),
	language("json", "JSON", "json", ".json .jsonc=jsonc", "vscode-json-language-server"),
	language("yaml", "YAML", "yaml", ".yaml .yml", "yaml-language-server"),
	language("shellscript", "Bash", "console", ".sh .bash .bashrc .bash_profile", "bash-language-server"),
	language("dockerfile", "Dockerfile", "docker", "Dockerfile Containerfile .dockerfile", "docker-langserver"),
	language("svelte", "Svelte", "svelte", ".svelte", "svelteserver"),
	language("markdown", "Markdown", "markdown", ".md .markdown", "vscode-markdown-language-server"),
}

func Find(id string) *Tool {
	for i := range Tools {
		if Tools[i].ID == id {
			return &Tools[i]
		}
	}
	return nil
}
