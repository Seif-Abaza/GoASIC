// Package miners defines the Miner interface and all data types shared across drivers.
package miners

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/goasic/goasic/pkg/minerdb"
)

// ── MinerData ─────────────────────────────────────────────────────────────────

// MinerData is a snapshot of live operational data from a miner.
type MinerData struct {
	// Identity
	IP       string    `json:"ip"`
	DateTime time.Time `json:"datetime"`
	Model    string    `json:"model"`
	Make     string    `json:"make"`
	Firmware string    `json:"firmware,omitempty"`

	// Algorithm
	Algorithm string `json:"algorithm"`

	// Performance
	Hashrate         *float64 `json:"hashrate_ths,omitempty"`      // TH/s (or TH/s-equivalent)
	ExpectedHashrate *float64 `json:"expected_hashrate_ths,omitempty"`
	HashratePct      *float64 `json:"hashrate_pct,omitempty"`      // actual/expected × 100

	// Environment
	Temperature []float64 `json:"temperature,omitempty"`
	FanSpeeds   []int     `json:"fan_speeds,omitempty"`

	// Power
	Wattage      *int     `json:"wattage_w,omitempty"`
	WattageLimit *int     `json:"wattage_limit_w,omitempty"`
	Efficiency   *float64 `json:"efficiency_jth,omitempty"` // J/TH

	// Pool
	Hostname   string `json:"hostname,omitempty"`
	Pool1URL   string `json:"pool1_url,omitempty"`
	Pool1User  string `json:"pool1_user,omitempty"`

	// Status
	Errors    []string `json:"errors,omitempty"`
	FaultLight bool    `json:"fault_light"`
	IsMining  bool     `json:"is_mining"`
	Uptime    *uint64  `json:"uptime_s,omitempty"`

	// Hardware
	ChipCount  *int    `json:"chip_count,omitempty"`
	FansCount  *int    `json:"fans_count,omitempty"`
	Cooling    string  `json:"cooling,omitempty"` // "Air" | "Hydro" | "Immersion"
}

// EnrichFromDB fills ExpectedHashrate, ChipCount, Cooling, and Algorithm from
// the embedded MinerDB. Call after setting Model and Make.
func (d *MinerData) EnrichFromDB() {
	query := d.Model
	if query == "" {
		query = d.Make
	}
	spec := minerdb.Get(query)
	if spec == nil {
		return
	}
	if d.ExpectedHashrate == nil && spec.HashrateTHS > 0 {
		v := spec.HashrateTHS
		d.ExpectedHashrate = &v
	}
	if d.ChipCount == nil && spec.ASICChips > 0 {
		v := spec.ASICChips
		d.ChipCount = &v
	}
	if d.FansCount == nil {
		v := spec.Fans
		d.FansCount = &v
	}
	if d.Cooling == "" {
		d.Cooling = string(spec.Cooling)
	}
	if d.Algorithm == "" {
		d.Algorithm = string(spec.Algorithm)
	}
	// Efficiency %
	if d.Hashrate != nil && d.ExpectedHashrate != nil && *d.ExpectedHashrate > 0 {
		pct := (*d.Hashrate / *d.ExpectedHashrate) * 100
		d.HashratePct = &pct
	}
	// J/TH
	if d.Wattage != nil && d.Hashrate != nil && *d.Hashrate > 0 {
		eff := float64(*d.Wattage) / *d.Hashrate
		d.Efficiency = &eff
	}
}

// ── PoolConfig ────────────────────────────────────────────────────────────────

// PoolConfig is a single mining pool connection.
type PoolConfig struct {
	URL      string
	User     string
	Password string
}

// ── MinerConfig ───────────────────────────────────────────────────────────────

// MinerConfig holds the full configuration to push to a miner.
type MinerConfig struct {
	Pools      []PoolConfig
	PowerLimit *int
	FanSpeed   *int
	Hostname   string
}

// AddPool appends a pool to the config.
func (c *MinerConfig) AddPool(url, user, password string) {
	c.Pools = append(c.Pools, PoolConfig{URL: url, User: user, Password: password})
}

// Pool returns the pool at index i, or a zero PoolConfig if out of range.
func (c *MinerConfig) Pool(i int) PoolConfig {
	if i < len(c.Pools) {
		return c.Pools[i]
	}
	return PoolConfig{}
}

// ── Miner interface ───────────────────────────────────────────────────────────

// MiningMode represents the operating mode of a miner.
type MiningMode string

const (
	ModeNormal      MiningMode = "normal"       // standard full-power mode
	ModeLowPower    MiningMode = "low_power"     // LPM – reduced freq/voltage
	ModeHighPerf    MiningMode = "high_perf"     // overclocked / turbo
	ModeSleep       MiningMode = "sleep"         // minimal power, mining paused
)

// FirmwareInfo holds metadata about a firmware update package.
type FirmwareInfo struct {
	Version     string // version string, e.g. "BOS+ 22.02.1"
	URL         string // download URL (used by stock firmware updaters)
	LocalPath   string // local file path if already downloaded
	Description string
}

// Miner is the interface all brand drivers must implement.
type Miner interface {
	// GetData returns a live snapshot of miner performance and status.
	GetData(ctx context.Context) (*MinerData, error)
	// GetConfig reads the current pool/power configuration from the miner.
	GetConfig(ctx context.Context) (*MinerConfig, error)
	// SendConfig pushes new pool/power settings to the miner.
	SendConfig(ctx context.Context, cfg MinerConfig) error
	// Reboot reboots the miner.
	Reboot(ctx context.Context) error
	// FaultLightOn turns on the fault LED.
	FaultLightOn(ctx context.Context) error
	// FaultLightOff turns off the fault LED.
	FaultLightOff(ctx context.Context) error
	// StopMining pauses hashing.
	StopMining(ctx context.Context) error
	// ResumeMining resumes hashing.
	ResumeMining(ctx context.Context) error
	// IsMining returns true if the miner is actively hashing.
	IsMining(ctx context.Context) (bool, error)
	// SetMode sets the operating mode (Normal / LowPower / HighPerf / Sleep).
	SetMode(ctx context.Context, mode MiningMode) error
	// SetFanSpeed sets the fan speed as a percentage (0–100).
	// Pass -1 to restore automatic fan control.
	SetFanSpeed(ctx context.Context, pct int) error
	// UpdateFirmware flashes new firmware onto the miner.
	// The call blocks until the flash completes or ctx is cancelled.
	UpdateFirmware(ctx context.Context, fw FirmwareInfo) error
	// IP returns the miner's IP address string.
	IP() string
	// Brand returns the manufacturer string, e.g. "Antminer".
	Brand() string
}

// ── helpers ───────────────────────────────────────────────────────────────────

// DetectFirmwareClass returns "hydro", "immersion", "altair", or "standard"
// based on the model name string.
func DetectFirmwareClass(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "hyd") || strings.Contains(m, "hydro") {
		return "hydro"
	}
	if strings.Contains(m, "imm") || strings.Contains(m, "immersion") {
		return "immersion"
	}
	if strings.Contains(m, " l7") || strings.Contains(m, " l9") ||
		strings.Contains(m, " d7") || strings.Contains(m, " k7") ||
		strings.Contains(m, " ks5") || strings.Contains(m, " z15") {
		return "altair"
	}
	return "standard"
}

// PtrFloat64 is a convenience helper.
func PtrFloat64(v float64) *float64 { return &v }

// PtrInt is a convenience helper.
func PtrInt(v int) *int { return &v }

// PtrUint64 is a convenience helper.
func PtrUint64(v uint64) *uint64 { return &v }

// FormatSummary returns a one-line human-readable summary of a MinerData.
func FormatSummary(d *MinerData) string {
	hr := "?"
	if d.Hashrate != nil {
		hr = fmt.Sprintf("%.2f TH/s", *d.Hashrate)
	}
	pct := ""
	if d.HashratePct != nil {
		pct = fmt.Sprintf(" (%.1f%%)", *d.HashratePct)
	}
	mining := "idle"
	if d.IsMining {
		mining = "mining"
	}
	return fmt.Sprintf("[%s] %s %s | %s%s | %s | algo:%s | cool:%s",
		d.IP, d.Make, d.Model, hr, pct, mining, d.Algorithm, d.Cooling)
}
