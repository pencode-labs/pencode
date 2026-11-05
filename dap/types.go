package dap

type Capabilities struct {
	ExceptionBreakpointFilters []ExceptionFilter `json:"exceptionBreakpointFilters"`
	ConfigurationDone          bool              `json:"supportsConfigurationDoneRequest"`
	ConditionalBreakpoints     bool              `json:"supportsConditionalBreakpoints"`
	HitConditionalBreakpoints  bool              `json:"supportsHitConditionalBreakpoints"`
	LogPoints                  bool              `json:"supportsLogPoints"`
	Terminate                  bool              `json:"supportsTerminateRequest"`
	VariablePaging             bool              `json:"supportsVariablePaging"`
}
type ExceptionFilter struct {
	Filter  string `json:"filter"`
	Default bool   `json:"default"`
}
type Source struct {
	Name            string `json:"name,omitempty"`
	Path            string `json:"path,omitempty"`
	SourceReference int    `json:"sourceReference,omitempty"`
}
type SourceBreakpoint struct {
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
	LogMessage   string `json:"logMessage,omitempty"`
	Line         int    `json:"line"`
	Column       int    `json:"column,omitempty"`
}
type Breakpoint struct {
	Message  string `json:"message"`
	Source   Source `json:"source"`
	ID       int    `json:"id"`
	Line     int    `json:"line"`
	Verified bool   `json:"verified"`
}
type Thread struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}
type StackFrame struct {
	Name   string `json:"name"`
	Source Source `json:"source"`
	ID     int    `json:"id"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}
type Scope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variablesReference"`
	Expensive          bool   `json:"expensive"`
}
type Variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type"`
	EvaluateName       string `json:"evaluateName"`
	VariablesReference int    `json:"variablesReference"`
	NamedVariables     int    `json:"namedVariables"`
	IndexedVariables   int    `json:"indexedVariables"`
}
type EvaluateResponse struct {
	Result             string `json:"result"`
	Type               string `json:"type"`
	VariablesReference int    `json:"variablesReference"`
	NamedVariables     int    `json:"namedVariables"`
	IndexedVariables   int    `json:"indexedVariables"`
}
type StoppedEvent struct {
	Reason            string `json:"reason"`
	Description       string `json:"description"`
	ThreadID          int    `json:"threadId"`
	AllThreadsStopped bool   `json:"allThreadsStopped"`
}
type RunInTerminalArguments struct {
	Env                         map[string]*string `json:"env"`
	Kind                        string             `json:"kind"`
	Title                       string             `json:"title"`
	Cwd                         string             `json:"cwd"`
	Args                        []string           `json:"args"`
	ArgsCanBeInterpretedByShell bool               `json:"argsCanBeInterpretedByShell"`
}
