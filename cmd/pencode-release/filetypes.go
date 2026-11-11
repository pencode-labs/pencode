package main

import (
	"fmt"
	"strings"
)

// Register alternatives without changing the user's default file handlers.
const fileExtensions = ".txt .text .log .md .markdown .rst .go .mod .sum .work .rs .c .h .cc .cpp .cxx .hpp .cs .java .kt .kts .swift .m .mm .py .pyw .rb .php .phtml .js .mjs .cjs .jsx .ts .tsx .vue .svelte .html .htm .css .scss .sass .less .json .jsonc .jsonl .xml .svg .yaml .yml .toml .ini .conf .cfg .properties .sql .sh .bash .zsh .fish .ps1 .bat .cmd .dockerfile .gitignore .gitattributes .editorconfig .env .csv .tsv .ipynb .png .jpg .jpeg .gif .webp .bmp .ico .pdf .docx .xlsx .pptx .odt .ods .odp"

const linuxDesktopEntry = `[Desktop Entry]
Type=Application
Name=Pencode
Comment=Code editor
Exec=/opt/pencode/Pencode -- %F
Icon=pencode
Terminal=false
Categories=Development;IDE;
StartupWMClass=pencode
MimeType=text/plain;text/markdown;text/x-rst;text/x-go;text/x-rust;text/x-c;text/x-c++;text/x-csrc;text/x-chdr;text/x-c++src;text/x-c++hdr;text/x-csharp;text/x-java;text/x-kotlin;text/x-swift;text/x-objcsrc;text/x-python;text/x-ruby;text/x-php;text/javascript;application/javascript;application/typescript;text/x-typescript;text/html;text/css;text/x-scss;text/x-sass;text/x-less;application/json;application/x-ipynb+json;application/xml;text/xml;image/svg+xml;application/yaml;application/x-yaml;text/yaml;text/x-yaml;application/toml;text/x-toml;text/x-ini;text/x-log;text/x-sql;application/sql;application/x-shellscript;text/x-shellscript;text/x-script.python;text/x-makefile;text/x-cmake;text/x-dockerfile;text/x-diff;text/x-patch;text/csv;text/tab-separated-values;image/png;image/jpeg;image/gif;image/webp;image/bmp;image/vnd.microsoft.icon;application/pdf;application/vnd.openxmlformats-officedocument.wordprocessingml.document;application/vnd.openxmlformats-officedocument.spreadsheetml.sheet;application/vnd.openxmlformats-officedocument.presentationml.presentation;application/vnd.oasis.opendocument.text;application/vnd.oasis.opendocument.spreadsheet;application/vnd.oasis.opendocument.presentation;inode/directory;
`

const macOSDocumentTypes = `<array><dict>
<key>CFBundleTypeName</key><string>Documents</string>
<key>CFBundleTypeRole</key><string>Editor</string>
<key>LSHandlerRank</key><string>Alternate</string>
<key>LSItemContentTypes</key><array><string>public.data</string></array>
</dict><dict>
<key>CFBundleTypeName</key><string>Folder</string>
<key>CFBundleTypeRole</key><string>Viewer</string>
<key>LSHandlerRank</key><string>Alternate</string>
<key>LSItemContentTypes</key><array><string>public.folder</string></array>
</dict></array>`

func windowsFileAssociations() string {
	var b strings.Builder
	for extension := range strings.FieldsSeq(fileExtensions) {
		fmt.Fprintf(&b, "Root: HKCU; Subkey: \"Software\\Classes\\%s\\OpenWithProgids\"; ValueType: string; ValueName: \"Pencode.File\"; ValueData: \"\"; Flags: uninsdeletevalue uninsdeletekeyifempty\n", extension)
		fmt.Fprintf(&b, "Root: HKCU; Subkey: \"Software\\Classes\\Applications\\Pencode.exe\\SupportedTypes\"; ValueType: string; ValueName: \"%s\"; ValueData: \"\"\n", extension)
	}
	return b.String()
}
