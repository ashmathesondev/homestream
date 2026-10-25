//go:build !linux

package main

import "fmt"

func diskSpace(directory string) (total, available uint64, err error) {
	return 0, 0, fmt.Errorf("disk space reporting is supported on Linux")
}
