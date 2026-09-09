package agent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/henrygd/beszel/internal/entities/diskusage"
	"github.com/shirou/gopsutil/v4/disk"
)

type DiskUsageManager struct {
	agent       *Agent
	mu          sync.Mutex
	lastReport  *diskusage.DiskUsageReport
	lastScanned time.Time
	cacheTTL    time.Duration
}

func NewDiskUsageManager(agent *Agent) *DiskUsageManager {
	return &DiskUsageManager{
		agent:    agent,
		cacheTTL: 15 * time.Minute,
	}
}

func formatBytes(bytes uint64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case bytes >= tb:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(tb))
	case bytes >= gb:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// fastDirSize calculates size with depth and item limits
func fastDirSize(path string, maxDepth int, maxItems int) uint64 {
	var totalSize uint64
	var count int

	var walk func(p string, depth int)
	walk = func(p string, depth int) {
		if depth > maxDepth || count >= maxItems {
			return
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return
		}
		for _, entry := range entries {
			count++
			if count >= maxItems {
				return
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			mode := info.Mode()
			// Skip symlinks, sockets, fifos, devices
			if mode&fs.ModeSymlink != 0 || mode&fs.ModeSocket != 0 || mode&fs.ModeNamedPipe != 0 || mode&fs.ModeDevice != 0 {
				continue
			}
			if entry.IsDir() {
				walk(filepath.Join(p, entry.Name()), depth+1)
			} else {
				totalSize += uint64(info.Size())
			}
		}
	}

	walk(path, 0)
	return totalSize
}

func (dum *DiskUsageManager) GetReport(force bool) (*diskusage.DiskUsageReport, error) {
	dum.mu.Lock()
	defer dum.mu.Unlock()

	if !force && dum.lastReport != nil && time.Since(dum.lastScanned) < dum.cacheTTL {
		return dum.lastReport, nil
	}

	slog.Info("Scanning disk usage breakdown...")

	// Detect root mount and root stats
	rootPath := "/"
	if runtime.GOOS == "windows" {
		rootPath = "C:\\"
		if sysDrive := os.Getenv("SystemDrive"); sysDrive != "" {
			rootPath = sysDrive + "\\"
		}
	}

	// Check if container host root is mapped
	hostPrefix := ""
	if fi, err := os.Stat("/host"); err == nil && fi.IsDir() {
		hostPrefix = "/host"
	}

	diskStatPath := rootPath
	if hostPrefix != "" {
		diskStatPath = hostPrefix
	}

	usage, err := disk.Usage(diskStatPath)
	var totalBytes, usedBytes, freeBytes uint64
	var usedPercent float64
	if err == nil && usage != nil {
		totalBytes = usage.Total
		usedBytes = usage.Used
		freeBytes = usage.Free
		usedPercent = usage.UsedPercent
	}

	// Check for pre-generated disk breakdown JSON file (from host helper or cron)
	customJsonPaths := []string{
		"/var/lib/beszel-agent/disk_breakdown.json",
		"/etc/beszel/disk_breakdown.json",
	}
	if hostPrefix != "" {
		customJsonPaths = append([]string{filepath.Join(hostPrefix, "var/lib/beszel-agent/disk_breakdown.json")}, customJsonPaths...)
	}

	for _, cjp := range customJsonPaths {
		if data, err := os.ReadFile(cjp); err == nil {
			var fileReport diskusage.DiskUsageReport
			if err := json.Unmarshal(data, &fileReport); err == nil && len(fileReport.Categories) > 0 {
				if fileReport.TotalBytes > 0 {
					totalBytes = fileReport.TotalBytes
					usedBytes = fileReport.UsedBytes
					freeBytes = fileReport.FreeBytes
					usedPercent = fileReport.UsedPercent
				}
				fileReport.Timestamp = time.Now().Unix()
				dum.lastReport = &fileReport
				dum.lastScanned = time.Now()
				return &fileReport, nil
			}
		}
	}

	// Discover user home directories
	var userHomes []string
	if runtime.GOOS == "windows" {
		usersDir := `C:\Users`
		if entries, err := os.ReadDir(usersDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && entry.Name() != "Public" && entry.Name() != "Default" {
					userHomes = append(userHomes, filepath.Join(usersDir, entry.Name()))
				}
			}
		}
	} else {
		homeDir := "/home"
		if hostPrefix != "" {
			homeDir = filepath.Join(hostPrefix, "home")
		}
		if entries, err := os.ReadDir(homeDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					userHomes = append(userHomes, filepath.Join(homeDir, entry.Name()))
				}
			}
		}
		rootHome := "/root"
		if hostPrefix != "" {
			rootHome = filepath.Join(hostPrefix, "root")
		}
		if fi, err := os.Stat(rootHome); err == nil && fi.IsDir() {
			userHomes = append(userHomes, rootHome)
		}
	}

	type targetScan struct {
		name        string
		category    string
		path        string
		displayPath string
		cleanupCmd  string
		description string
	}

	var targets []targetScan

	// Scan homes for workspaces, state, caches
	workspaceNames := []string{"workspaces", "workspace", "t3code", "projects", "code", "dev", "git"}
	for _, home := range userHomes {
		userName := filepath.Base(home)
		for _, ws := range workspaceNames {
			p := filepath.Join(home, ws)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				targets = append(targets, targetScan{
					name:        fmt.Sprintf("Workspaces (%s/%s)", userName, ws),
					category:    "Workspaces",
					path:        p,
					displayPath: fmt.Sprintf("~%s/%s", userName, ws),
					cleanupCmd:  "Review unused git branches, temporary test runs, and stale build output",
					description: "Development projects, git repositories, and active workspaces",
				})
			}
		}

		// Agent state
		agentDirs := []struct {
			dir  string
			name string
		}{
			{".t3", "T3 Agent & State"},
			{".gemini", "Gemini CLI State"},
			{".claude", "Claude Code State"},
			{".agents", "Agent Workspaces & Skills"},
			{".local", "Local User Binaries & State"},
		}
		for _, ad := range agentDirs {
			p := filepath.Join(home, ad.dir)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				targets = append(targets, targetScan{
					name:        fmt.Sprintf("%s (%s)", ad.name, userName),
					category:    "AI & Agent State",
					path:        p,
					displayPath: fmt.Sprintf("~%s/%s", userName, ad.dir),
					cleanupCmd:  "rm -rf ~/.t3/worktrees/* ~/.t3/userdata/logs/*",
					description: "Agent toolchains, runtime checkouts, and log artifacts",
				})
			}
		}

		// Package Caches
		pkgCaches := []struct {
			dir  string
			name string
			cmd  string
			desc string
		}{
			{".nuget", "NuGet Package Cache", "dotnet nuget locals all --clear", ".NET global NuGet package download cache"},
			{".npm", "NPM Cache", "npm cache clean --force", "Node.js global NPM cache"},
			{".pnpm-store", "pnpm Store", "pnpm store prune", "pnpm virtual store & hard links"},
			{".cargo", "Cargo Cache", "cargo clean", "Rust Cargo registry and git checkout cache"},
			{".cache", "General User Cache", "rm -rf ~/.cache/*", "User application caches and tool temp data"},
		}
		for _, pc := range pkgCaches {
			p := filepath.Join(home, pc.dir)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				targets = append(targets, targetScan{
					name:        fmt.Sprintf("%s (%s)", pc.name, userName),
					category:    "Package Caches",
					path:        p,
					displayPath: fmt.Sprintf("~%s/%s", userName, pc.dir),
					cleanupCmd:  pc.cmd,
					description: pc.desc,
				})
			}
		}
	}

	// System directories
	prefixPath := func(p string) string {
		if hostPrefix != "" {
			return filepath.Join(hostPrefix, strings.TrimPrefix(p, "/"))
		}
		return p
	}

	sysTargets := []struct {
		path        string
		name        string
		category    string
		cleanupCmd  string
		description string
	}{
		{"/var/log", "System Logs", "System Logs", "sudo journalctl --vacuum-size=100M && sudo journalctl --vacuum-time=7d", "Systemd journal archives and syslog logs"},
		{"/var/cache/apt", "APT Package Cache", "Package Caches", "sudo apt-get clean && sudo apt-get autoremove -y", "Downloaded Debian/Ubuntu .deb archives"},
		{"/var/lib/docker", "Docker Storage", "Docker & Containers", "docker system prune -af --volumes", "Docker container writable layers, unused images, and buildkit caches"},
		{"/snap", "Snap Packages", "Snaps & Packages", "snap list --all", "Canonical Snap installed versions and revisions"},
		{"/var/lib/snapd", "Snapd State", "Snaps & Packages", "sudo rm -rf /var/lib/snapd/cache/*", "Snap daemon download cache and mountpoints"},
		{"/tmp", "Temporary Files", "Temporary Files", "sudo rm -rf /tmp/* /var/tmp/*", "Ephemeral OS temp files and socket links"},
		{"/usr", "System Binaries & Libs", "System Binaries", "sudo apt-get --purge autoremove -y", "Core operating system binaries and shared libraries"},
	}

	for _, st := range sysTargets {
		realPath := prefixPath(st.path)
		if fi, err := os.Stat(realPath); err == nil && fi.IsDir() {
			targets = append(targets, targetScan{
				name:        st.name,
				category:    st.category,
				path:        realPath,
				displayPath: st.path,
				cleanupCmd:  st.cleanupCmd,
				description: st.description,
			})
		}
	}

	// Calculate sizes
	var categories []diskusage.DiskCategoryItem
	var sumCategorized uint64

	for _, tg := range targets {
		size := fastDirSize(tg.path, 4, 30000)
		if size < 20*1024*1024 { // skip items smaller than 20MB to keep list clean
			continue
		}

		sumCategorized += size

		pct := float64(0)
		if totalBytes > 0 {
			pct = (float64(size) / float64(totalBytes)) * 100.0
		}

		status := "clean"
		if size >= 3*1024*1024*1024 { // 3 GB+
			status = "bloated"
		} else if size >= 1*1024*1024*1024 { // 1 GB+
			status = "warning"
		}

		categories = append(categories, diskusage.DiskCategoryItem{
			Name:        tg.name,
			Category:    tg.category,
			Path:        tg.displayPath,
			Size:        size,
			SizeHuman:   formatBytes(size),
			PercentDisk: pct,
			Status:      status,
			CleanupCmd:  tg.cleanupCmd,
			Description: tg.description,
		})
	}

	// Sort categories by size descending
	sort.Slice(categories, func(i, j int) bool {
		return categories[i].Size > categories[j].Size
	})

	// Add Other / Uncategorized if used space exceeds categorized sum
	if usedBytes > sumCategorized && (usedBytes-sumCategorized) > 100*1024*1024 {
		otherSize := usedBytes - sumCategorized
		otherPct := float64(0)
		if totalBytes > 0 {
			otherPct = (float64(otherSize) / float64(totalBytes)) * 100.0
		}
		categories = append(categories, diskusage.DiskCategoryItem{
			Name:        "Other / Uncategorized",
			Category:    "Other",
			Path:        "(Root filesystem other files)",
			Size:        otherSize,
			SizeHuman:   formatBytes(otherSize),
			PercentDisk: otherPct,
			Status:      "clean",
			CleanupCmd:  "",
			Description: "Operating system base files, unmonitored home folders, and kernel modules",
		})
	}

	report := &diskusage.DiskUsageReport{
		TotalBytes:   totalBytes,
		UsedBytes:    usedBytes,
		FreeBytes:    freeBytes,
		UsedPercent:  usedPercent,
		RootMount:    rootPath,
		Categories:   categories,
		Timestamp:    time.Now().Unix(),
		ScannedPaths: len(categories),
	}

	dum.lastReport = report
	dum.lastScanned = time.Now()
	slog.Info("Disk usage breakdown complete", "categories", len(categories), "used", formatBytes(usedBytes))

	return report, nil
}
