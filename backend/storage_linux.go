//go:build linux

package main

import "syscall"

func diskSpace(directory string) (total, available uint64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(directory, &stat); err != nil {
		return
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}
