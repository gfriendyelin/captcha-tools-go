//go:build !windows && !linux

package procmon

import "fmt"

func RSS() (uint64, error) { return 0, fmt.Errorf("RSS sampling is unsupported on this platform") }
