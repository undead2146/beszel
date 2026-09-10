//go:build linux

package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type inodeDevKey struct {
	dev uint64
	ino uint64
}

// calculateDirSize calculates true allocated disk space on Linux
// using inode deduplication and 512-byte block allocation (equivalent to du -s)
func calculateDirSize(dirPath string, maxDepth int) uint64 {
	seen := make(map[inodeDevKey]struct{}, 50000)
	var totalBytes uint64

	var walk func(p string, depth int)
	walk = func(p string, depth int) {
		if depth > maxDepth {
			return
		}
		// Skip live container overlay mounts to avoid walking live container filesystems
		if strings.Contains(p, "/overlay2/") && strings.HasSuffix(p, "/merged") {
			return
		}

		entries, err := os.ReadDir(p)
		if err != nil {
			return
		}
		for _, entry := range entries {
			// Skip symlinks to avoid loops
			if entry.Type()&fs.ModeSymlink != 0 {
				continue
			}

			subPath := filepath.Join(p, entry.Name())
			var stat syscall.Stat_t
			if err := syscall.Lstat(subPath, &stat); err != nil {
				continue
			}

			// Inode deduplication (prevents hard link multiplier in overlay2, pnpm, etc.)
			key := inodeDevKey{dev: uint64(stat.Dev), ino: uint64(stat.Ino)}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			if entry.IsDir() {
				walk(subPath, depth+1)
			} else {
				if stat.Blocks > 0 {
					totalBytes += uint64(stat.Blocks) * 512
				}
			}
		}
	}

	walk(dirPath, 0)
	return totalBytes
}