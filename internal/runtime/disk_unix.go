//go:build linux || darwin

package runtime

import (
	"log/slog"
	"path/filepath"
	"syscall"
)

// CheckDiskUsage checks disk usage for the given paths
func CheckDiskUsage(paths []string) []DiskInfo {
	var results []DiskInfo
	seen := map[string]bool{}
	for _, p := range paths {
		abs, _ := filepath.Abs(p)
		if seen[abs] {
			continue
		}
		seen[abs] = true

		var stat syscall.Statfs_t
		if err := syscall.Statfs(abs, &stat); err != nil {
			slog.Warn("disk check failed", "path", abs, "error", err)
			continue
		}

		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bavail * uint64(stat.Bsize)
		used := total - free

		results = append(results, DiskInfo{
			Path:        abs,
			TotalGB:     float64(total) / 1024 / 1024 / 1024,
			UsedGB:      float64(used) / 1024 / 1024 / 1024,
			FreeGB:      float64(free) / 1024 / 1024 / 1024,
			UsedPercent: float64(used) / float64(total) * 100,
		})
	}
	return results
}
