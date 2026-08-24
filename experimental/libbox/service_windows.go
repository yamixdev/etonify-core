package libbox

import (
	"os"
	"syscall"
)

func dup(fd int) (nfd int, err error) {
	return 0, os.ErrInvalid
}

func closeFd(fd int) error {
	return syscall.Close(syscall.Handle(fd))
}
