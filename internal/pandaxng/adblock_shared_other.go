//go:build !linux

package pandaxng

import "os"

func newSharedAdblockLists() ([]string, []*os.File, func(), error) {
	args, cleanup, err := writeAdblockLists()
	return args, nil, cleanup, err
}
