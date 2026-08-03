//go:build darwin || linux

package console

import "syscall"

// storageCapacity reports the capacity of the filesystem that backs path. The
// model directory can be a mounted volume, so this deliberately measures its
// filesystem instead of the process root or an arbitrary host path.
func storageCapacity(path string) (total, available int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	blockSize := int64(stat.Bsize)
	return int64(stat.Blocks) * blockSize, int64(stat.Bavail) * blockSize, nil
}
