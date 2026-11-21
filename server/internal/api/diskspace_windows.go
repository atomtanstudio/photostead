//go:build windows

package api

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// diskSpace on Windows: the volume's free and total bytes, and its serial number to tell disks apart.
func diskSpace(path string) (diskRoom, bool) {
	if path == "" {
		return diskRoom{}, false
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return diskRoom{}, false
	}
	var free, total, totalFree uint64
	if windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) != nil {
		return diskRoom{}, false
	}
	var serial uint32
	if root, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`); err == nil {
		_ = windows.GetVolumeInformation(root, nil, 0, &serial, nil, nil, nil, 0)
	}
	return diskRoom{free: free, total: total, fsid: [2]int32{int32(serial), 0}}, true
}
