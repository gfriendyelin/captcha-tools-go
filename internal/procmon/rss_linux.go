package procmon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func RSS() (uint64, error) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("invalid VmRSS line %q", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		return kb * 1024, err
	}
	return 0, fmt.Errorf("VmRSS not found")
}
