// Package goasic is a Go library for managing Bitcoin and alt-coin ASIC miners.
//
// It is a full Go equivalent of pyasic, supporting all 58 known miner models
// across 5 brands as of 2026.
//
// # Quick start
//
//	ctx := context.Background()
//
//	// Auto-detect miner type and connect
//	miner, err := goasic.Detect(ctx, "192.168.1.10")
//
//	// Get live data
//	data, err := miner.GetData(ctx)
//	fmt.Println(goasic.Summary(data))
//
//	// Scan a whole subnet
//	miners, err := goasic.ScanSubnet(ctx, "192.168.1.0/24", 100)
//
// # Supported brands
//
//	Antminer  (Bitmain)  — SHA-256d / Scrypt / X11 / kHeavyHash / Equihash
//	Whatsminer (MicroBT) — SHA-256d
//	Avalonminer (Canaan) — SHA-256d
//	IceRiver             — kHeavyHash (Kaspa)
//	U3                   — SHA-256d (Hydro)
package goasic

import (
	"context"

	"github.com/goasic/goasic/pkg/minerdb"
	"github.com/goasic/goasic/pkg/miners"
	"github.com/goasic/goasic/pkg/network"
)

// Miner is the interface all brand drivers implement.
type Miner = miners.Miner

// MinerData is a live snapshot of miner performance and status.
type MinerData = miners.MinerData

// MinerConfig holds pool and power configuration to push to a miner.
type MinerConfig = miners.MinerConfig

// MinerSpec is a static specification entry from the embedded MinerDB.
type MinerSpec = minerdb.MinerSpec

// Detect auto-detects the miner brand at ip and returns the correct driver.
// ip must be a valid routable IPv4/IPv6 address — not a hostname.
func Detect(ctx context.Context, ip string) (Miner, error) {
	return miners.Detect(ctx, ip)
}

// NewAntminer creates an Antminer driver without auto-detection.
func NewAntminer(ip string) (Miner, error) { return miners.NewAntminer(ip) }

// NewWhatsminer creates a Whatsminer driver without auto-detection.
func NewWhatsminer(ip string) (Miner, error) { return miners.NewWhatsminer(ip) }

// NewAvalonminer creates an Avalonminer driver without auto-detection.
func NewAvalonminer(ip string) (Miner, error) { return miners.NewAvalonminer(ip) }

// NewIceRiver creates an IceRiver driver without auto-detection.
func NewIceRiver(ip string) (Miner, error) { return miners.NewIceRiver(ip) }

// NewU3Miner creates a U3 hydro miner driver without auto-detection.
func NewU3Miner(ip string) (Miner, error) { return miners.NewU3Miner(ip) }

// ScanSubnet scans all hosts in a CIDR block and returns detected miners.
// maxConcurrent controls how many IPs are probed in parallel (recommend 50–200).
func ScanSubnet(ctx context.Context, cidr string, maxConcurrent int) ([]Miner, error) {
	return network.ScanAll(ctx, cidr, maxConcurrent)
}

// Summary returns a one-line human-readable string for a MinerData snapshot.
func Summary(d *MinerData) string {
	return miners.FormatSummary(d)
}

// DB returns the embedded MinerDB for model lookups.
func DB() []*MinerSpec { return minerdb.All() }

// DBGet looks up a model spec by name.
func DBGet(model string) *MinerSpec { return minerdb.Get(model) }

// DBCount returns the total number of known models.
func DBCount() int { return minerdb.Count() }

// ── New types ─────────────────────────────────────────────────────────────────

// MiningMode represents the operating mode of a miner.
type MiningMode = miners.MiningMode

// FirmwareInfo holds metadata for a firmware update.
type FirmwareInfo = miners.FirmwareInfo

const (
	ModeNormal   = miners.ModeNormal
	ModeLowPower = miners.ModeLowPower
	ModeHighPerf = miners.ModeHighPerf
	ModeSleep    = miners.ModeSleep
)

// ── Alternative firmware constructors ────────────────────────────────────────

// NewBraiinsOS creates a driver for Antminer hardware running Braiins OS+.
func NewBraiinsOS(ip, username, password string) (Miner, error) {
	return miners.NewBraiinsOS(ip, username, password)
}

// NewVnish creates a driver for Antminer hardware running Vnish firmware.
func NewVnish(ip, username, password string) (Miner, error) {
	return miners.NewVnish(ip, username, password)
}

// NewLuxOS creates a driver for Antminer hardware running LuxOS firmware.
// bearerToken is the API token obtained from the LuxOS web panel.
func NewLuxOS(ip, bearerToken string) (Miner, error) {
	return miners.NewLuxOS(ip, bearerToken)
}

// NewHiveon creates a driver for Antminer hardware running Hiveon firmware.
func NewHiveon(ip, username, password string) (Miner, error) {
	return miners.NewHiveon(ip, username, password)
}

// NewInnosilicon creates a driver for Innosilicon T3/T3+/A10/A11 hardware.
func NewInnosilicon(ip string) (Miner, error) {
	return miners.NewInnosilicon(ip)
}
