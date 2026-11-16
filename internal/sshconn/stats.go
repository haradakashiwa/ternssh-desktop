package sshconn

import (
	"bufio"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// A Linux server's status is read from /proc and df over the SSH
// connection, every few seconds: what is used of its CPU, memory and
// disks, how fast each of its network interfaces moves bytes, and which
// processes use the most CPU.

// statsCommand prints what a sample reads, each part after a line @@name.
const statsCommand = `export LC_ALL=C; uname -n | sed 's/^/@@host /'; ` +
	`for f in stat meminfo net/dev loadavg uptime; do echo "@@$f"; cat /proc/$f 2>/dev/null; done; ` +
	`echo @@pagesize; getconf PAGESIZE 2>/dev/null; ` +
	`echo @@procs; cat /proc/[0-9]*/stat 2>/dev/null; ` +
	`echo @@df; df -kP 2>/dev/null`

// Disk is a mounted file system.
type Disk struct {
	Mount      string
	Device     string
	Size, Used uint64 // bytes
}

// sample is what one run of statsCommand read.
type sample struct {
	at                  time.Time
	host                string
	cpuBusy, cpuTotal   uint64 // jiffies since boot
	cores               int
	memTotal, memAvail  uint64 // bytes
	swapTotal, swapFree uint64
	rx, tx              uint64               // bytes since boot, of every interface but lo
	ifaces              map[string][2]uint64 // bytes received and sent, by interface
	load                [3]float64
	uptime              time.Duration
	disks               []Disk
	pageSize            uint64
	procs               map[int]procTimes
}

// procTimes is a process at a sample.
type procTimes struct {
	name  string
	ticks uint64 // CPU time used, user and system, in jiffies
	rss   uint64 // resident pages
}

// Iface is how fast a network interface moves bytes, per second.
type Iface struct {
	Name   string
	RX, TX float64
}

// Proc is a process and what it uses: CPU as top shows it, 100 for all of
// one core, and its resident memory in bytes.
type Proc struct {
	PID  int
	Name string
	CPU  float64
	Mem  uint64
}

// TopProcs is how many processes Stats keeps, those using the most CPU.
const TopProcs = 10

// Stats is a server's status at a time: rates and shares over the time
// since the sample before.
type Stats struct {
	At     time.Time
	Host   string
	CPU    float64 // the share of the CPU busy, 0 to 1
	Cores  int
	Load   [3]float64
	Uptime time.Duration

	MemUsed, MemTotal   uint64 // bytes
	SwapUsed, SwapTotal uint64
	Disks               []Disk

	RX, TX float64 // bytes per second, received and sent, of every interface but lo
	Ifaces []Iface // by name, lo left out
	Procs  []Proc  // the TopProcs using the most CPU, then memory
}

// Net returns the rates of the interface name, or of every one when it is
// empty or gone.
func (s Stats) Net(name string) (rx, tx float64) {
	for _, i := range s.Ifaces {
		if i.Name == name {
			return i.RX, i.TX
		}
	}
	return s.RX, s.TX
}

// errNotLinux is the error of a server without /proc.
var errNotLinux = errors.New("无法读取服务器状态：只支持 Linux 服务器")

// parseSample reads the output of statsCommand.
func parseSample(out string, at time.Time) (sample, error) {
	s := sample{at: at, ifaces: map[string][2]uint64{}, procs: map[int]procTimes{}, pageSize: 4096}
	part := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sawStat := false
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "@@"); ok {
			part, _, _ = strings.Cut(rest, " ")
			if part == "host" {
				s.host = strings.TrimSpace(strings.TrimPrefix(rest, "host"))
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch part {
		case "stat":
			switch {
			case f[0] == "cpu" && len(f) >= 5:
				sawStat = true
				for i, v := range f[1:] {
					n, _ := strconv.ParseUint(v, 10, 64)
					if i >= 8 {
						break // guest time is counted in user time already
					}
					s.cpuTotal += n
					if i != 3 && i != 4 { // idle and iowait
						s.cpuBusy += n
					}
				}
			case strings.HasPrefix(f[0], "cpu"):
				s.cores++
			}
		case "meminfo":
			if len(f) < 2 {
				continue
			}
			n, _ := strconv.ParseUint(f[1], 10, 64)
			n *= 1024 // kB
			switch f[0] {
			case "MemTotal:":
				s.memTotal = n
			case "MemAvailable:":
				s.memAvail = n
			case "SwapTotal:":
				s.swapTotal = n
			case "SwapFree:":
				s.swapFree = n
			}
		case "net/dev":
			name, rest, ok := strings.Cut(line, ":")
			if !ok {
				continue // the headers
			}
			name = strings.TrimSpace(name)
			v := strings.Fields(rest)
			if name == "lo" || len(v) < 9 {
				continue
			}
			rx, _ := strconv.ParseUint(v[0], 10, 64)
			tx, _ := strconv.ParseUint(v[8], 10, 64)
			s.rx += rx
			s.tx += tx
			s.ifaces[name] = [2]uint64{rx, tx}
		case "loadavg":
			for i := 0; i < 3 && i < len(f); i++ {
				s.load[i], _ = strconv.ParseFloat(f[i], 64)
			}
		case "uptime":
			sec, _ := strconv.ParseFloat(f[0], 64)
			s.uptime = time.Duration(sec * float64(time.Second))
		case "pagesize":
			if n, err := strconv.ParseUint(f[0], 10, 64); err == nil && n > 0 {
				s.pageSize = n
			}
		case "procs":
			if pid, p, ok := parseProcStat(line); ok {
				s.procs[pid] = p
			}
		case "df":
			if d, ok := parseDF(f); ok {
				s.disks = append(s.disks, d)
			}
		}
	}
	if !sawStat {
		return s, errNotLinux
	}
	s.disks = dedupeDisks(s.disks)
	return s, nil
}

// parseProcStat reads a line of /proc/<pid>/stat: the PID, the name in
// parentheses, which may hold spaces and parentheses itself, then fields
// of which the 14th and 15th are the user and system time and the 24th
// the resident pages.
func parseProcStat(line string) (int, procTimes, bool) {
	open, end := strings.IndexByte(line, '('), strings.LastIndexByte(line, ')')
	if open < 0 || end < open {
		return 0, procTimes{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line[:open]))
	f := strings.Fields(line[end+1:]) // from the 3rd field, the state
	if err != nil || len(f) < 22 {
		return 0, procTimes{}, false
	}
	utime, _ := strconv.ParseUint(f[11], 10, 64)
	stime, _ := strconv.ParseUint(f[12], 10, 64)
	rss, _ := strconv.ParseInt(f[21], 10, 64)
	return pid, procTimes{name: line[open+1 : end], ticks: utime + stime, rss: uint64(max(rss, 0))}, true
}

// parseDF reads a line of df -kP: the device, its size, what is used and
// free in kB, the capacity and where it is mounted, which may hold
// spaces. Pseudo file systems are left out.
func parseDF(f []string) (Disk, bool) {
	if len(f) < 6 || f[0] == "Filesystem" {
		return Disk{}, false
	}
	size, err1 := strconv.ParseUint(f[1], 10, 64)
	used, err2 := strconv.ParseUint(f[2], 10, 64)
	if err1 != nil || err2 != nil || size == 0 {
		return Disk{}, false
	}
	d := Disk{Device: f[0], Mount: strings.Join(f[5:], " "), Size: size * 1024, Used: used * 1024}
	for _, p := range []string{"tmpfs", "devtmpfs", "udev", "overlay", "shm", "none", "efivarfs", "squashfs"} {
		if d.Device == p {
			return Disk{}, false
		}
	}
	for _, p := range []string{"/dev", "/run", "/sys", "/proc", "/snap/", "/boot/efi", "/var/lib/docker/"} {
		if d.Mount == p || strings.HasPrefix(d.Mount, p+"/") || strings.HasPrefix(d.Mount, p) && strings.HasSuffix(p, "/") {
			return Disk{}, false
		}
	}
	return d, true
}

// dedupeDisks keeps one mount of a device, the shortest.
func dedupeDisks(disks []Disk) []Disk {
	var out []Disk
	at := map[string]int{}
	for _, d := range disks {
		if i, ok := at[d.Device]; ok {
			if len(d.Mount) < len(out[i].Mount) {
				out[i] = d
			}
			continue
		}
		at[d.Device] = len(out)
		out = append(out, d)
	}
	return out
}

// stats turns two samples into the status between them.
func stats(prev, cur sample) Stats {
	st := Stats{
		At: cur.at, Host: cur.host, Cores: cur.cores, Load: cur.load, Uptime: cur.uptime,
		MemTotal: cur.memTotal, SwapTotal: cur.swapTotal, Disks: cur.disks,
	}
	if cur.memAvail <= cur.memTotal {
		st.MemUsed = cur.memTotal - cur.memAvail
	}
	if cur.swapFree <= cur.swapTotal {
		st.SwapUsed = cur.swapTotal - cur.swapFree
	}
	if total := cur.cpuTotal - prev.cpuTotal; cur.cpuTotal > prev.cpuTotal {
		st.CPU = float64(cur.cpuBusy-min(cur.cpuBusy, prev.cpuBusy)) / float64(total)
		st.CPU = min(max(st.CPU, 0), 1)
	}
	// Counters that went back (a reboot, an interface gone) give 0.
	rate := func(cur, prev uint64, dt float64) float64 {
		if cur < prev || dt <= 0 {
			return 0
		}
		return float64(cur-prev) / dt
	}
	dt := cur.at.Sub(prev.at).Seconds()
	st.RX, st.TX = rate(cur.rx, prev.rx, dt), rate(cur.tx, prev.tx, dt)
	for name, c := range cur.ifaces {
		if name == "lo" {
			continue
		}
		p, ok := prev.ifaces[name]
		if !ok {
			p = c
		}
		st.Ifaces = append(st.Ifaces, Iface{Name: name, RX: rate(c[0], p[0], dt), TX: rate(c[1], p[1], dt)})
	}
	sort.Slice(st.Ifaces, func(i, j int) bool { return st.Ifaces[i].Name < st.Ifaces[j].Name })
	st.Procs = topProcs(prev, cur)
	return st
}

// topProcs returns the processes that used the most CPU between two
// samples, then the most memory; one that started between them counts
// from the second.
func topProcs(prev, cur sample) []Proc {
	jiffies := cur.cpuTotal - min(cur.cpuTotal, prev.cpuTotal)
	cores := float64(max(cur.cores, 1))
	procs := make([]Proc, 0, len(cur.procs))
	for pid, p := range cur.procs {
		pr := Proc{PID: pid, Name: p.name, Mem: p.rss * cur.pageSize}
		if q, ok := prev.procs[pid]; ok && jiffies > 0 && p.ticks >= q.ticks {
			pr.CPU = float64(p.ticks-q.ticks) / float64(jiffies) * cores * 100
		}
		procs = append(procs, pr)
	}
	sort.Slice(procs, func(i, j int) bool {
		a, b := procs[i], procs[j]
		if a.CPU != b.CPU {
			return a.CPU > b.CPU
		}
		if a.Mem != b.Mem {
			return a.Mem > b.Mem
		}
		return a.PID < b.PID
	})
	return procs[:min(len(procs), TopProcs)]
}

// HistoryLen is how many samples a Monitor keeps.
const HistoryLen = 90

// Monitor samples a server's status every few seconds, keeping the last
// HistoryLen. Its methods are safe from any goroutine.
type Monitor struct {
	client   *ssh.Client
	every    time.Duration
	onUpdate func()

	mu      sync.Mutex
	history []Stats
	err     error
	stop    chan struct{}
	once    sync.Once
}

// StartMonitor samples the server of client every so often, telling
// onUpdate of each sample, on a goroutine of its own.
func StartMonitor(client *ssh.Client, every time.Duration, onUpdate func()) *Monitor {
	m := &Monitor{client: client, every: every, onUpdate: onUpdate, stop: make(chan struct{})}
	go m.run()
	return m
}

func (m *Monitor) run() {
	var prev *sample
	t := time.NewTicker(m.every)
	defer t.Stop()
	for {
		s, err := m.sample()
		m.mu.Lock()
		m.err = err
		if err == nil {
			if prev != nil {
				m.history = append(m.history, stats(*prev, s))
				if len(m.history) > HistoryLen {
					m.history = m.history[len(m.history)-HistoryLen:]
				}
			}
			prev = &s
		}
		m.mu.Unlock()
		if m.onUpdate != nil && (err != nil || len(m.History()) > 0) {
			m.onUpdate()
		}
		if errors.Is(err, errNotLinux) {
			return
		}
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
	}
}

func (m *Monitor) sample() (sample, error) {
	sess, err := m.client.NewSession()
	if err != nil {
		return sample{}, err
	}
	defer sess.Close()
	out, err := sess.Output(statsCommand)
	if err != nil && len(out) == 0 {
		return sample{}, err
	}
	return parseSample(string(out), time.Now())
}

// History returns the status sampled, oldest first.
func (m *Monitor) History() []Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Stats(nil), m.history...)
}

// Err returns the error of the last sample, nil when it worked.
func (m *Monitor) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Stop ends the sampling.
func (m *Monitor) Stop() {
	m.once.Do(func() { close(m.stop) })
}
