//go:build linux

package rootmode

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func fdPath(fd int) string { return fmt.Sprintf("/proc/self/fd/%d", fd) }

func openWorkDir() (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Reason("work_dir_denied")
	}
	for _, part := range []string{"data", "adb", "etonify", "runtime"} {
		if part == "etonify" || part == "runtime" {
			if err = unix.Mkdirat(fd, part, 0700); err != nil && err != unix.EEXIST {
				unix.Close(fd)
				return nil, Reason("work_dir_denied")
			}
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, Reason("work_dir_denied")
		}
		fd = next
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || stat.Uid != 0 || stat.Mode&0002 != 0 || ((part == "etonify" || part == "runtime") && stat.Mode&0077 != 0) {
			unix.Close(fd)
			return nil, Reason("work_dir_denied")
		}
	}
	return os.NewFile(uintptr(fd), WorkDirectory), nil
}

func lockRuntime(work *os.File) (*os.File, error) {
	fd, err := unix.Openat(int(work.Fd()), "runtime.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, Reason("lock_denied")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Mode&0077 != 0 {
		unix.Close(fd)
		return nil, Reason("lock_denied")
	}
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		unix.Close(fd)
		return nil, Reason("already_running")
	}
	return os.NewFile(uintptr(fd), "runtime.lock"), nil
}

func openAppFiles(uid int) (*os.File, error) {
	fd, err := unix.Open(AppFiles(uid), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Reason("app_directory_denied")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || int(stat.Uid) != uid || stat.Mode&0002 != 0 {
		unix.Close(fd)
		return nil, Reason("app_directory_denied")
	}
	return os.NewFile(uintptr(fd), AppFiles(uid)), nil
}

func openRelativeRegular(base int, relative string, uid int) (*os.File, error) {
	fd, err := unix.Dup(base)
	if err != nil {
		return nil, Reason("rule_file_denied")
	}
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, Reason("rule_file_denied")
		}
		fd = next
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || int(stat.Uid) != uid || stat.Mode&0002 != 0 || stat.Nlink == 0 || (i == len(parts)-1 && (stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1)) {
			unix.Close(fd)
			return nil, Reason("rule_file_denied")
		}
	}
	return os.NewFile(uintptr(fd), relative), nil
}

func snapshotRules(work *os.File, app *os.File, uid int) func(string) (string, error) {
	total := int64(0)
	return func(value string) (string, error) {
		relative, err := RuleRelativePath(value, uid)
		if err != nil {
			return "", err
		}
		file, err := openRelativeRegular(int(app.Fd()), relative, uid)
		if err != nil {
			return "", err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
		if err != nil || len(data) > 64<<20 {
			return "", Reason("rule_file_denied")
		}
		total += int64(len(data))
		if total > 256<<20 {
			return "", Reason("rule_file_denied")
		}
		sum := sha256.Sum256(data)
		name := "rule-" + hex.EncodeToString(sum[:]) + ".srs"
		target, err := os.OpenFile(filepath.Join(fdPath(int(work.Fd())), name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			target, err = openRelativeRegular(int(work.Fd()), name, 0)
			if err != nil {
				return "", err
			}
			defer target.Close()
			existing, readErr := io.ReadAll(io.LimitReader(target, (64<<20)+1))
			if readErr != nil || sha256.Sum256(existing) != sum {
				return "", Reason("rule_file_denied")
			}
		} else {
			if err != nil {
				return "", Reason("rule_file_denied")
			}
			_, err = target.Write(data)
			if err == nil {
				err = target.Sync()
			}
			target.Close()
			if err != nil {
				return "", Reason("rule_file_denied")
			}
		}
		return WorkDirectory + "/" + name, nil
	}
}

func clearSnapshots(work *os.File) error {
	entries, err := os.ReadDir(fdPath(int(work.Fd())))
	if err != nil {
		return Reason("cleanup_failed")
	}
	for _, entry := range entries {
		name := entry.Name()
		hash, ok := strings.CutPrefix(name, "rule-")
		if !ok {
			continue
		}
		hash, ok = strings.CutSuffix(hash, ".srs")
		if !ok || len(hash) != 64 {
			continue
		}
		if _, err = hex.DecodeString(hash); err != nil {
			continue
		}
		file, err := openRelativeRegular(int(work.Fd()), name, 0)
		if err != nil {
			return Reason("cleanup_failed")
		}
		file.Close()
		if unix.Unlinkat(int(work.Fd()), name, 0) != nil {
			return Reason("cleanup_failed")
		}
	}
	return nil
}
