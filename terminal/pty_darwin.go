package terminal

import (
	"os"
	"syscall"
	"unsafe"
)

func openPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	var name [128]byte
	err = ioctl(master, syscall.TIOCPTYGRANT, nil)
	if err == nil {
		err = ioctl(master, syscall.TIOCPTYUNLK, nil)
	}
	if err == nil {
		err = ioctl(master, syscall.TIOCPTYGNAME, unsafe.Pointer(&name[0]))
	}
	var slave *os.File
	if err == nil {
		n := 0
		for n < len(name) && name[n] != 0 {
			n++
		}
		slave, err = os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	}
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
