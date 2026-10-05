package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

func getCPUUsage() (float64, error) {
	if runtime.GOOS == "darwin" {
		return getCPUUsageMac()
	} else if runtime.GOOS == "linux" {
		return getCPUUsageLinux()
	}
	return 0, &unsupportedOSError{OS: runtime.GOOS}
}

func getCPUUsageMac() (float64, error) {
	// `top -l 1` reports CPU usage since boot (≈meaningless). `-l 2` takes two
	// samples; the SECOND "CPU usage:" line is the real-time delta between them,
	// which matches Activity Monitor.
	cmd := exec.Command("top", "-l", "2", "-n", "0")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	lastUsage := -1.0
	for _, line := range lines {
		if !strings.Contains(line, "CPU usage:") {
			continue
		}
		// "CPU usage: 12.45% user, 5.23% sys, 82.32% idle"
		idleIndex := strings.Index(line, "% idle")
		if idleIndex == -1 {
			continue
		}
		start := idleIndex - 1
		for start > 0 && (line[start] >= '0' && line[start] <= '9' || line[start] == '.') {
			start--
		}
		start++
		idle, err := strconv.ParseFloat(strings.TrimSpace(line[start:idleIndex]), 64)
		if err != nil {
			continue
		}
		usage := 100.0 - idle
		if usage < 0 {
			usage = 0
		}
		if usage > 100 {
			usage = 100
		}
		lastUsage = usage // keep the last sample (the real-time one)
	}
	if lastUsage < 0 {
		return 0, errCPUUsageParse
	}
	return lastUsage, nil
}

func getCPUUsageLinux() (float64, error) {
	// /proc/stat holds cumulative counters since boot. A single read yields the
	// average since boot — not current load. Sample twice and use the delta.
	total1, idle1, err := readProcStatCPU()
	if err != nil {
		return 0, err
	}
	time.Sleep(250 * time.Millisecond)
	total2, idle2, err := readProcStatCPU()
	if err != nil {
		return 0, err
	}

	totalDelta := float64(total2 - total1)
	idleDelta := float64(idle2 - idle1)
	if totalDelta <= 0 {
		return 0, nil
	}
	usage := 100.0 * (1.0 - idleDelta/totalDelta)
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, nil
}

func readProcStatCPU() (total, idle uint64, err error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0, errProcStatRead
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 8 {
		return 0, 0, errProcStatFormat
	}
	// fields: cpu user nice system idle iowait irq softirq ...
	for i := 1; i < len(fields); i++ {
		val, e := strconv.ParseUint(fields[i], 10, 64)
		if e != nil {
			continue
		}
		total += val
		if i == 4 { // idle column
			idle = val
		}
	}
	return total, idle, nil
}

// getLoadAverage returns load average values (1min, 5min, 15min) like htop
func getLoadAverage() ([3]float64, error) {
	if runtime.GOOS == "darwin" {
		return getLoadAverageMac()
	} else if runtime.GOOS == "linux" {
		return getLoadAverageLinux()
	}
	return [3]float64{0, 0, 0}, &unsupportedOSError{OS: runtime.GOOS}
}

func getLoadAverageMac() ([3]float64, error) {
	cmd := exec.Command("sysctl", "vm.loadavg")
	output, err := cmd.Output()
	if err != nil {
		return [3]float64{0, 0, 0}, err
	}

	// Format: "vm.loadavg: { 1.23 0.45 0.12 }"
	parts := strings.Fields(string(output))
	if len(parts) < 5 {
		return [3]float64{0, 0, 0}, errLoadavgParse
	}

	var load [3]float64
	for i := 0; i < 3; i++ {
		val, err := strconv.ParseFloat(parts[i+2], 64)
		if err != nil {
			return [3]float64{0, 0, 0}, err
		}
		load[i] = val
	}

	return load, nil
}

func getLoadAverageLinux() ([3]float64, error) {
	file, err := os.Open("/proc/loadavg")
	if err != nil {
		return [3]float64{0, 0, 0}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return [3]float64{0, 0, 0}, errLoadavgRead
	}

	line := scanner.Text()
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return [3]float64{0, 0, 0}, errLoadavgFormat
	}

	var load [3]float64
	for i := 0; i < 3; i++ {
		val, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return [3]float64{0, 0, 0}, err
		}
		load[i] = val
	}

	return load, nil
}

func getRAMUsage() (float64, error) {
	if runtime.GOOS == "darwin" {
		return getRAMUsageMac()
	} else if runtime.GOOS == "linux" {
		return getRAMUsageLinux()
	}
	return 0, &unsupportedOSError{OS: runtime.GOOS}
}

func getRAMTotal() (float64, error) {
	if runtime.GOOS == "darwin" {
		return getRAMTotalMac()
	} else if runtime.GOOS == "linux" {
		return getRAMTotalLinux()
	}
	return 0, &unsupportedOSError{OS: runtime.GOOS}
}

func getRAMTotalMac() (float64, error) {
	cmd := exec.Command("sysctl", "hw.memsize")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	// Format: "hw.memsize: 17179869184"
	parts := strings.Fields(string(output))
	if len(parts) < 2 {
		return 0, errMemsizeParse
	}

	memsize, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}

	// Convert bytes to GB
	return float64(memsize) / (1024 * 1024 * 1024), nil
}

func getRAMTotalLinux() (float64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// Value is in KB
				memKB, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					return 0, err
				}
				// Convert KB to GB
				return float64(memKB) / (1024 * 1024), nil
			}
		}
	}
	return 0, errMemTotalMissing
}

func getRAMUsageMac() (float64, error) {
	// Summing per-process RSS double-counts shared pages and wildly overcounts.
	// Activity Monitor's "Memory Used" ≈ (active + wired + compressed) pages.
	// vm_stat gives those page counts; multiply by the page size.
	memTotal, err := getRAMTotalMac()
	if err != nil {
		return 0, err
	}
	memTotalBytes := memTotal * 1024 * 1024 * 1024

	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, err
	}

	pageSize := 4096.0 // default; vm_stat header states the real size
	var active, wired, compressed float64
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "page size of") {
			// "Mach Virtual Memory Statistics: (page size of 16384 bytes)"
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "of" && i+1 < len(fields) {
					if v, e := strconv.ParseFloat(fields[i+1], 64); e == nil {
						pageSize = v
					}
				}
			}
			continue
		}
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		valStr := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[colon+1:]), "."))
		val, e := strconv.ParseFloat(valStr, 64)
		if e != nil {
			continue
		}
		switch key {
		case "Pages active":
			active = val
		case "Pages wired down":
			wired = val
		case "Pages occupied by compressor":
			compressed = val
		}
	}

	usedBytes := (active + wired + compressed) * pageSize
	if memTotalBytes <= 0 {
		return 0, errMemTotalInvalid
	}
	usage := 100.0 * usedBytes / memTotalBytes
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, nil
}

func getRAMUsageLinux() (float64, error) {
	// Used = MemTotal - MemAvailable. MemAvailable already accounts for
	// reclaimable page cache, so this matches `free`/htop (which exclude cache),
	// unlike summing per-process RSS (which double-counts shared pages).
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	var memTotalKB, memAvailableKB float64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		val, e := strconv.ParseFloat(fields[1], 64) // value in KB
		if e != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			memTotalKB = val
		case "MemAvailable:":
			memAvailableKB = val
		}
	}
	if memTotalKB <= 0 {
		return 0, errMemTotalInvalid
	}
	usage := 100.0 * (memTotalKB - memAvailableKB) / memTotalKB
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, nil
}

func getDiskUsage() (float64, error) {
	if runtime.GOOS == "darwin" {
		return getDiskUsageMac()
	} else if runtime.GOOS == "linux" {
		return getDiskUsageLinux()
	}
	return 0, &unsupportedOSError{OS: runtime.GOOS}
}

// getDiskTotal returns total disk space in GB
func getDiskTotal() (float64, error) {
	if runtime.GOOS == "darwin" {
		return getDiskTotalMac()
	} else if runtime.GOOS == "linux" {
		return getDiskTotalLinux()
	}
	return 0, &unsupportedOSError{OS: runtime.GOOS}
}

// getDiskFree returns free disk space in GB
func getDiskFree() (float64, error) {
	if runtime.GOOS == "darwin" {
		return getDiskFreeMac()
	} else if runtime.GOOS == "linux" {
		return getDiskFreeLinux()
	}
	return 0, &unsupportedOSError{OS: runtime.GOOS}
}

func getDiskTotalMac() (float64, error) {
	// Use diskutil to get actual physical disk size (matches macOS Settings)
	cmd := exec.Command("diskutil", "info", "/")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	// Look for "Container Total Space:" or "Disk Size:" line
	lines := strings.Split(string(output), "\n")
	var totalBytes uint64
	for _, line := range lines {
		if strings.Contains(line, "Container Total Space:") {
			// Format: "Container Total Space:     494.4 GB (494384795648 Bytes)"
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "GB" && i > 0 {
					// Previous field should be the size
					sizeStr := fields[i-1]
					sizeGB, err := strconv.ParseFloat(sizeStr, 64)
					if err == nil {
						return sizeGB, nil
					}
				}
			}
		} else if strings.Contains(line, "Disk Size:") && totalBytes == 0 {
			// Format: "Disk Size:                 494.4 GB (494384795648 Bytes)"
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "GB" && i > 0 {
					sizeStr := fields[i-1]
					sizeGB, err := strconv.ParseFloat(sizeStr, 64)
					if err == nil {
						return sizeGB, nil
					}
				}
			}
			// Fallback: parse bytes from parentheses
			bytesMatch := strings.Index(line, "(")
			if bytesMatch > 0 {
				bytesPart := line[bytesMatch+1:]
				bytesFields := strings.Fields(bytesPart)
				if len(bytesFields) > 0 {
					bytesStr := bytesFields[0]
					bytes, err := strconv.ParseUint(bytesStr, 10, 64)
					if err == nil {
						totalBytes = bytes
					}
				}
			}
		}
	}

	// If we found bytes but not GB, convert
	if totalBytes > 0 {
		return float64(totalBytes) / (1024 * 1024 * 1024), nil
	}

	return 0, errDiskutilParse
}

func getDiskTotalLinux() (float64, error) {
	cmd := exec.Command("df", "-BG", "/")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, errDfParse
	}

	// Parse the second line (first line is header)
	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return 0, errDfFormat
	}

	// First field after filesystem is total size (remove 'G' suffix)
	sizeStr := strings.TrimSuffix(fields[1], "G")
	totalGB, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		return 0, err
	}

	return totalGB, nil
}

func getDiskUsageMac() (float64, error) {
	// Use diskutil to get actual physical disk usage (matches macOS Settings)
	cmd := exec.Command("diskutil", "info", "/")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	var totalBytes, freeBytes uint64
	var totalGB, freeGB float64

	for _, line := range lines {
		// Get Container Total Space (physical disk size)
		if strings.Contains(line, "Container Total Space:") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "GB" && i > 0 {
					sizeStr := fields[i-1]
					val, err := strconv.ParseFloat(sizeStr, 64)
					if err == nil {
						totalGB = val
					}
				}
			}
			// Also try to get bytes
			bytesMatch := strings.Index(line, "(")
			if bytesMatch > 0 {
				bytesPart := line[bytesMatch+1:]
				bytesFields := strings.Fields(bytesPart)
				if len(bytesFields) > 0 {
					bytesStr := bytesFields[0]
					bytes, err := strconv.ParseUint(bytesStr, 10, 64)
					if err == nil {
						totalBytes = bytes
					}
				}
			}
		}
		// Get Container Free Space
		if strings.Contains(line, "Container Free Space:") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "GB" && i > 0 {
					sizeStr := fields[i-1]
					val, err := strconv.ParseFloat(sizeStr, 64)
					if err == nil {
						freeGB = val
					}
				}
			}
			// Also try to get bytes
			bytesMatch := strings.Index(line, "(")
			if bytesMatch > 0 {
				bytesPart := line[bytesMatch+1:]
				bytesFields := strings.Fields(bytesPart)
				if len(bytesFields) > 0 {
					bytesStr := bytesFields[0]
					bytes, err := strconv.ParseUint(bytesStr, 10, 64)
					if err == nil {
						freeBytes = bytes
					}
				}
			}
		}
	}

	if totalGB > 0 && freeGB > 0 {
		usedGB := totalGB - freeGB
		usage := (usedGB / totalGB) * 100.0
		if usage < 0 {
			usage = 0
		}
		if usage > 100 {
			usage = 100
		}
		return usage, nil
	}

	// Fallback to bytes calculation
	if totalBytes > 0 && freeBytes > 0 {
		usedBytes := totalBytes - freeBytes
		usage := (float64(usedBytes) / float64(totalBytes)) * 100.0
		if usage < 0 {
			usage = 0
		}
		if usage > 100 {
			usage = 100
		}
		return usage, nil
	}

	// Fallback to df if diskutil fails
	cmd = exec.Command("df", "-h", "/")
	output, err = cmd.Output()
	if err != nil {
		return 0, err
	}

	lines = strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, errDiskUsageParse
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return 0, errDiskUsageParse
	}

	// Format: Filesystem Size Used Avail Capacity iused ifree %iused Mounted on
	// We want the Capacity field (e.g., "45%")
	capacityStr := fields[4]
	capacityStr = strings.TrimSuffix(capacityStr, "%")
	usage, err := strconv.ParseFloat(capacityStr, 64)
	if err != nil {
		return 0, err
	}

	return usage, nil
}

func getDiskFreeMac() (float64, error) {
	// Use diskutil to get actual free space (matches macOS Settings)
	cmd := exec.Command("diskutil", "info", "/")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	var freeBytes uint64
	var freeGB float64

	for _, line := range lines {
		// Get Container Free Space
		if strings.Contains(line, "Container Free Space:") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "GB" && i > 0 {
					sizeStr := fields[i-1]
					val, err := strconv.ParseFloat(sizeStr, 64)
					if err == nil {
						freeGB = val
					}
				}
			}
			// Also try to get bytes for precision
			bytesMatch := strings.Index(line, "(")
			if bytesMatch > 0 {
				bytesPart := line[bytesMatch+1:]
				bytesFields := strings.Fields(bytesPart)
				if len(bytesFields) > 0 {
					bytesStr := bytesFields[0]
					bytes, err := strconv.ParseUint(bytesStr, 10, 64)
					if err == nil {
						freeBytes = bytes
					}
				}
			}
		}
	}

	// Prefer GB value if available, otherwise convert from bytes
	if freeGB > 0 {
		return freeGB, nil
	}
	if freeBytes > 0 {
		return float64(freeBytes) / (1024 * 1024 * 1024), nil
	}

	// Fallback to df if diskutil fails
	cmd = exec.Command("df", "-g", "/")
	output, err = cmd.Output()
	if err != nil {
		return 0, err
	}

	lines = strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, errDfParse
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return 0, errDfFormat
	}

	// Available space is in field 3 (0-indexed: 2)
	availableGB, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return 0, err
	}

	return availableGB, nil
}

func getDiskFreeLinux() (float64, error) {
	cmd := exec.Command("df", "-BG", "/")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, errDfParse
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return 0, errDfFormat
	}

	// Available space is in field 3 (0-indexed: 2), remove 'G' suffix
	sizeStr := strings.TrimSuffix(fields[2], "G")
	availableGB, err := strconv.ParseFloat(sizeStr, 64)
	if err != nil {
		return 0, err
	}

	return availableGB, nil
}

func getDiskUsageLinux() (float64, error) {
	cmd := exec.Command("df", "-h", "/")
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, errDiskUsageParse
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return 0, errDiskUsageParse
	}

	// Format: Filesystem Size Used Avail Use% Mounted on
	// We want the Use% field (e.g., "45%")
	usageStr := fields[4]
	usageStr = strings.TrimSuffix(usageStr, "%")
	usage, err := strconv.ParseFloat(usageStr, 64)
	if err != nil {
		return 0, err
	}

	return usage, nil
}

// Network state for calculating speed
var (
	networkState struct {
		sync.Mutex
		bytesIn   uint64
		bytesOut  uint64
		timestamp time.Time
	}
)

// getNetworkMetrics returns network metrics and metadata
// Returns: metrics map (network_bytes_in, network_bytes_out, network_speed_in, network_speed_out)
//
//	metadata map (network_bytes_in_total, network_bytes_out_total)
func getNetworkMetrics() (map[string]float64, map[string]float64, error) {
	var bytesIn, bytesOut uint64
	var err error

	if runtime.GOOS == "darwin" {
		bytesIn, bytesOut, err = getNetworkBytesMac()
	} else if runtime.GOOS == "linux" {
		bytesIn, bytesOut, err = getNetworkBytesLinux()
	} else {
		return nil, nil, &unsupportedOSError{OS: runtime.GOOS}
	}

	if err != nil {
		return nil, nil, err
	}

	metrics := make(map[string]float64)
	metadata := make(map[string]float64)

	metadata["network_bytes_in_total"] = float64(bytesIn)
	metadata["network_bytes_out_total"] = float64(bytesOut)

	networkState.Lock()
	defer networkState.Unlock()

	now := time.Now()
	var speedIn, speedOut float64
	if networkState.timestamp.IsZero() {
		// Speeds need two readings: the first call only records the counters.
		networkState.bytesIn = bytesIn
		networkState.bytesOut = bytesOut
		networkState.timestamp = now
		speedIn = 0
		speedOut = 0
	} else {
		timeDiff := now.Sub(networkState.timestamp).Seconds()
		if timeDiff > 0 {
			bytesInDiff := bytesIn - networkState.bytesIn
			bytesOutDiff := bytesOut - networkState.bytesOut
			speedIn = float64(bytesInDiff) / timeDiff
			speedOut = float64(bytesOutDiff) / timeDiff

			// Handle counter wraparound (if bytes decreased, assume it wrapped)
			if speedIn < 0 {
				speedIn = 0
			}
			if speedOut < 0 {
				speedOut = 0
			}
		} else {
			speedIn = 0
			speedOut = 0
		}

		networkState.bytesIn = bytesIn
		networkState.bytesOut = bytesOut
		networkState.timestamp = now
	}

	// Store speeds in metadata (in MB/s - megabytes per second)
	metadata["network_speed_in_mb_per_s"] = speedIn / (1024 * 1024)
	metadata["network_speed_out_mb_per_s"] = speedOut / (1024 * 1024)

	// Add ping measurement to check internet connectivity/latency
	latency, errPing := pingPublic()
	if errPing == nil && latency >= 0 {
		metadata["network_ping_latency_ms"] = latency
		metadata["network_ping_success"] = 1.0
	} else {
		// Log ping error silently to avoid spamming, but we record the failure
		metadata["network_ping_latency_ms"] = 0.0 // Default when failed so the frontend doesn't crash
		metadata["network_ping_success"] = 0.0
	}

	// For the main network metric, we'll use combined speed (in MB/s)
	// This represents total network activity
	metrics["network"] = (speedIn + speedOut) / (1024 * 1024) // Convert to MB/s

	return metrics, metadata, nil
}

func pingPublic() (float64, error) {
	host := "1.1.1.1" // Reliable public endpoint for internet connectivity check
	start := time.Now()
	var cmd *exec.Cmd

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "ping", "-n", "1", "-w", "2000", host)
	} else if runtime.GOOS == "darwin" {
		// macOS has no -W; -t is the overall timeout in seconds.
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-t", "2", host)
	} else {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", host) // Linux ping takes seconds for -W
	}

	out, err := cmd.Output()
	if err != nil {
		return -1, err
	}
	// Prefer the real ICMP RTT ("time=8.1 ms") over wall-clock, which includes
	// the ping subprocess fork/exec overhead.
	if m := pingRTTAgentRe.FindStringSubmatch(string(out)); len(m) >= 2 {
		if v, e := strconv.ParseFloat(m[1], 64); e == nil {
			return v, nil
		}
	}
	return float64(time.Since(start).Milliseconds()), nil
}

var pingRTTAgentRe = regexp.MustCompile(`time[=<]\s*([0-9.]+)\s*ms`)

func getNetworkBytesLinux() (uint64, uint64, error) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	var totalBytesIn, totalBytesOut uint64
	scanner := bufio.NewScanner(file)

	// Skip first two lines (header)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue
		}

		line := scanner.Text()
		// Format: interface: bytes_in packets_in errs_in drop_in ... bytes_out packets_out ...
		// Example: eth0: 12345678 12345 0 0 ... 98765432 98765 ...

		// Find the colon separator
		colonIndex := strings.Index(line, ":")
		if colonIndex == -1 {
			continue
		}

		// Skip loopback interface
		interfaceName := strings.TrimSpace(line[:colonIndex])
		if interfaceName == "lo" {
			continue
		}

		// Parse the rest after colon
		fields := strings.Fields(line[colonIndex+1:])
		if len(fields) < 9 {
			continue
		}

		// Field 0: bytes received (RX bytes)
		// Field 8: bytes transmitted (TX bytes)
		bytesIn, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		bytesOut, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			continue
		}

		totalBytesIn += bytesIn
		totalBytesOut += bytesOut
	}

	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}

	return totalBytesIn, totalBytesOut, nil
}

func getNetworkBytesMac() (uint64, uint64, error) {
	// Use netstat to get network statistics
	cmd := exec.Command("netstat", "-ib")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	var totalBytesIn, totalBytesOut uint64
	lines := strings.Split(string(output), "\n")

	// Skip header line
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		// Skip loopback interface
		interfaceName := fields[0]
		if interfaceName == "lo0" || strings.HasPrefix(interfaceName, "lo") {
			continue
		}

		// Field 6: Ibytes (input bytes)
		// Field 9: Obytes (output bytes)
		bytesIn, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			continue
		}
		bytesOut, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}

		totalBytesIn += bytesIn
		totalBytesOut += bytesOut
	}

	return totalBytesIn, totalBytesOut, nil
}
