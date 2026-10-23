package terminal

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

func openPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock, number uint32
	err = ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock))
	if err == nil {
		err = ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&number))
	}
	var slave *os.File
	if err == nil {
		slave, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(number), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	}
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
