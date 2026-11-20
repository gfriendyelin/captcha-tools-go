package procmon

import (
	"fmt"
	"syscall"
	"unsafe"
)

type memoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uint64
	WorkingSetSize             uint64
	QuotaPeakPagedPoolUsage    uint64
	QuotaPagedPoolUsage        uint64
	QuotaPeakNonPagedPoolUsage uint64
	QuotaNonPagedPoolUsage     uint64
	PagefileUsage              uint64
	PeakPagefileUsage          uint64
}

func RSS() (uint64, error) {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, err
	}
	library := syscall.NewLazyDLL("psapi.dll")
	procedure := library.NewProc("GetProcessMemoryInfo")
	var counters memoryCounters
	result, _, callErr := procedure.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), unsafe.Sizeof(counters))
	if result == 0 {
		return 0, fmt.Errorf("GetProcessMemoryInfo: %v", callErr)
	}
	return counters.WorkingSetSize, nil
}
