package workbench

// Indexed by LSP CompletionItemKind; zero also covers omitted or unknown kinds.
// Static keys let both theme preparation and row rendering reuse cached sources.
var pencodeCompletionIcons = [...]pencodeIconKey{
	{"icons/symbol-misc.svg", "icon.foreground"},
	{"icons/symbol-string.svg", "icon.foreground"},      // Text
	{"icons/symbol-method.svg", "icon.foreground"},      // Method
	{"icons/square-function.svg", "icon.foreground"},    // Function
	{"icons/symbol-method.svg", "icon.foreground"},      // Constructor
	{"icons/symbol-field.svg", "icon.foreground"},       // Field
	{"icons/symbol-variable.svg", "icon.foreground"},    // Variable
	{"icons/symbol-class.svg", "icon.foreground"},       // Class
	{"icons/symbol-interface.svg", "icon.foreground"},   // Interface
	{"icons/symbol-namespace.svg", "icon.foreground"},   // Module
	{"icons/symbol-property.svg", "icon.foreground"},    // Property
	{"icons/symbol-ruler.svg", "icon.foreground"},       // Unit
	{"icons/symbol-numeric.svg", "icon.foreground"},     // Value
	{"icons/symbol-enum.svg", "icon.foreground"},        // Enum
	{"icons/symbol-keyword.svg", "icon.foreground"},     // Keyword
	{"icons/symbol-snippet.svg", "icon.foreground"},     // Snippet
	{"icons/symbol-color.svg", "icon.foreground"},       // Color
	{"icons/file-text.svg", "icon.foreground"},          // File
	{"icons/symbol-misc.svg", "icon.foreground"},        // Reference
	{"icons/folder.svg", "icon.foreground"},             // Folder
	{"icons/symbol-enum-member.svg", "icon.foreground"}, // EnumMember
	{"icons/symbol-constant.svg", "icon.foreground"},    // Constant
	{"icons/symbol-structure.svg", "icon.foreground"},   // Struct
	{"icons/symbol-event.svg", "icon.foreground"},       // Event
	{"icons/symbol-operator.svg", "icon.foreground"},    // Operator
	{"icons/symbol-class.svg", "icon.foreground"},       // TypeParameter
}

func languageCompletionIcon(kind int) pencodeIconKey {
	if kind < 0 || kind >= len(pencodeCompletionIcons) {
		kind = 0
	}
	return pencodeCompletionIcons[kind]
}
