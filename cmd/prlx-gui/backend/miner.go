// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ParallaxProtocol/parallax/common"
	"github.com/ParallaxProtocol/parallax/log"
)

// defaultPools is the static list of known Parallax mining pools shipped with
// the GUI. Users can add custom pools in settings.
var defaultPools = []PoolInfo{
	{Name: "CoolPool (Main)", URL: "stratum1+tcp://prlx.coolpool.top:3003", Region: "Global", Builtin: true},
	{Name: "CoolPool (EU)", URL: "stratum1+tcp://eu.coolpool.top:3003", Region: "EU", Builtin: true},
	{Name: "CoolPool (US)", URL: "stratum1+tcp://us.coolpool.top:3003", Region: "US", Builtin: true},
	{Name: "CoolPool (Asia)", URL: "stratum1+tcp://asia.coolpool.top:3003", Region: "Asia", Builtin: true},
}

// MinerController manages GPU pool mining (via hashwarp child process) and
// CPU solo mining (via the embedded Parallax node). It follows the same
// lifecycle patterns as NodeController.
type MinerController struct {
	cfg  *ConfigStore
	node *NodeController

	mu            sync.RWMutex
	mode          string // "off" | "pool" | "solo"
	running       bool
	stopCh        chan struct{}
	startedAt     time.Time
	lastStatus    MinerStatus
	cmd           *exec.Cmd // hashwarp child process (pool mode only)
	apiPort       int       // hashwarp API port
	generatingDAG bool      // parsed from hashwarp stdout

	emitter func(MinerEvent)
}

// NewMinerController creates a MinerController. Heavy work is deferred to Start.
func NewMinerController(cfg *ConfigStore, node *NodeController) *MinerController {
	return &MinerController{
		cfg:  cfg,
		node: node,
		mode: "off",
	}
}

// SetEmitter installs the Wails event callback.
func (m *MinerController) SetEmitter(fn func(MinerEvent)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitter = fn
}

func (m *MinerController) emit(kind string, payload interface{}) {
	m.mu.RLock()
	fn := m.emitter
	m.mu.RUnlock()
	if fn != nil {
		fn(MinerEvent{Kind: kind, Payload: payload})
	}
}

// DefaultPools returns the built-in pool list.
func (m *MinerController) DefaultPools() []PoolInfo {
	return defaultPools
}

// HashwarpInstalled reports whether the hashwarp binary can be found.
func (m *MinerController) HashwarpInstalled() bool {
	_, err := m.hashwarpPath()
	return err == nil
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status returns the latest mining snapshot.
func (m *MinerController) Status() MinerStatus {
	m.mu.RLock()
	st := m.lastStatus
	m.mu.RUnlock()
	return st
}

// ---------------------------------------------------------------------------
// Start
// ---------------------------------------------------------------------------

// Start begins mining in the given mode ("pool" or "solo"). Idempotent if
// already running in the same mode.
func (m *MinerController) Start(ctx context.Context, mode string) error {
	m.mu.Lock()
	if m.running {
		if m.mode == mode {
			m.mu.Unlock()
			return nil // already running in this mode
		}
		m.mu.Unlock()
		// Different mode requested — stop first.
		if err := m.Stop(); err != nil {
			return fmt.Errorf("stop existing miner: %w", err)
		}
		m.mu.Lock()
	}

	cfg := m.cfg.Get()
	m.stopCh = make(chan struct{})
	m.startedAt = time.Now()
	m.lastStatus = MinerStatus{Mode: mode, Running: true}
	m.mode = mode
	m.running = true
	stopCh := m.stopCh
	m.mu.Unlock()

	var err error
	switch mode {
	case "pool":
		err = m.startPool(cfg, stopCh)
	case "sologpu":
		err = m.startSoloGPU(cfg, stopCh)
	case "solo":
		err = m.startSolo(cfg, stopCh)
	default:
		err = fmt.Errorf("unknown mining mode: %q", mode)
	}

	if err != nil {
		m.mu.Lock()
		m.running = false
		m.mode = "off"
		m.lastStatus = MinerStatus{Mode: "off"}
		m.mu.Unlock()
		return err
	}

	m.emit("started", mode)
	return nil
}

// startPool spawns the hashwarp child process and monitoring goroutines.
func (m *MinerController) startPool(cfg GUIConfig, stopCh chan struct{}) error {
	if cfg.MiningWallet == "" {
		return fmt.Errorf("wallet address is required for pool mining")
	}
	if cfg.MiningPool == "" {
		return fmt.Errorf("pool URL is required")
	}

	hwPath, err := m.hashwarpPath()
	if err != nil {
		return err
	}

	// Allocate an ephemeral port for the hashwarp JSON-RPC API.
	apiPort, err := freePort()
	if err != nil {
		return fmt.Errorf("allocate API port: %w", err)
	}
	m.mu.Lock()
	m.apiPort = apiPort
	m.mu.Unlock()

	// Build the pool connection URI.
	// Format: scheme://wallet.worker@host:port
	poolURL := cfg.MiningPool
	worker := cfg.MiningWorker
	if worker == "" {
		worker = hostname()
	}
	connURI := buildPoolURI(poolURL, cfg.MiningWallet, worker)

	args := []string{
		"-P", connURI,
		"--api-bind", fmt.Sprintf("127.0.0.1:%d", apiPort),
		"--HWMON", "2",
		"--nocolor",
	}

	// Device selection
	if len(cfg.MiningDevices) > 0 {
		devStr := intSliceToCSV(cfg.MiningDevices)
		// Use both CUDA and OpenCL device flags — hashwarp will pick the
		// applicable one based on the device backend.
		args = append(args, "--cu-devices", devStr, "--cl-devices", devStr)
	}

	cmd := exec.Command(hwPath, args...)
	cmd.Dir = filepath.Dir(hwPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hashwarp: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()

	go m.parseOutput(stdout, stopCh)
	go m.pollAPI(stopCh)
	go m.waitProcess(stopCh)

	log.Info("Pool mining started", "pid", cmd.Process.Pid, "uri", connURI, "api", apiPort)
	return nil
}

// startSoloGPU spawns hashwarp in getwork mode, connecting to the local
// node's HTTP RPC endpoint for GPU-accelerated solo mining.
func (m *MinerController) startSoloGPU(cfg GUIConfig, stopCh chan struct{}) error {
	prl := m.node.Parallax()
	if prl == nil {
		return fmt.Errorf("node must be running for solo GPU mining")
	}
	if cfg.MiningWallet == "" {
		return fmt.Errorf("wallet address is required for solo mining (coinbase)")
	}
	if !cfg.HTTPRPCEnabled {
		return fmt.Errorf("HTTP RPC must be enabled for solo GPU mining (Settings → HTTP-RPC)")
	}

	// Set coinbase and start the node's miner with 1 thread so the worker
	// continuously builds and refreshes block templates for hashwarp to
	// consume via eth_getWork.
	addr := common.HexToAddress(cfg.MiningWallet)
	prl.SetCoinbase(addr)
	if err := prl.StartMining(1); err != nil {
		return fmt.Errorf("start node miner: %w", err)
	}

	hwPath, err := m.hashwarpPath()
	if err != nil {
		return err
	}

	// Allocate an ephemeral port for the hashwarp JSON-RPC API.
	apiPort, err := freePort()
	if err != nil {
		return fmt.Errorf("allocate API port: %w", err)
	}
	m.mu.Lock()
	m.apiPort = apiPort
	m.mu.Unlock()

	// Connect to the local node via getwork (HTTP RPC).
	rpcPort := cfg.HTTPRPCPort
	if rpcPort == 0 {
		rpcPort = 8545
	}
	connURI := fmt.Sprintf("http://%s@127.0.0.1:%d", cfg.MiningWallet, rpcPort)

	args := []string{
		"-P", connURI,
		"--api-bind", fmt.Sprintf("127.0.0.1:%d", apiPort),
		"--HWMON", "2",
		"--nocolor",
	}

	if len(cfg.MiningDevices) > 0 {
		devStr := intSliceToCSV(cfg.MiningDevices)
		args = append(args, "--cu-devices", devStr, "--cl-devices", devStr)
	}

	cmd := exec.Command(hwPath, args...)
	cmd.Dir = filepath.Dir(hwPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hashwarp: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()

	go m.parseOutput(stdout, stopCh)
	go m.pollAPI(stopCh)
	go m.waitProcess(stopCh)

	log.Info("Solo GPU mining started", "pid", cmd.Process.Pid, "uri", connURI, "api", apiPort)
	return nil
}

// startSolo begins CPU mining through the embedded Parallax node.
func (m *MinerController) startSolo(cfg GUIConfig, stopCh chan struct{}) error {
	prl := m.node.Parallax()
	if prl == nil {
		return fmt.Errorf("node must be running for solo mining")
	}

	if cfg.MiningWallet == "" {
		return fmt.Errorf("wallet address is required for solo mining (coinbase)")
	}

	addr := common.HexToAddress(cfg.MiningWallet)
	prl.SetCoinbase(addr)

	threads := cfg.MiningThreads
	if threads <= 0 {
		threads = runtime.NumCPU() / 2
		if threads < 1 {
			threads = 1
		}
	}

	if err := prl.StartMining(threads); err != nil {
		return fmt.Errorf("start solo mining: %w", err)
	}

	go m.pollSoloStats(stopCh)

	log.Info("Solo mining started", "threads", threads, "coinbase", addr.Hex())
	return nil
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

// Stop halts mining. Idempotent.
func (m *MinerController) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	mode := m.mode
	cmd := m.cmd
	stopCh := m.stopCh

	m.running = false
	m.mode = "off"
	m.cmd = nil
	m.stopCh = nil
	m.mu.Unlock()

	// Signal goroutines to stop.
	close(stopCh)

	switch mode {
	case "pool", "sologpu":
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			go func() {
				_ = cmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
		}
		// For sologpu, also stop the node's miner (work generator).
		if mode == "sologpu" {
			if prl := m.node.Parallax(); prl != nil {
				prl.StopMining()
			}
		}
	case "solo":
		if prl := m.node.Parallax(); prl != nil {
			prl.StopMining()
		}
	}

	m.mu.Lock()
	m.lastStatus = MinerStatus{Mode: "off"}
	m.generatingDAG = false
	m.mu.Unlock()

	m.emit("stopped", nil)
	log.Info("Mining stopped", "mode", mode)
	return nil
}

// ---------------------------------------------------------------------------
// GPU detection
// ---------------------------------------------------------------------------

// DetectGPUs runs hashwarp --list-devices and parses the output.
func (m *MinerController) DetectGPUs() ([]DeviceInfo, error) {
	hwPath, err := m.hashwarpPath()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, hwPath, "--list-devices").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("hashwarp --list-devices: %w\n%s", err, string(out))
	}

	return parseDeviceList(string(out)), nil
}

// parseDeviceList parses the tabular output from hashwarp --list-devices.
// Example line: "  0 01:00.0  Gpu   GeForce GTX 1050 Ti           Yes   61 3.95 GB"
func parseDeviceList(output string) []DeviceInfo {
	var devices []DeviceInfo
	lines := strings.Split(output, "\n")
	inTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip empty lines and the header separator.
		if trimmed == "" || strings.HasPrefix(trimmed, "---") {
			if strings.HasPrefix(trimmed, "---") {
				inTable = true
			}
			continue
		}
		// Skip header row (before separator).
		if !inTable {
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 4 {
			continue
		}

		idx, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		dev := DeviceInfo{
			Index: idx,
			PciID: fields[1],
			Type:  fields[2],
		}

		// The device name can be multiple words. We collect everything
		// between the type field and known trailing fields.
		// Heuristic: scan for "Yes"/"No" markers that indicate CUDA/CL support.
		nameEnd := 3
		for i := 3; i < len(fields); i++ {
			if fields[i] == "Yes" || fields[i] == "No" {
				nameEnd = i
				break
			}
			// Check if it looks like a memory size (e.g., "3.95")
			if _, err := strconv.ParseFloat(fields[i], 64); err == nil && i+1 < len(fields) && fields[i+1] == "GB" {
				nameEnd = i
				break
			}
		}
		if nameEnd == 3 {
			nameEnd = len(fields)
		}
		dev.Name = strings.Join(fields[3:nameEnd], " ")

		// Scan remaining fields for CUDA/OpenCL markers and memory.
		rest := fields[nameEnd:]
		for i, f := range rest {
			switch f {
			case "Yes":
				// First "Yes" is usually CUDA, second is OpenCL.
				if !dev.CUDA {
					dev.CUDA = true
				} else {
					dev.OpenCL = true
				}
			case "GB":
				if i > 0 {
					dev.Memory = rest[i-1] + " GB"
				}
			}
		}

		devices = append(devices, dev)
	}
	return devices
}

// ---------------------------------------------------------------------------
// hashwarp output parsing
// ---------------------------------------------------------------------------

func (m *MinerController) parseOutput(r io.Reader, stopCh <-chan struct{}) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-stopCh:
			return
		default:
		}
		line := scanner.Text()

		// Forward to GUI log system.
		log.Debug("hashwarp", "msg", line)

		if strings.Contains(line, "Generating") && strings.Contains(line, "DAG") {
			m.mu.Lock()
			m.generatingDAG = true
			m.mu.Unlock()
			m.emit("dag", "generating")
		}
		// OpenCL: "X.XX GB of DAG data generated in Y ms."
		// CUDA:   "Generated DAG + Light in Y ms."
		if strings.Contains(line, "DAG data generated") || strings.Contains(line, "Generated DAG") {
			m.mu.Lock()
			m.generatingDAG = false
			m.mu.Unlock()
			m.emit("dag", "ready")
		}
	}
}

// ---------------------------------------------------------------------------
// hashwarp JSON-RPC API polling
// ---------------------------------------------------------------------------

func (m *MinerController) pollAPI(stopCh <-chan struct{}) {
	// Give hashwarp a moment to start its API server.
	select {
	case <-time.After(2 * time.Second):
	case <-stopCh:
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			st, err := m.fetchHashwarpStats()
			if err != nil {
				failures++
				if failures > 10 {
					m.mu.Lock()
					if m.running && (m.mode == "pool" || m.mode == "sologpu") {
						m.lastStatus.Error = "hashwarp API unreachable"
					}
					m.mu.Unlock()
				}
				continue
			}
			failures = 0

			m.mu.Lock()
			if m.running && (m.mode == "pool" || m.mode == "sologpu") {
				st.Mode = m.mode
				st.Running = true
				// If hashrate is non-zero, DAG generation is definitely done
				// regardless of whether we caught the stdout line.
				if st.Hashrate > 0 {
					m.generatingDAG = false
				}
				st.GeneratingDAG = m.generatingDAG
				st.UptimeSeconds = int64(time.Since(m.startedAt).Seconds())
				m.lastStatus = st
			}
			m.mu.Unlock()
		}
	}
}

// fetchHashwarpStats calls miner_getstatdetail and maps the response.
func (m *MinerController) fetchHashwarpStats() (MinerStatus, error) {
	m.mu.RLock()
	port := m.apiPort
	m.mu.RUnlock()

	raw, err := m.rpcCall(port, "miner_getstatdetail")
	if err != nil {
		return MinerStatus{}, err
	}

	// Parse the nested JSON response.
	var resp struct {
		Connection struct {
			Connected bool   `json:"connected"`
			URI       string `json:"uri"`
		} `json:"connection"`
		Mining struct {
			Hashrate string `json:"hashrate"` // hex
			Epoch    int    `json:"epoch"`
			Shares   []int  `json:"shares"` // [accepted, rejected, failed, secsSinceLast]
		} `json:"mining"`
		Devices []struct {
			Index    int    `json:"_index"`
			Mode     string `json:"_mode"`
			Hardware struct {
				Name    string    `json:"name"`
				Sensors []float64 `json:"sensors"` // [temp, fan, power]
			} `json:"hardware"`
			Mining struct {
				Hashrate string `json:"hashrate"` // hex
				Paused   bool   `json:"paused"`
				Shares   []int  `json:"shares"` // [accepted, rejected, failed, secsSinceLast]
			} `json:"mining"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return MinerStatus{}, fmt.Errorf("parse stats: %w", err)
	}

	st := MinerStatus{
		PoolConnected: resp.Connection.Connected,
		PoolURI:       resp.Connection.URI,
		Epoch:         resp.Mining.Epoch,
	}

	// Parse aggregate hashrate from hex.
	st.Hashrate = parseHexFloat(resp.Mining.Hashrate)

	if len(resp.Mining.Shares) >= 4 {
		st.SharesAccepted = uint64(resp.Mining.Shares[0])
		st.SharesRejected = uint64(resp.Mining.Shares[1])
		st.SharesFailed = uint64(resp.Mining.Shares[2])
		st.SecsSinceLastShare = uint64(resp.Mining.Shares[3])
	}

	for _, d := range resp.Devices {
		gd := GPUDeviceStatus{
			Index:    d.Index,
			Name:     d.Hardware.Name,
			Mode:     d.Mode,
			Hashrate: parseHexFloat(d.Mining.Hashrate),
			Paused:   d.Mining.Paused,
		}
		if len(d.Hardware.Sensors) >= 1 {
			gd.TempC = int(d.Hardware.Sensors[0])
		}
		if len(d.Hardware.Sensors) >= 2 {
			gd.FanPct = int(d.Hardware.Sensors[1])
		}
		if len(d.Hardware.Sensors) >= 3 {
			gd.PowerW = d.Hardware.Sensors[2]
		}
		if len(d.Mining.Shares) >= 3 {
			gd.Accepted = uint64(d.Mining.Shares[0])
			gd.Rejected = uint64(d.Mining.Shares[1])
			gd.Failed = uint64(d.Mining.Shares[2])
		}
		st.Devices = append(st.Devices, gd)
	}

	return st, nil
}

// rpcCall sends a JSON-RPC 2.0 request to the hashwarp API over TCP.
func (m *MinerController) rpcCall(port int, method string) (json.RawMessage, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	req := fmt.Sprintf(`{"id":1,"jsonrpc":"2.0","method":"%s"}`+"\n", method)
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// ---------------------------------------------------------------------------
// hashwarp process lifecycle
// ---------------------------------------------------------------------------

func (m *MinerController) waitProcess(stopCh <-chan struct{}) {
	m.mu.RLock()
	cmd := m.cmd
	m.mu.RUnlock()
	if cmd == nil {
		return
	}

	err := cmd.Wait()

	// Check if we were intentionally stopped.
	select {
	case <-stopCh:
		return // Clean shutdown, don't emit error.
	default:
	}

	// Unexpected exit.
	m.mu.Lock()
	m.running = false
	m.mode = "off"
	errMsg := "hashwarp exited unexpectedly"
	if err != nil {
		errMsg = fmt.Sprintf("hashwarp exited: %v", err)
	}
	m.lastStatus = MinerStatus{Mode: "off", Error: errMsg}
	m.mu.Unlock()

	m.emit("error", errMsg)
	log.Error("hashwarp exited unexpectedly", "err", err)
}

// ---------------------------------------------------------------------------
// Solo mining stats polling
// ---------------------------------------------------------------------------

func (m *MinerController) pollSoloStats(stopCh <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			prl := m.node.Parallax()
			if prl == nil {
				m.mu.Lock()
				if m.running && m.mode == "solo" {
					m.lastStatus.Error = "node stopped"
					m.running = false
					m.mode = "off"
				}
				m.mu.Unlock()
				m.emit("error", "node stopped while solo mining")
				return
			}

			hashrate := prl.Miner().Hashrate()
			mining := prl.IsMining()

			currentBlock := prl.BlockChain().CurrentBlock()
			var diffStr string
			if currentBlock != nil {
				diffStr = currentBlock.Difficulty().String()
			}

			m.mu.Lock()
			if m.running && m.mode == "solo" {
				m.lastStatus = MinerStatus{
					Mode:           "solo",
					Running:        mining,
					Hashrate:       float64(hashrate),
					UptimeSeconds:  int64(time.Since(m.startedAt).Seconds()),
					SoloDifficulty: diffStr,
				}
				if !mining {
					m.lastStatus.Error = "miner stopped"
					m.running = false
					m.mode = "off"
				}
			}
			m.mu.Unlock()
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// hashwarpPath locates the hashwarp binary: first next to the running
// executable, then in $PATH.
func (m *MinerController) hashwarpPath() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		name := "hashwarp"
		if runtime.GOOS == "windows" {
			name = "hashwarp.exe"
		}
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath("hashwarp")
	if err != nil {
		return "", fmt.Errorf("hashwarp binary not found (checked alongside prlx-gui and in $PATH)")
	}
	return p, nil
}

// buildPoolURI constructs a pool connection URI from the pool URL, wallet,
// and worker name. Pool URL format: "stratum+tcp://host:port"
// Result: "stratum+tcp://wallet.worker@host:port"
func buildPoolURI(poolURL, wallet, worker string) string {
	// Split scheme from host.
	parts := strings.SplitN(poolURL, "://", 2)
	if len(parts) != 2 {
		// Fallback: assume it's just host:port.
		return fmt.Sprintf("stratum+tcp://%s.%s@%s", wallet, worker, poolURL)
	}
	scheme := parts[0]
	hostPort := parts[1]
	return fmt.Sprintf("%s://%s.%s@%s", scheme, wallet, worker, hostPort)
}

// freePort asks the OS for a free TCP port on localhost.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// parseHexFloat parses a hex string like "0x00000000054a89c8" to float64.
func parseHexFloat(hex string) float64 {
	hex = strings.TrimPrefix(hex, "0x")
	hex = strings.TrimPrefix(hex, "0X")
	if hex == "" {
		return 0
	}
	n := new(big.Int)
	n.SetString(hex, 16)
	f, _ := new(big.Float).SetInt(n).Float64()
	return f
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "worker"
	}
	return h
}

func intSliceToCSV(ints []int) string {
	strs := make([]string, len(ints))
	for i, v := range ints {
		strs[i] = strconv.Itoa(v)
	}
	return strings.Join(strs, ",")
}
