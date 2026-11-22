package pandaxng

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed adblock/easylist.txt
var easyList []byte

//go:embed adblock/easyprivacy.txt
var easyPrivacy []byte

// SharedAdblockLists owns list files for the lifetime of an MCP server.
type SharedAdblockLists struct {
	mu      sync.RWMutex
	args    []string
	files   []*os.File
	cleanup func()
	closed  bool
}

// NewSharedAdblockLists prepares lists once for concurrent browser processes.
func NewSharedAdblockLists() (*SharedAdblockLists, error) {
	args, files, cleanup, err := newSharedAdblockLists()
	if err != nil {
		return nil, err
	}
	return &SharedAdblockLists{args: args, files: files, cleanup: cleanup}, nil
}

// Close waits for operations using the lists before releasing them.
func (lists *SharedAdblockLists) Close() {
	lists.mu.Lock()
	defer lists.mu.Unlock()
	if !lists.closed {
		lists.closed = true
		lists.cleanup()
	}
}

func prepareAdblockLists(disabled bool, shared *SharedAdblockLists) ([]string, []*os.File, func(), error) {
	if disabled {
		return nil, nil, func() {}, nil
	}
	if shared != nil {
		shared.mu.RLock()
		if shared.closed {
			shared.mu.RUnlock()
			return nil, nil, nil, fmt.Errorf("shared adblock lists are closed")
		}
		return shared.args, shared.files, shared.mu.RUnlock, nil
	}
	args, cleanup, err := writeAdblockLists()
	return args, nil, cleanup, err
}

// writeAdblockLists creates private temporary files for Lightpanda, which
// accepts filter list paths rather than list contents.
func writeAdblockLists() ([]string, func(), error) {
	dir, err := os.MkdirTemp("", "pandaxng-adblock-")
	if err != nil {
		return nil, nil, fmt.Errorf("create adblock directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	var args []string
	for _, list := range []struct {
		name string
		data []byte
	}{
		{"easylist.txt", easyList},
		{"easyprivacy.txt", easyPrivacy},
	} {
		path := filepath.Join(dir, list.name)
		if err := os.WriteFile(path, list.data, 0o600); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("write adblock list %s: %w", list.name, err)
		}
		args = append(args, "--adblock-lists", path)
	}
	return args, cleanup, nil
}
