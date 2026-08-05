//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// exeIDOfPath 는 경로 하나를 잰다.
func exeIDOfPath(path string) (ExeID, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return ExeID{}, err
	}
	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ExeID{}, fmt.Errorf("stat 을 해석하지 못했다(path=%q)", path)
	}
	return ExeID{
		OK: true, Dev: uint64(sys.Dev), Ino: uint64(sys.Ino),
		Size: fi.Size(), MtimeNano: fi.ModTime().UnixNano(),
	}, nil
}

// selfWatchSupported 는 이 플랫폼에서 자기 재기동이 가능한가다.
func selfWatchSupported() bool { return true }
