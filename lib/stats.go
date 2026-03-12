package lib

import (
	"fmt"
    "github.com/shirou/gopsutil/v4/cpu"
    "github.com/shirou/gopsutil/v4/mem"
)

func GetSystemStats() (string, string) {
	v, err := mem.VirtualMemory()
	memUsage := "N/A"
	if err == nil {
		memUsage = fmt.Sprintf("%.1f/%.1f GB", float64(v.Used)/1e9, float64(v.Total)/1e9)
	}

	c, err := cpu.Percent(0, false)
	cpuUsage := "N/A"
	if err == nil && len(c) > 0 {
		cpuUsage = fmt.Sprintf("%.1f%%", c[0])
	}

	return cpuUsage, memUsage
}
