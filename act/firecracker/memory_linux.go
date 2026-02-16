//go:build linux

package firecracker

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const kbPerMB = 1024

type realMemorySystem struct{}

// GetAvailableMemoryMB returns currently available memory in MB.
func (s *realMemorySystem) GetAvailableMemoryMB() (int64, error) {
	return readMemInfo("MemAvailable")
}

// GetTotalMemoryMB returns total system memory in MB.
func (s *realMemorySystem) GetTotalMemoryMB() (int64, error) {
	return readMemInfo("MemTotal")
}

func readMemInfo(field string) (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, field+":") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return 0, fmt.Errorf("invalid %s line: %s", field, line)
			}
			kb, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse %s: %w", field, err)
			}
			return kb / kbPerMB, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("%s not found in /proc/meminfo", field)
}
