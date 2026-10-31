package workbench

// Indexed by LSP CompletionItemKind; zero also covers omitted or unknown kinds.
// Static keys let both theme preparation and row rendering reuse cached sources.
var pencodeCompletionIcons = [...]pencodeIconKey{
	{"icons/symbol-misc.svg", "icon.foreground"},
	{"icons/symbol-string.svg", "symbolIcon.textForeground"},                  // Text
	{"icons/symbol-method.svg", "symbolIcon.methodForeground"},                // Method
	{"icons/square-function.svg", "symbolIcon.functionForeground"},            // Function
	{"icons/symbol-method.svg", "symbolIcon.constructorForeground"},           // Constructor
	{"icons/symbol-field.svg", "symbolIcon.fieldForeground"},                  // Field
	{"icons/symbol-variable.svg", "symbolIcon.variableForeground"},            // Variable
	{"icons/symbol-class.svg", "symbolIcon.classForeground"},                  // Class
	{"icons/symbol-interface.svg", "symbolIcon.interfaceForeground"},          // Interface
	{"icons/symbol-namespace.svg", "symbolIcon.moduleForeground"},             // Module
	{"icons/symbol-property.svg", "symbolIcon.propertyForeground"},            // Property
	{"icons/symbol-ruler.svg", "symbolIcon.unitForeground"},                   // Unit
	{"icons/symbol-numeric.svg", "symbolIcon.numberForeground"},               // Value
	{"icons/symbol-enum.svg", "symbolIcon.enumeratorForeground"},              // Enum
	{"icons/symbol-keyword.svg", "symbolIcon.keywordForeground"},              // Keyword
	{"icons/symbol-snippet.svg", "symbolIcon.snippetForeground"},              // Snippet
	{"icons/symbol-color.svg", "symbolIcon.colorForeground"},                  // Color
	{"icons/file-text.svg", "symbolIcon.fileForeground"},                      // File
	{"icons/symbol-misc.svg", "symbolIcon.referenceForeground"},               // Reference
	{"icons/folder.svg", "symbolIcon.folderForeground"},                       // Folder
	{"icons/symbol-enum-member.svg", "symbolIcon.enumeratorMemberForeground"}, // EnumMember
	{"icons/symbol-constant.svg", "symbolIcon.constantForeground"},            // Constant
	{"icons/symbol-structure.svg", "symbolIcon.structForeground"},             // Struct
	{"icons/symbol-event.svg", "symbolIcon.eventForeground"},                  // Event
	{"icons/symbol-operator.svg", "symbolIcon.operatorForeground"},            // Operator
	{"icons/symbol-class.svg", "symbolIcon.typeParameterForeground"},          // TypeParameter
}

func languageCompletionIcon(kind int) pencodeIconKey {
	if kind < 0 || kind >= len(pencodeCompletionIcons) {
		kind = 0
	}
	return pencodeCompletionIcons[kind]
}
