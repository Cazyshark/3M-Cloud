package system

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"

	"github.com/multi-ops/internal/protocol"
)

// CollectMetrics gathers real-time performance metrics
func CollectMetrics() protocol.MachineMetrics {
	m := protocol.MachineMetrics{}
	m.CPUPercent = getCPUPercent()
	m.MemPercent, m.MemUsedMB = getMemPercent()
	m.DiskPercent = getDiskPercent()
	m.Load1, m.Load5, m.Load15 = getLoadAvg()
	m.NetRxBytes, m.NetTxBytes = getNetBytes()
	m.TCPConns = getTCPConns()
	m.ProcessCount = getProcessCount()
	return m
}

func getCPUPercent() float64 {
	// Use top in batch mode to get CPU usage
	c := exec.Command("bash", "-c", "top -bn1 | grep 'Cpu(s)' | awk '{print $2}'")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
	if err != nil {
		return 0
	}
	return v
}

func getMemPercent() (float64, uint64) {
	c := exec.Command("free", "-m")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0, 0
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) < 2 {
		return 0, 0
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 3 {
		return 0, 0
	}
	total, _ := strconv.ParseUint(fields[1], 10, 64)
	used, _ := strconv.ParseUint(fields[2], 10, 64)
	if total == 0 {
		return 0, 0
	}
	return float64(used) / float64(total) * 100, used
}

func getDiskPercent() float64 {
	c := exec.Command("df", "-h", "/")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) < 2 {
		return 0
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return 0
	}
	pctStr := strings.TrimSuffix(fields[4], "%")
	pct, _ := strconv.ParseFloat(pctStr, 64)
	return pct
}

func getLoadAvg() (load1, load5, load15 float64) {
	c := exec.Command("cat", "/proc/loadavg")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(out.String())
	if len(fields) >= 3 {
		load1, _ = strconv.ParseFloat(fields[0], 64)
		load5, _ = strconv.ParseFloat(fields[1], 64)
		load15, _ = strconv.ParseFloat(fields[2], 64)
	}
	return
}

func getNetBytes() (rx, tx uint64) {
	c := exec.Command("bash", "-c",
		"cat /proc/net/dev | grep -E 'eth|ens|wlan|enp' | head -1")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0, 0
	}
	line := strings.TrimSpace(out.String())
	if idx := strings.Index(line, ":"); idx >= 0 {
		fields := strings.Fields(line[idx+1:])
		if len(fields) >= 10 {
			rx, _ = strconv.ParseUint(fields[0], 10, 64)
			tx, _ = strconv.ParseUint(fields[8], 10, 64)
		}
	}
	return
}

func getTCPConns() int {
	c := exec.Command("bash", "-c", "ss -t | tail -n +2 | wc -l")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	return n
}

func getProcessCount() int {
	c := exec.Command("bash", "-c", "ps -e --no-headers | wc -l")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	return n
}
