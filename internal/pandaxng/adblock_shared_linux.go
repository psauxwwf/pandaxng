//go:build linux

package pandaxng

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Linux anonymous files disappear when the last process closes its descriptor,
// even if the server is killed. Each child inherits its own descriptors.
func newSharedAdblockLists() ([]string, []*os.File, func(), error) {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		return nil, nil, nil, fmt.Errorf("shared adblock lists require /proc/self/fd: %w", err)
	}
	var args []string
	var files []*os.File
	cleanup := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	for _, list := range []struct {
		name string
		data []byte
	}{
		{"pandaxng-easylist", easyList},
		{"pandaxng-easyprivacy", easyPrivacy},
	} {
		fd, err := unix.MemfdCreate(list.name, unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
		if err != nil {
			cleanup()
			return nil, nil, nil, fmt.Errorf("create anonymous adblock list %s: %w", list.name, err)
		}
		file := os.NewFile(uintptr(fd), list.name)
		files = append(files, file)
		if _, err := file.Write(list.data); err != nil {
			cleanup()
			return nil, nil, nil, fmt.Errorf("write anonymous adblock list %s: %w", list.name, err)
		}
		if _, err := unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_SEAL|unix.F_SEAL_WRITE|unix.F_SEAL_SHRINK|unix.F_SEAL_GROW); err != nil {
			cleanup()
			return nil, nil, nil, fmt.Errorf("seal anonymous adblock list %s: %w", list.name, err)
		}
		args = append(args, "--adblock-lists", fmt.Sprintf("/proc/self/fd/%d", len(files)+2))
	}
	return args, files, cleanup, nil
}
