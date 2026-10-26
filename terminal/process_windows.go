package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var createPseudoConsole = kernel32.NewProc("CreatePseudoConsole")
var resizePseudoConsole = kernel32.NewProc("ResizePseudoConsole")
var closePseudoConsole = kernel32.NewProc("ClosePseudoConsole")
var initializeAttributes = kernel32.NewProc("InitializeProcThreadAttributeList")
var updateAttributes = kernel32.NewProc("UpdateProcThreadAttribute")
var deleteAttributes = kernel32.NewProc("DeleteProcThreadAttributeList")
var createJob = kernel32.NewProc("CreateJobObjectW")
var assignJob = kernel32.NewProc("AssignProcessToJobObject")
var terminateJob = kernel32.NewProc("TerminateJobObject")
var resumeThread = kernel32.NewProc("ResumeThread")

type windowsProcess struct {
	input, output         *os.File
	console, process, job syscall.Handle
	once                  sync.Once
	mu                    sync.Mutex
	started               bool
	cols, rows            int
	pid                   int
}

func startNative(directory string, cols, rows int) (Process, error) {
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		shell = os.Getenv("COMSPEC")
		if shell == "" {
			shell = "cmd.exe"
		}
	}
	cmd := exec.Command(shell)
	cmd.Dir = directory
	return startNativeCommand(cmd, cols, rows)
}
func startNativeCommand(cmd *exec.Cmd, cols, rows int) (Process, error) {
	if err := createPseudoConsole.Find(); err != nil {
		return nil, fmt.Errorf("ConPTY requires Windows 10 1809 or newer: %w", err)
	}
	var inRead, inWrite, outRead, outWrite syscall.Handle
	if err := syscall.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(inRead)
	if err := syscall.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		syscall.CloseHandle(inWrite)
		return nil, err
	}
	defer syscall.CloseHandle(outWrite)
	p := &windowsProcess{input: os.NewFile(uintptr(inWrite), "terminal-input"), output: os.NewFile(uintptr(outRead), "terminal-output")}
	ok := false
	defer func() {
		if !ok {
			p.Close()
		}
	}()
	hr, _, _ := createPseudoConsole.Call(uintptr(uint32(uint16(cols))|uint32(uint16(rows))<<16), uintptr(inRead), uintptr(outWrite), 0, uintptr(unsafe.Pointer(&p.console)))
	if int32(hr) < 0 {
		return nil, fmt.Errorf("CreatePseudoConsole: HRESULT %#x", hr)
	}
	var size uintptr
	initializeAttributes.Call(0, 1, 0, uintptr(unsafe.Pointer(&size)))
	attributes := make([]byte, size)
	result, _, err := initializeAttributes.Call(uintptr(unsafe.Pointer(&attributes[0])), 1, 0, uintptr(unsafe.Pointer(&size)))
	if result == 0 {
		return nil, err
	}
	defer deleteAttributes.Call(uintptr(unsafe.Pointer(&attributes[0])))
	// The attribute value is HPCON itself, not a pointer to HPCON.
	result, _, err = updateAttributes.Call(uintptr(unsafe.Pointer(&attributes[0])), 0, 0x00020016, uintptr(p.console), unsafe.Sizeof(p.console), 0, 0)
	if result == 0 {
		return nil, err
	}
	application, err := syscall.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	args := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		args[i] = syscall.EscapeArg(arg)
	}
	command, err := syscall.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return nil, err
	}
	dir, err := syscall.UTF16PtrFromString(cmd.Dir)
	if err != nil {
		return nil, err
	}
	startup := struct {
		info       syscall.StartupInfo
		attributes *byte
	}{}
	startup.info.Cb = uint32(unsafe.Sizeof(startup))
	// Leave the standard handles null so ConPTY supplies console handles instead
	// of copying the host's redirected or closed input/output handles.
	startup.info.Flags = syscall.STARTF_USESTDHANDLES
	startup.attributes = &attributes[0]
	var info syscall.ProcessInformation
	// Suspend until the process is in our job, so descendants cannot escape cleanup.
	environment := utf16.Encode([]rune(strings.Join(cmd.Environ(), "\x00") + "\x00\x00"))
	err = syscall.CreateProcess(application, command, nil, nil, false, 0x00080000|0x00000400|0x00000004, &environment[0], dir, &startup.info, &info)
	runtime.KeepAlive(attributes)
	if err != nil {
		return nil, err
	}
	p.process = info.Process
	p.pid = int(info.ProcessId)
	defer syscall.CloseHandle(info.Thread)
	job, _, err := createJob.Call(0, 0)
	if job == 0 {
		return nil, err
	}
	p.job = syscall.Handle(job)
	result, _, err = assignJob.Call(job, uintptr(p.process))
	if result == 0 {
		return nil, err
	}
	result, _, err = resumeThread.Call(uintptr(info.Thread))
	if uint32(result) == 0xffffffff {
		return nil, err
	}
	p.cols, p.rows = cols, rows
	ok = true
	p.started = true
	return p, nil
}
func (p *windowsProcess) PID() int                    { return p.pid }
func (p *windowsProcess) Read(b []byte) (int, error)  { return p.output.Read(b) }
func (p *windowsProcess) Write(b []byte) (int, error) { return p.input.Write(b) }
func (p *windowsProcess) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console == 0 {
		return ErrClosed
	}
	if p.cols == cols && p.rows == rows {
		return nil
	}
	hr, _, _ := resizePseudoConsole.Call(uintptr(p.console), uintptr(uint32(uint16(cols))|uint32(uint16(rows))<<16))
	if int32(hr) < 0 {
		return fmt.Errorf("ResizePseudoConsole: HRESULT %#x", hr)
	}
	p.cols, p.rows = cols, rows
	return nil
}
func (p *windowsProcess) Wait() error {
	_, err := syscall.WaitForSingleObject(p.process, syscall.INFINITE)
	p.mu.Lock()
	if err == nil {
		var code uint32
		err = syscall.GetExitCodeProcess(p.process, &code)
		if err == nil && code != 0 {
			err = fmt.Errorf("shell exited with code %d", code)
		}
	}
	syscall.CloseHandle(p.process)
	p.process = 0
	p.mu.Unlock()
	// ConPTY keeps its output pipe alive until the host closes the console. Read
	// continues concurrently while ClosePseudoConsole drains its final output.
	_ = p.Close()
	return err
}
func (p *windowsProcess) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		if p.job != 0 {
			terminateJob.Call(uintptr(p.job), 1)
		}
		if p.process != 0 {
			syscall.TerminateProcess(p.process, 1)
			if !p.started {
				syscall.CloseHandle(p.process)
				p.process = 0
			}
		}
		console := p.console
		p.console = 0
		p.mu.Unlock()
		p.input.Close()
		if console != 0 {
			closePseudoConsole.Call(uintptr(console))
		}
		p.output.Close()
		if p.job != 0 {
			syscall.CloseHandle(p.job)
		}
	})
	return nil
}
