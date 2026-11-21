//go:build !windows

package api

import "golang.org/x/sys/unix"

func diskSpace(path string) (diskRoom, bool) {
	var st unix.Statfs_t
	if path == "" || unix.Statfs(path, &st) != nil {
		return diskRoom{}, false
	}
	bs := uint64(st.Bsize) // int64 on Linux, uint32 on macOS
	return diskRoom{free: st.Bavail * bs, total: st.Blocks * bs, fsid: [2]int32{int32(st.Fsid.Val[0]), int32(st.Fsid.Val[1])}}, true
}
