//go:build linux || darwin

package terminal

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

type unixProcess struct {
	master     *os.File
	cmd        *exec.Cmd
	once       sync.Once
	cols, rows int
}

func ioctl(file *os.File, request uint, value unsafe.Pointer) error {
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var result syscall.Errno
	err = raw.Control(func(fd uintptr) {
		_, _, result = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(request), uintptr(value))
	})
	if err != nil {
		return err
	}
	if result != 0 {
		return result
	}
	return nil
}
func startNative(directory string, cols, rows int) (Process, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	defer slave.Close()
	cmd := shellCommand(directory)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	p := &unixProcess{master: master, cmd: cmd}
	if err = p.Resize(cols, rows); err == nil {
		err = cmd.Start()
	}
	if err != nil {
		master.Close()
		return nil, err
	}
	return p, nil
}
func (p *unixProcess) Read(b []byte) (int, error)  { return p.master.Read(b) }
func (p *unixProcess) Write(b []byte) (int, error) { return p.master.Write(b) }
func (p *unixProcess) Wait() error                 { return p.cmd.Wait() }

func (p *unixProcess) Resize(cols, rows int) error {
	if p.cols == cols && p.rows == rows {
		return nil
	}
	size := [4]uint16{uint16(rows), uint16(cols), 0, 0}
	err := ioctl(p.master, syscall.TIOCSWINSZ, unsafe.Pointer(&size))
	if err == nil {
		p.cols, p.rows = cols, rows
	}
	return err
}
func (p *unixProcess) Close() error {
	p.once.Do(func() {
		// Close the controlling terminal and kill its session's initial process group.
		// This also terminates a shell waiting for a foreground job.
		if p.cmd.Process != nil {
			var group int32
			if ioctl(p.master, syscall.TIOCGPGRP, unsafe.Pointer(&group)) == nil && group > 0 {
				_ = syscall.Kill(-int(group), syscall.SIGKILL)
			}
			_ = p.cmd.Process.Kill()
		}
		_ = p.master.Close()
	})
	return nil
}
