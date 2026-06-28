//go:build windows

package runtime

import (
	"log/slog"
	"path/filepath"
	"syscall"
	"unsafe"
)

// CheckDiskUsage checks disk usage for the given paths (Windows implementation)
func CheckDiskUsage(paths []string) []DiskInfo {
	var results []DiskInfo
	seen := map[string]bool{}

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")

	for _, p := range paths {
		abs, _ := filepath.Abs(p)
		if seen[abs] {
			continue
		}
		seen[abs] = true

		root := filepath.VolumeName(abs) + `\`
		var freeBytes, totalBytes, totalFreeBytes uint64
		ret, _, _ := getDiskFreeSpaceEx.Call(
			uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(root))),
			uintptr(unsafe.Pointer(&freeBytes)),
			uintptr(unsafe.Pointer(&totalBytes)),
			uintptr(unsafe.Pointer(&totalFreeBytes)),
		)
		if ret == 0 {
			slog.Warn("disk check failed", "path", abs)
			continue
		}

		used := totalBytes - freeBytes
		results = append(results, DiskInfo{
			Path:        abs,
			TotalGB:     float64(totalBytes) / 1024 / 1024 / 1024,
			UsedGB:      float64(used) / 1024 / 1024 / 1024,
			FreeGB:      float64(freeBytes) / 1024 / 1024 / 1024,
			UsedPercent: float64(used) / float64(totalBytes) * 100,
		})
	}
	return results
}
