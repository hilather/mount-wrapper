package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ProcessMemProvider reads resident memory for a mount child process.
// Production uses DefaultProcessMem (/proc on Linux, ps on Darwin).
type ProcessMemProvider interface {
	// RSSBytes returns the process resident set size in bytes, or nil if the
	// pid is invalid, the process is gone, or the platform cannot measure.
	RSSBytes(pid int) *int64
	// RSSPeakBytes returns peak RSS when available (Linux VmHWM), else nil.
	RSSPeakBytes(pid int) *int64
}

// DefaultProcessMem implements ProcessMemProvider for the host OS.
type DefaultProcessMem struct{}

// RSSBytes implements ProcessMemProvider.
func (DefaultProcessMem) RSSBytes(pid int) *int64 {
	return readProcessRSSBytes(pid)
}

// RSSPeakBytes implements ProcessMemProvider.
func (DefaultProcessMem) RSSPeakBytes(pid int) *int64 {
	return readProcessRSSPeakBytes(pid)
}

// MapProcessMem is a test ProcessMemProvider.
type MapProcessMem struct {
	RSS     map[int]int64
	RSSPeak map[int]int64
	// MissingAsNil when true returns nil for unknown pids (default: nil).
}

// RSSBytes implements ProcessMemProvider.
func (m MapProcessMem) RSSBytes(pid int) *int64 {
	if pid <= 0 || m.RSS == nil {
		return nil
	}
	v, ok := m.RSS[pid]
	if !ok {
		return nil
	}
	return &v
}

// RSSPeakBytes implements ProcessMemProvider.
func (m MapProcessMem) RSSPeakBytes(pid int) *int64 {
	if pid <= 0 || m.RSSPeak == nil {
		return nil
	}
	v, ok := m.RSSPeak[pid]
	if !ok {
		return nil
	}
	return &v
}

func readProcessRSSBytes(pid int) *int64 {
	if pid <= 0 {
		return nil
	}
	switch runtime.GOOS {
	case "linux":
		st, err := readProcStatus(pid)
		if err != nil {
			return nil
		}
		if st.vmRSSKB < 0 {
			return nil
		}
		v := st.vmRSSKB * 1024
		return &v
	case "darwin":
		return readDarwinRSSBytes(pid)
	default:
		return nil
	}
}

func readProcessRSSPeakBytes(pid int) *int64 {
	if pid <= 0 {
		return nil
	}
	if runtime.GOOS != "linux" {
		return nil
	}
	st, err := readProcStatus(pid)
	if err != nil || st.vmHWMKB < 0 {
		return nil
	}
	v := st.vmHWMKB * 1024
	return &v
}

type procStatusMem struct {
	vmRSSKB int64 // -1 if missing
	vmHWMKB int64 // -1 if missing
}

func readProcStatus(pid int) (procStatusMem, error) {
	out := procStatusMem{vmRSSKB: -1, vmHWMKB: -1}
	path := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	return parseProcStatusMem(data), nil
}

// parseProcStatusMem extracts VmRSS / VmHWM (kB) from /proc/pid/status content.
func parseProcStatusMem(data []byte) procStatusMem {
	out := procStatusMem{vmRSSKB: -1, vmHWMKB: -1}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimSpace(rest)
		// e.g. "1234 kB"
		fields := strings.Fields(rest)
		if len(fields) < 1 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || n < 0 {
			continue
		}
		switch key {
		case "VmRSS":
			out.vmRSSKB = n
		case "VmHWM":
			out.vmHWMKB = n
		}
	}
	return out
}

// readDarwinRSSBytes uses `ps -o rss= -p PID` (RSS in KiB).
func readDarwinRSSBytes(pid int) *int64 {
	cmd := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	// ps may print leading spaces; Fields handles multi-line noise.
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || kb < 0 {
		return nil
	}
	v := kb * 1024
	return &v
}
