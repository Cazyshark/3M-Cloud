package system

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/multi-ops/internal/protocol"
)

func CollectAgentInfo(agentID string) protocol.AgentInfo {
	info := protocol.AgentInfo{
		AgentID: agentID,
		Status:  "online",
		OS:      runtime.GOOS,
	}

	info.Hostname = run("hostname")
	info.PublicIP = getPublicIP()
	info.Location = getGeoLocation(info.PublicIP)
	info.DistributorID = getLSBField("Distributor ID")
	info.Description = getLSBField("Description")
	info.Release = getLSBField("Release")
	info.Codename = getLSBField("Codename")
	info.CPUModel, info.CPUCores = getCPUInfo()
	info.MemoryMB = getMemoryMB()
	info.DiskGB, info.DiskUsed = getDiskInfo()
	info.Uptime = getUptimeSeconds()
	info.LastSeen = time.Now().Unix()

	return info
}

func run(cmd string, args ...string) string {
	c := exec.Command(cmd, args...)
	var out bytes.Buffer
	c.Stdout = &out
	c.Run()
	return strings.TrimSpace(out.String())
}

func getPublicIP() string {
	for _, url := range []string{
		"https://api.ipify.org",
		"https://ifconfig.me",
		"https://icanhazip.com",
	} {
		c := exec.Command("curl", "-s", "--connect-timeout", "3", url)
		var out bytes.Buffer
		c.Stdout = &out
		if err := c.Run(); err == nil {
			ip := strings.TrimSpace(out.String())
			if ip != "" {
				return ip
			}
		}
	}
	return "unknown"
}

type geoResponse struct {
	Country   string `json:"country"`
	RegionName string `json:"regionName"`
	City      string `json:"city"`
}

func getGeoLocation(ip string) string {
	c := exec.Command("curl", "-s", "--connect-timeout", "3",
		fmt.Sprintf("http://ip-api.com/json/%s?fields=country,regionName,city", ip))
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return "unknown"
	}

	var geo geoResponse
	if err := json.Unmarshal(out.Bytes(), &geo); err != nil {
		return "unknown"
	}

	var parts []string
	for _, s := range []string{geo.Country, geo.RegionName, geo.City} {
		if s != "" && s != "undefined" {
			parts = append(parts, s)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, " - ")
	}
	return "unknown"
}

func getLSBField(field string) string {
	c := exec.Command("lsb_release", "-a")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return ""
	}
	for _, line := range strings.Split(out.String(), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == field {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func getCPUInfo() (string, int) {
	model := ""
	cores := 0
	c := exec.Command("lscpu")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return "unknown", 1
	}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		// Match both English "Model name:" and Chinese "型号名称："
		if after, ok := trimPrefixAny(line, "Model name:", "型号名称：", "Model name："); ok {
			model = strings.TrimSpace(after)
		}
		if after, ok := trimPrefixAny(line, "CPU(s):", "CPU：", "CPU:"); ok {
			cores, _ = strconv.Atoi(strings.TrimSpace(after))
		}
	}
	if model == "" {
		model = "unknown"
	}
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	return model, cores
}

// trimPrefixAny tries multiple prefixes and returns the trimmed string
func trimPrefixAny(s string, prefixes ...string) (string, bool) {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return strings.TrimPrefix(s, p), true
		}
	}
	return s, false
}

func getMemoryMB() uint64 {
	c := exec.Command("free", "-m")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 2 {
			val, _ := strconv.ParseUint(fields[1], 10, 64)
			return val
		}
	}
	return 0
}

func getDiskInfo() (totalGB, usedGB uint64) {
	c := exec.Command("df", "-BG", "/")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0, 0
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 3 {
			t := strings.TrimSuffix(fields[1], "G")
			u := strings.TrimSuffix(fields[2], "G")
			totalGB, _ = strconv.ParseUint(t, 10, 64)
			usedGB, _ = strconv.ParseUint(u, 10, 64)
		}
	}
	return
}

func getUptimeSeconds() uint64 {
	c := exec.Command("cat", "/proc/uptime")
	var out bytes.Buffer
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return 0
	}
	parts := strings.Fields(out.String())
	if len(parts) >= 1 {
		f, _ := strconv.ParseFloat(parts[0], 64)
		return uint64(f)
	}
	return 0
}
