// Package debugger owns language-independent debug session state. Snapshots
// are immutable after publication and can be consumed without per-frame copies.
package debugger

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/dap"
	"github.com/pencode-labs/pencode/output"
)

type State uint8

const (
	Starting State = iota
	Configuring
	Running
	Stopped
	Stopping
	Ended
	Failed
)

func (state State) String() string {
	return [...]string{"启动中", "配置中", "运行中", "已暂停", "结束中", "已结束", "调试失败"}[state]
}

type Snapshot struct {
	Revision      uint64
	Epoch         uint64
	State         State
	Reason, Error string
	Capabilities  dap.Capabilities
	Threads       []dap.Thread
	ThreadID      int
	Frames        []dap.StackFrame
	FrameID       int
	Scopes        []dap.Scope
	Variables     map[int][]dap.Variable
	Evaluations   []dap.Variable
	Breakpoints   map[string][]dap.Breakpoint
}

type Open func(context.Context) (io.ReadWriteCloser, error)

type Session struct {
	mu                 sync.Mutex
	bpMu               sync.Mutex
	view               Snapshot
	conn               *dap.Conn
	ctx                context.Context
	cancel             context.CancelFunc
	done               chan struct{}
	initialized        chan struct{}
	initOnce           sync.Once
	stopOnce           sync.Once
	config             Config
	breakpoints        map[string][]dap.SourceBreakpoint
	loading            map[int]bool
	configured         bool
	breakpointRevision uint64
	controlPending     bool
	frameGeneration    uint64
	consoleGeneration  uint64
	wake               func()
	Output             *output.Channel
}

func Start(parent context.Context, config Config, breakpoints map[string][]dap.SourceBreakpoint, open Open, handler dap.Handler, channel *output.Channel, wake func()) *Session {
	ctx, cancel := context.WithCancel(parent)
	s := &Session{ctx: ctx, cancel: cancel, done: make(chan struct{}), initialized: make(chan struct{}), config: config, breakpoints: maps.Clone(breakpoints), loading: make(map[int]bool), wake: wake, Output: channel, view: Snapshot{Revision: 1, State: Starting}}
	go s.run(open, handler)
	return s
}

func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Snapshot() Snapshot    { s.mu.Lock(); defer s.mu.Unlock(); return s.view }
func (s *Session) notify() {
	if s.wake != nil {
		s.wake()
	}
}
func (s *Session) Close() {
	s.cancel()
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (s *Session) report(err error) {
	if err == nil || s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if s.view.State < Stopping {
		s.view.Error = err.Error()
		s.view.Revision++
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Session) run(open Open, handler dap.Handler) {
	defer close(s.done)
	defer s.cancel()
	err := s.start(open, handler)
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
		<-conn.EventsDone()
	}
	s.mu.Lock()
	if err != nil && s.ctx.Err() == nil {
		s.view.State, s.view.Error = Failed, err.Error()
	} else if s.view.State != Failed {
		s.view.State = Ended
	}
	s.invalidateLocked()
	s.view.Revision++
	s.mu.Unlock()
	s.notify()
}

func (s *Session) start(open Open, handler dap.Handler) error {
	startup, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()
	stream, err := open(startup)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		_ = stream.Close()
		return s.ctx.Err()
	}
	conn := dap.New(stream, s.event, handler)
	s.conn = conn
	s.mu.Unlock()
	go func() { <-s.ctx.Done(); _ = conn.Close() }()
	var caps dap.Capabilities
	err = conn.Call(startup, "initialize", struct {
		ClientID        string `json:"clientID"`
		AdapterID       string `json:"adapterID"`
		PathFormat      string `json:"pathFormat"`
		LinesStartAt1   bool   `json:"linesStartAt1"`
		ColumnsStartAt1 bool   `json:"columnsStartAt1"`
		RunInTerminal   bool   `json:"supportsRunInTerminalRequest"`
		VariablePaging  bool   `json:"supportsVariablePaging"`
		VariableType    bool   `json:"supportsVariableType"`
	}{"pencode", s.config.Adapter, "path", true, true, handler != nil, true, true}, &caps)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.view.Capabilities = caps
	s.view.State = Configuring
	s.view.Revision++
	s.mu.Unlock()
	s.notify()
	launch := make(chan error, 1)
	go func() { launch <- conn.Call(startup, s.config.Request, s.config.Configuration, nil) }()
	select {
	case <-s.initialized:
	case err := <-launch:
		if err != nil {
			return err
		}
		// Even adapters which respond immediately must signal readiness before
		// receiving configuration requests.
		select {
		case <-s.initialized:
		case <-startup.Done():
			return startup.Err()
		case <-conn.Done():
			return conn.Err()
		}
		launch = nil
	case <-startup.Done():
		return startup.Err()
	case <-conn.Done():
		return conn.Err()
	}
	s.bpMu.Lock()
	for {
		s.mu.Lock()
		wanted, revision := maps.Clone(s.breakpoints), s.breakpointRevision
		s.mu.Unlock()
		for name, bps := range wanted {
			if err = s.sendBreakpoints(startup, name, bps); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
		s.mu.Lock()
		if revision == s.breakpointRevision {
			s.configured = true
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
	}
	s.bpMu.Unlock()
	if err != nil {
		return err
	}
	filters := make([]string, 0, len(caps.ExceptionBreakpointFilters))
	for _, filter := range caps.ExceptionBreakpointFilters {
		if filter.Default {
			filters = append(filters, filter.Filter)
		}
	}
	if len(caps.ExceptionBreakpointFilters) > 0 || !caps.ConfigurationDone {
		if err := conn.Call(startup, "setExceptionBreakpoints", struct {
			Filters []string `json:"filters"`
		}{filters}, nil); err != nil {
			return err
		}
	}
	if caps.ConfigurationDone {
		if err := conn.Call(startup, "configurationDone", struct{}{}, nil); err != nil {
			return err
		}
	}
	if launch != nil {
		select {
		case err = <-launch:
			if err != nil {
				return err
			}
		case <-startup.Done():
			return startup.Err()
		case <-conn.Done():
			return conn.Err()
		}
	}
	s.mu.Lock()
	if s.view.State == Configuring {
		s.view.State = Running
		s.view.Revision++
	}
	s.mu.Unlock()
	s.notify()
	select {
	case <-s.ctx.Done():
		return nil
	case <-conn.Done():
		<-conn.EventsDone()
		s.mu.Lock()
		ended := s.view.State >= Stopping
		s.mu.Unlock()
		if ended {
			return nil
		}
		return conn.Err()
	}
}

func (s *Session) invalidateLocked() {
	s.view.Epoch++
	s.frameGeneration++
	s.view.Frames, s.view.Scopes, s.view.Variables = nil, nil, nil
	s.view.FrameID = 0
	// Keep console history, but adapter references expire when execution resumes.
	if len(s.view.Evaluations) > 0 {
		s.view.Evaluations = append([]dap.Variable(nil), s.view.Evaluations...)
		for i := range s.view.Evaluations {
			s.view.Evaluations[i].VariablesReference = 0
		}
	}
	clear(s.loading)
}

func (s *Session) event(name string, body json.RawMessage) {
	if name == "initialized" {
		s.initOnce.Do(func() { close(s.initialized) })
		return
	}
	if name == "output" {
		var event struct {
			Output string `json:"output"`
		}
		if json.Unmarshal(body, &event) == nil && s.Output != nil {
			_, _ = s.Output.Write([]byte(event.Output))
			s.notify()
		}
		return
	}
	s.mu.Lock()
	if s.view.State >= Ended {
		s.mu.Unlock()
		return
	}
	refresh, terminate := false, false
	switch name {
	case "stopped":
		var event dap.StoppedEvent
		if json.Unmarshal(body, &event) != nil || s.view.State >= Stopping {
			s.mu.Unlock()
			return
		}
		s.invalidateLocked()
		s.view.State, s.view.ThreadID, s.view.Reason, s.view.Error = Stopped, event.ThreadID, event.Reason, ""
		refresh = true
	case "continued":
		if s.view.State >= Stopping {
			s.mu.Unlock()
			return
		}
		s.invalidateLocked()
		s.view.State = Running
	case "terminated":
		s.invalidateLocked()
		s.view.State = Stopping
		terminate = true
	case "breakpoint":
		var event struct {
			Breakpoint dap.Breakpoint `json:"breakpoint"`
		}
		if json.Unmarshal(body, &event) == nil {
			for path, list := range s.view.Breakpoints {
				for i, bp := range list {
					if bp.ID != 0 && bp.ID == event.Breakpoint.ID {
						copyList := append([]dap.Breakpoint(nil), list...)
						copyList[i] = event.Breakpoint
						s.view.Breakpoints = maps.Clone(s.view.Breakpoints)
						s.view.Breakpoints[path] = copyList
					}
				}
			}
		}
	case "thread":
		refresh = s.view.State == Stopped
	default:
		s.mu.Unlock()
		return
	}
	s.view.Revision++
	epoch := s.view.Epoch
	s.mu.Unlock()
	s.notify()
	if refresh {
		go s.refresh(epoch)
	}
	if terminate {
		s.Stop()
	}
}

func (s *Session) request(command string, args any, result any) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return errors.New("debugger is starting")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	return conn.Call(ctx, command, args, result)
}

func (s *Session) refresh(epoch uint64) {
	var response struct {
		Threads []dap.Thread `json:"threads"`
	}
	if err := s.request("threads", struct{}{}, &response); err != nil {
		s.report(err)
		return
	}
	s.mu.Lock()
	if s.view.Epoch != epoch || s.view.State != Stopped {
		s.mu.Unlock()
		return
	}
	s.view.Threads = response.Threads
	if s.view.ThreadID == 0 && len(response.Threads) > 0 {
		s.view.ThreadID = response.Threads[0].ID
	}
	thread := s.view.ThreadID
	s.view.Revision++
	s.mu.Unlock()
	s.notify()
	s.fetchFrames(epoch, thread)
}

func (s *Session) SelectThread(id int) {
	s.mu.Lock()
	if s.view.State != Stopped {
		s.mu.Unlock()
		return
	}
	s.invalidateLocked()
	s.view.ThreadID = id
	s.view.Revision++
	epoch := s.view.Epoch
	s.mu.Unlock()
	s.notify()
	go s.fetchFrames(epoch, id)
}

func (s *Session) fetchFrames(epoch uint64, thread int) {
	var response struct {
		Frames []dap.StackFrame `json:"stackFrames"`
	}
	if err := s.request("stackTrace", struct {
		ThreadID   int `json:"threadId"`
		StartFrame int `json:"startFrame"`
		Levels     int `json:"levels"`
	}{thread, 0, 100}, &response); err != nil {
		s.report(err)
		return
	}
	s.mu.Lock()
	if s.view.Epoch != epoch || s.view.ThreadID != thread || s.view.State != Stopped {
		s.mu.Unlock()
		return
	}
	s.view.Frames = response.Frames
	var id int
	if len(response.Frames) > 0 {
		id = response.Frames[0].ID
		s.selectFrameLocked(id)
	}
	generation := s.frameGeneration
	s.view.Revision++
	s.mu.Unlock()
	s.notify()
	if len(response.Frames) > 0 {
		s.fetchScopes(epoch, generation, id)
	}
}

func (s *Session) selectFrameLocked(id int) {
	s.frameGeneration++
	s.view.FrameID, s.view.Scopes = id, nil
}

func (s *Session) SelectFrame(id int) {
	s.mu.Lock()
	if s.view.State != Stopped {
		s.mu.Unlock()
		return
	}
	s.selectFrameLocked(id)
	generation, epoch := s.frameGeneration, s.view.Epoch
	s.view.Revision++
	s.mu.Unlock()
	s.notify()
	go s.fetchScopes(epoch, generation, id)
}

func (s *Session) fetchScopes(epoch, generation uint64, id int) {
	var response struct {
		Scopes []dap.Scope `json:"scopes"`
	}
	if err := s.request("scopes", struct {
		FrameID int `json:"frameId"`
	}{id}, &response); err != nil {
		s.report(err)
		return
	}
	s.mu.Lock()
	if s.view.Epoch != epoch || s.frameGeneration != generation || s.view.State != Stopped {
		s.mu.Unlock()
		return
	}
	s.view.Scopes = response.Scopes
	s.view.Revision++
	s.mu.Unlock()
	s.notify()
	for _, scope := range response.Scopes {
		if !scope.Expensive {
			s.Expand(scope.VariablesReference)
		}
	}
}

func (s *Session) Expand(reference int) {
	s.mu.Lock()
	if reference <= 0 || s.view.State != Stopped || s.loading[reference] {
		s.mu.Unlock()
		return
	}
	if _, ok := s.view.Variables[reference]; ok {
		s.mu.Unlock()
		return
	}
	s.loading[reference] = true
	epoch := s.view.Epoch
	s.mu.Unlock()
	go func() {
		var response struct {
			Variables []dap.Variable `json:"variables"`
		}
		err := s.request("variables", struct {
			Reference int `json:"variablesReference"`
			Start     int `json:"start,omitempty"`
			Count     int `json:"count,omitempty"`
		}{reference, 0, 100}, &response)
		s.mu.Lock()
		if s.view.Epoch != epoch || s.view.State != Stopped {
			s.mu.Unlock()
			return
		}
		delete(s.loading, reference)
		if err == nil {
			s.view.Variables = maps.Clone(s.view.Variables)
			if s.view.Variables == nil {
				s.view.Variables = make(map[int][]dap.Variable)
			}
			s.view.Variables[reference] = response.Variables
		} else {
			s.view.Error = err.Error()
		}
		s.view.Revision++
		s.mu.Unlock()
		s.notify()
	}()
}

func (s *Session) Control(command string) {
	s.mu.Lock()
	if s.controlPending || s.view.State != Stopped && !(command == "pause" && s.view.State == Running) {
		s.mu.Unlock()
		return
	}
	if command != "continue" && command != "next" && command != "stepIn" && command != "stepOut" && command != "pause" {
		s.mu.Unlock()
		return
	}
	s.controlPending = true
	thread, epoch := s.view.ThreadID, s.view.Epoch
	s.mu.Unlock()
	go func() {
		err := s.request(command, struct {
			ThreadID int `json:"threadId"`
		}{thread}, nil)
		s.mu.Lock()
		s.controlPending = false
		if err == nil && command != "pause" && s.view.Epoch == epoch && s.view.State == Stopped {
			s.invalidateLocked()
			s.view.State = Running
			s.view.Revision++
		}
		s.mu.Unlock()
		s.report(err)
		s.notify()
	}()
}

func (s *Session) Evaluate(expression string) {
	s.mu.Lock()
	if s.view.State != Stopped {
		s.mu.Unlock()
		return
	}
	frame, epoch, generation, console := s.view.FrameID, s.view.Epoch, s.frameGeneration, s.consoleGeneration
	s.mu.Unlock()
	go func() {
		defer s.notify()
		var result dap.EvaluateResponse
		err := s.request("evaluate", struct {
			Expression string `json:"expression"`
			FrameID    int    `json:"frameId"`
			Context    string `json:"context"`
		}{expression, frame, "repl"}, &result)
		s.mu.Lock()
		if s.view.Epoch != epoch || s.frameGeneration != generation || s.consoleGeneration != console || s.view.State != Stopped {
			s.mu.Unlock()
			return
		}
		if err != nil {
			result.Result = err.Error()
			result.VariablesReference = 0
		}
		// Bound history and publish a fresh slice so older snapshots stay immutable.
		history := s.view.Evaluations[max(0, len(s.view.Evaluations)-99):]
		next := make([]dap.Variable, len(history)+1)
		copy(next, history)
		next[len(history)] = dap.Variable{Name: expression, Value: result.Result, Type: result.Type, VariablesReference: result.VariablesReference, NamedVariables: result.NamedVariables, IndexedVariables: result.IndexedVariables}
		s.view.Evaluations = next
		s.view.Revision++
		s.mu.Unlock()
	}()
}

func (s *Session) ClearConsole() {
	s.mu.Lock()
	s.consoleGeneration++
	s.view.Evaluations = nil
	s.view.Revision++
	s.mu.Unlock()
	if s.Output != nil {
		s.Output.Clear()
	}
	s.notify()
}

func (s *Session) SetBreakpoints(path string, breakpoints []dap.SourceBreakpoint) {
	s.mu.Lock()
	if s.breakpoints == nil {
		s.breakpoints = make(map[string][]dap.SourceBreakpoint)
	}
	s.breakpoints[path] = breakpoints
	s.breakpointRevision++
	ready := s.configured && s.view.State < Stopping
	s.mu.Unlock()
	if !ready {
		return
	}
	go func() {
		s.bpMu.Lock()
		defer s.bpMu.Unlock()
		s.mu.Lock()
		latest := s.breakpoints[path]
		s.mu.Unlock()
		ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
		defer cancel()
		s.report(s.sendBreakpoints(ctx, path, latest))
	}()
}

func (s *Session) sendBreakpoints(ctx context.Context, path string, bps []dap.SourceBreakpoint) error {
	if bps == nil {
		bps = []dap.SourceBreakpoint{}
	}
	var response struct {
		Breakpoints []dap.Breakpoint `json:"breakpoints"`
	}
	err := s.conn.Call(ctx, "setBreakpoints", struct {
		Source      dap.Source             `json:"source"`
		Breakpoints []dap.SourceBreakpoint `json:"breakpoints"`
	}{dap.Source{Path: path}, bps}, &response)
	if err == nil {
		s.mu.Lock()
		s.view.Breakpoints = maps.Clone(s.view.Breakpoints)
		if s.view.Breakpoints == nil {
			s.view.Breakpoints = make(map[string][]dap.Breakpoint)
		}
		s.view.Breakpoints[path] = response.Breakpoints
		s.view.Revision++
		s.mu.Unlock()
		s.notify()
	}
	return err
}

func (s *Session) Stop() {
	s.stopOnce.Do(s.stop)
}

func (s *Session) stop() {
	s.mu.Lock()
	if s.view.State == Ended || s.view.State == Failed {
		s.mu.Unlock()
		return
	}
	conn := s.conn
	s.view.State = Stopping
	s.invalidateLocked()
	s.view.Revision++
	s.mu.Unlock()
	s.notify()
	go func() {
		if conn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = conn.Call(ctx, "disconnect", struct {
				TerminateDebuggee bool `json:"terminateDebuggee"`
			}{s.config.Request == "launch"}, nil)
			cancel()
		}
		s.Close()
	}()
}
