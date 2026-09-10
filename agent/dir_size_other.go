//go:build !linux

package agent

import (
	"io/fs"
	"os"
	"path/filepath"
)

func calculateDirSize(dirPath string, maxDepth int) uint64 {
	var totalBytes uint64
	var walk func(p string, depth int)
	walk = func(p string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			subPath := filepath.Join(p, entry.Name())
			if entry.IsDir() {
				walk(subPath, depth+1)
			} else if info, err := entry.Info(); err == nil {
				totalBytes += uint64(info.Size())
			}
		}
	}
	walk(dirPath, 0)
	return totalBytes
}