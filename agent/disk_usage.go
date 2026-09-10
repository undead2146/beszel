package agent

import (
	"encoding/json"
	"fmt"
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

	// Check for pre-generated disk breakdown JSON file
	customJsonPaths := []string{
		"/var/lib/beszel-agent/disk_breakdown.json",
		"/etc/beszel/disk_breakdown.json",
	}
	if hostPrefix != "" {
		customJsonPaths = append([]string{
			filepath.Join(hostPrefix, "var/lib/beszel-agent/disk_breakdown.json"),
			filepath.Join(hostPrefix, "etc/beszel/disk_breakdown.json"),
		}, customJsonPaths...)
	}

	for _, p := range customJsonPaths {
		if data, err := os.ReadFile(p); err == nil {
			var customReport diskusage.DiskUsageReport
			if err := json.Unmarshal(data, &customReport); err == nil && len(customReport.Categories) > 0 {
				customReport.TotalBytes = totalBytes
				customReport.UsedBytes = usedBytes
				customReport.FreeBytes = freeBytes
				customReport.UsedPercent = usedPercent
				customReport.RootMount = rootPath
				customReport.Timestamp = time.Now().Unix()
				dum.lastReport = &customReport
				dum.lastScanned = time.Now()
				return &customReport, nil
			}
		}
	}

	// Discover user home directories
	var userHomes []string
	if runtime.GOOS == "windows" {
		usersRoot := filepath.Join(rootPath, "Users")
		if entries, err := os.ReadDir(usersRoot); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && entry.Name() != "Default" && entry.Name() != "Public" {
					userHomes = append(userHomes, filepath.Join(usersRoot, entry.Name()))
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

	prefixPath := func(p string) string {
		if hostPrefix != "" {
			return filepath.Join(hostPrefix, strings.TrimPrefix(p, "/"))
		}
		return p
	}

	var categories []diskusage.DiskCategoryItem

	// 1. Docker & Container storage (measured via Docker API for 100% accuracy without overlay2 bloat)
	dockerMeasured := false
	if dum.agent != nil && dum.agent.dockerManager != nil {
		if dockerSize, reclaimable, err := dum.agent.dockerManager.getDiskUsage(); err == nil && dockerSize > 0 {
			dockerMeasured = true
			if usedBytes > 0 && dockerSize > usedBytes {
				dockerSize = usedBytes
			}
			pct := float64(0)
			if totalBytes > 0 {
				pct = (float64(dockerSize) / float64(totalBytes)) * 100.0
			}
			status := "clean"
			cleanup := "docker system prune -af --volumes"
			if reclaimable > 2*1024*1024*1024 {
				status = "warning"
				cleanup = fmt.Sprintf("docker system prune -af --volumes (reclaims ~%s)", formatBytes(reclaimable))
			} else if reclaimable > 500*1024*1024 {
				cleanup = fmt.Sprintf("docker image prune -af (reclaims ~%s)", formatBytes(reclaimable))
			}

			categories = append(categories, diskusage.DiskCategoryItem{
				Name:        "Docker Storage",
				Category:    "Docker & Containers",
				Path:        "/var/lib/docker",
				Size:        dockerSize,
				SizeHuman:   formatBytes(dockerSize),
				PercentDisk: pct,
				Status:      status,
				CleanupCmd:  cleanup,
				Description: "Docker container layers, images, and local volumes",
			})
		}
	}

	// Fallback for Docker if API not accessible
	if !dockerMeasured {
		realDockerPath := prefixPath("/var/lib/docker")
		if fi, err := os.Stat(realDockerPath); err == nil && fi.IsDir() {
			dockerSize := calculateDirSize(realDockerPath, 10)
			if usedBytes > 0 && dockerSize > usedBytes {
				dockerSize = usedBytes
			}
			if dockerSize >= 20*1024*1024 {
				pct := float64(0)
				if totalBytes > 0 {
					pct = (float64(dockerSize) / float64(totalBytes)) * 100.0
				}
				status := "clean"
				if dockerSize > 10*1024*1024*1024 {
					status = "warning"
				}
				categories = append(categories, diskusage.DiskCategoryItem{
					Name:        "Docker Storage",
					Category:    "Docker & Containers",
					Path:        "/var/lib/docker",
					Size:        dockerSize,
					SizeHuman:   formatBytes(dockerSize),
					PercentDisk: pct,
					Status:      status,
					CleanupCmd:  "docker system prune -af --volumes",
					Description: "Docker container layers, images, and local volumes",
				})
			}
		}
	}

	// 2. Scan homes for Workspaces, AI & Agent State, and Package Caches
	workspaceNames := []string{"workspaces", "workspace", "t3code", "projects", "code", "dev", "git"}
	for _, home := range userHomes {
		userName := filepath.Base(home)
		for _, ws := range workspaceNames {
			p := filepath.Join(home, ws)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				size := calculateDirSize(p, 15)
				if usedBytes > 0 && size > usedBytes {
					size = usedBytes
				}
				if size < 20*1024*1024 {
					continue
				}
				pct := float64(0)
				if totalBytes > 0 {
					pct = (float64(size) / float64(totalBytes)) * 100.0
				}
				status := "clean"
				if size > 30*1024*1024*1024 {
					status = "warning"
				}
				categories = append(categories, diskusage.DiskCategoryItem{
					Name:        fmt.Sprintf("Workspaces (%s/%s)", userName, ws),
					Category:    "Workspaces",
					Path:        fmt.Sprintf("~%s/%s", userName, ws),
					Size:        size,
					SizeHuman:   formatBytes(size),
					PercentDisk: pct,
					Status:      status,
					CleanupCmd:  "Review unused git branches, temporary test runs, and stale build output",
					Description: "Active code checkouts, git repositories, and dependencies",
				})
			}
		}

		// Agent state
		agentDirs := []struct {
			dir  string
			name string
			cmd  string
		}{
			{".t3", "T3 Agent & State", "rm -rf ~/.t3/worktrees/* ~/.t3/userdata/logs/*"},
			{".gemini", "Gemini CLI State", "rm -rf ~/.gemini/tmp/*"},
			{".claude", "Claude Code State", ""},
			{".agents", "Agent Workspaces & Skills", ""},
			{".local", "Local User Binaries & State", "rm -rf ~/.local/share/Trash/*"},
		}
		for _, ad := range agentDirs {
			p := filepath.Join(home, ad.dir)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				size := calculateDirSize(p, 15)
				if usedBytes > 0 && size > usedBytes {
					size = usedBytes
				}
				if size < 20*1024*1024 {
					continue
				}
				pct := float64(0)
				if totalBytes > 0 {
					pct = (float64(size) / float64(totalBytes)) * 100.0
				}
				status := "clean"
				if size > 15*1024*1024*1024 {
					status = "bloated"
				} else if size > 5*1024*1024*1024 && ad.cmd != "" {
					status = "warning"
				}
				categories = append(categories, diskusage.DiskCategoryItem{
					Name:        fmt.Sprintf("%s (%s)", ad.name, userName),
					Category:    "AI & Agent State",
					Path:        fmt.Sprintf("~%s/%s", userName, ad.dir),
					Size:        size,
					SizeHuman:   formatBytes(size),
					PercentDisk: pct,
					Status:      status,
					CleanupCmd:  ad.cmd,
					Description: "Agent toolchains, runtime checkouts, and log artifacts",
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
				size := calculateDirSize(p, 15)
				if usedBytes > 0 && size > usedBytes {
					size = usedBytes
				}
				if size < 20*1024*1024 {
					continue
				}
				pct := float64(0)
				if totalBytes > 0 {
					pct = (float64(size) / float64(totalBytes)) * 100.0
				}
				status := "clean"
				if size > 3*1024*1024*1024 {
					status = "bloated"
				} else if size > 1*1024*1024*1024 {
					status = "warning"
				}
				categories = append(categories, diskusage.DiskCategoryItem{
					Name:        fmt.Sprintf("%s (%s)", pc.name, userName),
					Category:    "Package Caches",
					Path:        fmt.Sprintf("~%s/%s", userName, pc.dir),
					Size:        size,
					SizeHuman:   formatBytes(size),
					PercentDisk: pct,
					Status:      status,
					CleanupCmd:  pc.cmd,
					Description: pc.desc,
				})
			}
		}
	}

	// 3. System targets
	sysTargets := []struct {
		path        string
		name        string
		category    string
		cleanupCmd  string
		description string
		maxDepth    int
	}{
		{"/var/log", "System Logs", "System Logs", "sudo journalctl --vacuum-size=100M && sudo journalctl --vacuum-time=7d", "Systemd journal archives and syslog logs", 10},
		{"/var/cache/apt", "APT Package Cache", "Package Caches", "sudo apt-get clean && sudo apt-get autoremove -y", "Downloaded Debian/Ubuntu .deb archives", 10},
		{"/snap", "Snap Packages", "Snaps & Packages", "snap list --all", "Canonical Snap installed versions and revisions", 10},
		{"/var/lib/snapd", "Snapd State", "Snaps & Packages", "sudo rm -rf /var/lib/snapd/cache/*", "Snap daemon download cache and mountpoints", 10},
		{"/tmp", "Temporary Files", "Temporary Files", "sudo rm -rf /tmp/* /var/tmp/*", "Ephemeral OS temp files and socket links", 10},
		{"/usr", "System Binaries & Libs", "System Binaries", "", "Core operating system binaries and shared libraries", 4},
	}

	for _, st := range sysTargets {
		realPath := prefixPath(st.path)
		if fi, err := os.Stat(realPath); err == nil && fi.IsDir() {
			size := calculateDirSize(realPath, st.maxDepth)
			if usedBytes > 0 && size > usedBytes {
				size = usedBytes
			}
			if size < 20*1024*1024 {
				continue
			}
			pct := float64(0)
			if totalBytes > 0 {
				pct = (float64(size) / float64(totalBytes)) * 100.0
			}
			status := "clean"
			if st.path == "/tmp" && size > 2*1024*1024*1024 {
				status = "warning"
			}
			if st.path == "/var/log" && size > 2*1024*1024*1024 {
				status = "warning"
			}
			categories = append(categories, diskusage.DiskCategoryItem{
				Name:        st.name,
				Category:    st.category,
				Path:        st.path,
				Size:        size,
				SizeHuman:   formatBytes(size),
				PercentDisk: pct,
				Status:      status,
				CleanupCmd:  st.cleanupCmd,
				Description: st.description,
			})
		}
	}

	// Sort by size descending
	sort.Slice(categories, func(i, j int) bool {
		return categories[i].Size > categories[j].Size
	})

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

	slog.Info("Disk usage breakdown complete", "categories", len(categories), "used", formatBytes(usedBytes))

	dum.lastReport = report
	dum.lastScanned = time.Now()
	return report, nil
}