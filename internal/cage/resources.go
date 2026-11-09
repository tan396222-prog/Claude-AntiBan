package cage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/host"
)

// ResourceLimits keeps the existing host-share policy while replacing the fixed
// 4 GiB / 2 CPU defaults. Zero hardware observations keep those legacy defaults.
func ResourceLimits() (memoryGiB, cpus int) {
	memoryGiB, cpus = host.MemGiB(), host.CPUs()
	if memoryGiB <= 0 {
		memoryGiB = 4
	}
	if cpus <= 0 {
		cpus = 2
	}
	return host.Cap("memory", memoryGiB, io.Discard), host.Cap("cpus", cpus, io.Discard)
}

const resourceProbe = `LC_ALL=C; export LC_ALL
awk '/^(MemTotal|MemAvailable|SwapTotal|SwapFree):/ {print $1,$2}' /proc/meminfo
awk '/^(oom_kill|balloon_inflate|balloon_deflate) / {print $1,$2}' /proc/vmstat
printf 'page_size '; getconf PAGESIZE
cat /proc/pressure/memory
head -n 1 /proc/stat
printf 'boot '; cat /proc/sys/kernel/random/boot_id`

type ResourceSample struct {
	Total             uint64  `json:"total_bytes"`
	Available         uint64  `json:"available_bytes"`
	SwapTotal         uint64  `json:"swap_total_bytes"`
	SwapFree          uint64  `json:"swap_free_bytes"`
	OOMKills          uint64  `json:"oom_kills"`
	Balloon           uint64  `json:"balloon_bytes"`
	Stall             float64 `json:"memory_stall_percent"`
	Boot              string  `json:"boot_id"`
	cpuTotal, cpuIdle uint64
}

func parseResources(data []byte) (ResourceSample, error) {
	var s ResourceSample
	var inflated, deflated, pageSize uint64
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		key := strings.TrimSuffix(f[0], ":")
		n, err := strconv.ParseUint(f[1], 10, 64)
		switch key {
		case "balloon_inflate", "balloon_deflate", "page_size":
			if err != nil || n > 1<<40 {
				return s, errors.New("invalid balloon observation")
			}
			if key == "balloon_inflate" {
				inflated = n
			}
			if key == "balloon_deflate" {
				deflated = n
			}
			if key == "page_size" {
				pageSize = n
			}
		case "MemTotal", "MemAvailable", "SwapTotal", "SwapFree", "oom_kill":
			if err != nil || n > 1<<40 {
				return s, errors.New("invalid guest resource observation")
			}
			seen[key] = true
			switch key {
			case "MemTotal":
				s.Total = n << 10
			case "MemAvailable":
				s.Available = n << 10
			case "SwapTotal":
				s.SwapTotal = n << 10
			case "SwapFree":
				s.SwapFree = n << 10
			case "oom_kill":
				s.OOMKills = n
			}
		case "some":
			for _, value := range f[1:] {
				if strings.HasPrefix(value, "avg10=") {
					s.Stall, _ = strconv.ParseFloat(strings.TrimPrefix(value, "avg10="), 64)
				}
			}
		case "boot":
			s.Boot = f[1]
		case "cpu":
			// guest/guest_nice are already included in user/nice.
			for i := 1; i < len(f) && i <= 8; i++ {
				v, e := strconv.ParseUint(f[i], 10, 64)
				if e != nil {
					return s, e
				}
				s.cpuTotal += v
				if i == 4 || i == 5 {
					s.cpuIdle += v
				}
			}
		}
	}
	if !seen["MemAvailable"] || !seen["oom_kill"] || s.Total < 256<<20 || s.Available > s.Total || s.SwapFree > s.SwapTotal || len(s.Boot) != 36 || math.IsNaN(s.Stall) || s.Stall < 0 || s.Stall > 100 {
		return s, errors.New("incomplete guest resource observation")
	}
	if inflated != 0 || deflated != 0 {
		if inflated < deflated || pageSize < 4096 || pageSize > 65536 || pageSize&(pageSize-1) != 0 {
			return s, errors.New("invalid balloon counters")
		}
		s.Balloon = (inflated - deflated) * pageSize
	}
	return s, nil
}

type ResourceStatus struct {
	Version      int             `json:"version"`
	PID          int             `json:"pid"`
	UpdatedAt    time.Time       `json:"updated_at"`
	ObservedAt   time.Time       `json:"observed_at"`
	Maximum      uint64          `json:"maximum_bytes"`
	Target       uint64          `json:"target_bytes"`
	CPUs         int             `json:"cpus"`
	CPUPercent   float64         `json:"cpu_percent"`
	Guest        *ResourceSample `json:"guest,omitempty"`
	Pressure     bool            `json:"pressure"`
	HostPressure bool            `json:"host_pressure"`
	Error        string          `json:"error,omitempty"`
}

// memoryTarget grows immediately and reclaims only after sustained headroom.
// The requested target is not a measurement of the guest or host's resident RAM.
type memoryPolicy struct {
	maximum, target uint64
	idleSince       time.Time
}

func (p *memoryPolicy) next(now time.Time, s ResourceSample, hostPressure bool) uint64 {
	const mib = uint64(1 << 20)
	used := s.Total - s.Available + s.SwapTotal - s.SwapFree
	reserve := max(uint64(1<<30), used/4)
	// Kernel bookkeeping and balloon pages are absent from MemTotal.
	desired := min(p.maximum, max(min(uint64(2<<30), p.maximum), ((used+reserve+256*mib+255*mib)/(256*mib))*(256*mib)))
	pressure := s.Available < max(uint64(512*mib), s.Total/8) || s.Stall >= 1
	if pressure || desired > p.target {
		p.idleSince = time.Time{}
		if hostPressure {
			return p.target
		}
		// A burst should not wait through several incremental allocations.
		if pressure {
			return p.maximum
		}
		return desired
	}
	if desired+512*mib > p.target {
		p.idleSince = time.Time{}
		return p.target
	}
	if p.idleSince.IsZero() {
		p.idleSince = now
	}
	if now.Sub(p.idleSince) < 30*time.Second {
		return p.target
	}
	// Never request another reclamation until the guest reflects the prior one.
	if s.Balloon < p.maximum-p.target {
		return p.target
	}
	return max(desired, p.target-256*mib)
}

// StartResourceManager belongs to the VM hostagent, so closing a window cannot
// strand a small balloon target. stop joins the loop before the driver stops.
func StartResourceManager(parent context.Context, dir string, maximum uint64, cpus int, read func(context.Context, string) ([]byte, error), set func(context.Context, uint64) error) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p := memoryPolicy{maximum: maximum, target: maximum}
		status := ResourceStatus{Version: 1, PID: os.Getpid(), Maximum: maximum, Target: maximum, CPUs: cpus}
		var previous ResourceSample
		var bootTotal uint64
		var lastError string
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			probeCtx, finish := context.WithTimeout(ctx, 3*time.Second)
			data, err := read(probeCtx, resourceProbe)
			finish()
			var sample ResourceSample
			if err == nil {
				sample, err = parseResources(data)
				if err == nil && sample.Total > maximum {
					err = errors.New("guest memory exceeds VM capacity")
				}
				if err == nil {
					if previous.Boot != sample.Boot {
						bootTotal = sample.Total
					}
					// Some Linux kernels keep MemTotal constant while balloon pages
					// count as used. Subtract them once, also accepting kernels that
					// reduce MemTotal. Balloon counters acknowledge each reclamation.
					if sample.Balloon >= bootTotal {
						err = errors.New("invalid balloon size")
					} else {
						sample.Total = min(sample.Total, bootTotal-sample.Balloon)
						if sample.Available > sample.Total {
							err = errors.New("inconsistent available memory")
						}
					}
				}
			}
			now := time.Now()
			status.UpdatedAt, status.HostPressure = now, host.MemoryPressure()
			target := p.target
			if err != nil {
				p.idleSince = time.Time{}
				status.Error = "资源采样暂时不可用，已暂停内存回收"
				// A stalled guest may itself be out of memory. Restore its budget
				// without relying on a command that needs guest RAM to execute.
				if !status.HostPressure {
					target = maximum
				}
			} else {
				status.Error = ""
				status.ObservedAt, status.Guest = now, &sample
				status.Pressure = sample.Available < max(uint64(512<<20), sample.Total/8) || sample.Stall >= 1
				if previous.Boot == sample.Boot && sample.cpuTotal > previous.cpuTotal && sample.cpuIdle >= previous.cpuIdle {
					total, idle := sample.cpuTotal-previous.cpuTotal, sample.cpuIdle-previous.cpuIdle
					status.CPUPercent = 100 * float64(total-min(idle, total)) / float64(total)
				}
				if previous.Boot == sample.Boot && sample.OOMKills > previous.OOMKills {
					diagnostics.Log("warn", "resources", fmt.Sprintf("guest OOM kills increased by %d; available=%d target=%d maximum=%d", sample.OOMKills-previous.OOMKills, sample.Available, p.target, maximum))
				}
				if previous.Boot != sample.Boot {
					p.idleSince = time.Time{}
				}
				target = p.next(now, sample, status.HostPressure)
				previous = sample
			}
			if ctx.Err() != nil {
				return
			}
			if target != p.target {
				changeCtx, finish := context.WithTimeout(ctx, 3*time.Second)
				e := set(changeCtx, target)
				finish()
				if e != nil {
					status.Error = "内存目标未能应用，保持当前配置"
				} else {
					p.target, status.Target = target, target
					diagnostics.Log("info", "resources", fmt.Sprintf("memory target=%d maximum=%d", target, maximum))
				}
			}
			if status.Error != lastError {
				if status.Error != "" {
					diagnostics.Log("warn", "resources", status.Error)
				}
				lastError = status.Error
			}
			if e := writeResourceStatus(dir, status); e != nil && lastError != "status write failed" {
				diagnostics.Log("warn", "resources", "resource status write failed")
				lastError = "status write failed"
			}
		}
	}()
	return func() { cancel(); <-done }
}

func writeResourceStatus(dir string, status ResourceStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".resources-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = host.ProtectFile(f.Name()); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(data)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "cage-resources.json"))
}

func ReadResourceStatus(dir string) (ResourceStatus, error) {
	var status ResourceStatus
	f, err := os.Open(filepath.Join(dir, "cage-resources.json"))
	if err != nil {
		return status, err
	}
	defer f.Close()
	err = json.NewDecoder(io.LimitReader(f, 16384)).Decode(&status)
	if err == nil && (status.Version != 1 || time.Since(status.UpdatedAt) > 10*time.Second || time.Until(status.UpdatedAt) > 5*time.Second) {
		err = errors.New("resource observation expired")
	}
	return status, err
}
