//go:build windows

package clientruntime

import "golang.org/x/sys/windows"

func freeDiskBytes(dir string) (int64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err = windows.GetDiskFreeSpaceEx(path, &available, nil, nil); err != nil {
		return 0, err
	}
	return int64(available), nil
}
