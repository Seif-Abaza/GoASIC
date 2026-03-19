# goasic

A Go library for managing Bitcoin and alt-coin ASIC miners — the complete Go equivalent of [pyasic](https://github.com/UpstreamData/pyasic).

Supports **68 models** across **9 brands/firmware** as of 2026.

---

## Feature Parity with pyasic

| Feature | pyasic | goasic |
|---|---|---|
| Multi-manufacturer support | ✓ | ✓ |
| Concurrent scanning (async) | ✓ asyncio | ✓ goroutines |
| Reboot | ✓ | ✓ |
| Pool / wallet config push | ✓ | ✓ |
| **Fan speed control** | ✓ | ✓ |
| **Operating modes** (LPM / Normal / High Perf) | ✓ | ✓ |
| Hashrate telemetry | ✓ | ✓ |
| Temperature telemetry | ✓ | ✓ |
| Power consumption telemetry | ✓ | ✓ |
| Chip efficiency (J/TH) | ✓ | ✓ |
| **Firmware update** (local file + URL) | ✓ | ✓ |
| Fault LED control | ✓ | ✓ |
| Stop / Resume mining | ✓ | ✓ |
| **Braiins OS+** firmware | ✓ | ✓ |
| **Vnish** firmware | ✓ | ✓ |
| **LuxOS** firmware | ✓ | ✓ |
| **Hiveon** firmware | ✓ | ✓ |
| **Innosilicon** hardware | ✓ | ✓ |
| Zero external dependencies | — | ✓ |
| Compile-time interface checks | — | ✓ |

---

## Supported Hardware

### Antminer (Bitmain) — 42 models
| Family | Models | Algorithm | Cooling |
|--------|--------|-----------|---------|
| S9 | S9, S9K, S9SE | SHA-256d | Air |
| S17 | S17, S17 Pro, T17, T17+ | SHA-256d | Air |
| S19 | S19, S19 Pro, S19j, S19j Pro, S19K Pro (x2), S19 XP (x5), S19e XP Hyd, S19 XP+ Hyd, T19 | SHA-256d | Air/Hydro |
| S21 | S21, S21a, S21b, S21 Pro, S21 XP, S21 Imm, S21 XP Imm, S21e Hyd (x3), S21+ Hyd (x3), S21 XP Hyd | SHA-256d | Air/Hydro/Imm |
| Scrypt | L7 (x2), L9 (x4) | Scrypt | Air |
| X11 | D7 | X11 | Air |
| Kaspa | K7, KS5 | kHeavyHash | Air |
| Equihash | Z15 Pro | Equihash | Air |

### Whatsminer (MicroBT) — 8 models
M20, M20S, M30S, M31S, M50, M50S++, M60, M61

### Avalonminer (Canaan) — 9 models
Nano 3, Nano 3S, Mini 3, A1066, A1166, A1246, A1346, A1366, A15 XP

### IceRiver — 3 models (KS7 variants)
### U3 — 1 model (S21 XP Hyd 860T)
### Innosilicon — 1 model (T3+)

---

## Alternative Firmware Support

| Firmware | Auth | Notes |
|----------|------|-------|
| Braiins OS+ | HTTP Basic | Profiles, auto-tuning, open source |
| Vnish | HTTP Basic | Per-chip frequency tuning |
| LuxOS | Bearer token | Power-target mode, OTA by URL |
| Hiveon | HTTP Basic | Cloud management |

goasic auto-detects which firmware is running via HTTP fingerprinting.

---

## Installation

```bash
go get github.com/goasic/goasic
```

Zero external dependencies — pure Go standard library.

---

## Quick Start

```go
ctx := context.Background()

// Auto-detect miner type
miner, _ := goasic.Detect(ctx, "192.168.1.50")

// Live data
data, _ := miner.GetData(ctx)
fmt.Println(goasic.Summary(data))
// [192.168.1.50] Antminer S21 XP | 268.45 TH/s (99.4%) | mining | algo:SHA-256d | cool:Air

// Pool config
cfg := goasic.MinerConfig{}
cfg.AddPool("stratum+tcp://pool.example.com:3333", "worker.001", "x")
miner.SendConfig(ctx, cfg)

// Operating mode
miner.SetMode(ctx, goasic.ModeLowPower)  // LPM
miner.SetMode(ctx, goasic.ModeNormal)   // Normal
miner.SetMode(ctx, goasic.ModeHighPerf) // Turbo

// Fan speed
miner.SetFanSpeed(ctx, 60)  // 60% PWM
miner.SetFanSpeed(ctx, -1)  // auto

// Firmware update (URL or local file)
miner.UpdateFirmware(ctx, goasic.FirmwareInfo{
    URL: "https://example.com/firmware.bin",
})

// Alternative firmware drivers
bos, _ := goasic.NewBraiinsOS("192.168.1.51", "root", "admin")
lux, _ := goasic.NewLuxOS("192.168.1.52", "my-bearer-token")
vni, _ := goasic.NewVnish("192.168.1.53", "root", "admin")
hiv, _ := goasic.NewHiveon("192.168.1.54", "root", "admin")
inn, _ := goasic.NewInnosilicon("192.168.1.55")

// Subnet scan
miners, _ := goasic.ScanSubnet(ctx, "192.168.1.0/24", 100)

// MinerDB lookup
spec := goasic.DBGet("Antminer S21 XP")
fmt.Printf("%.0f TH/s @ %dW = %.1f J/TH\n",
    spec.HashrateTHS, spec.PowerWatts,
    float64(spec.PowerWatts)/spec.HashrateTHS)
```

---

## CLI Scan Tool

```bash
go run ./cmd/scan -subnet 192.168.1.0/24 -concurrency 100
go run ./cmd/scan -subnet 192.168.1.0/24 -json
```

---

## Running Tests

```bash
go test ./...           # all 85 tests
go test -v ./...        # verbose
go test -race ./...     # race detector
```

---

## Project Structure

```
goasic/
├── goasic.go                  # Public API
├── go.mod                     # Zero external dependencies
├── cmd/scan/main.go           # CLI tool
└── pkg/
    ├── minerdb/               # Embedded 68-model database
    ├── miners/
    │   ├── types.go           # Miner interface (14 methods)
    │   ├── factory.go         # Auto-detection
    │   ├── firmware.go        # Shared firmware helpers
    │   ├── antminer.go        # Bitmain all variants
    │   ├── whatsminer.go      # MicroBT + BTMiner token auth
    │   ├── avalonminer.go     # Canaan
    │   ├── iceriver.go        # IceRiver REST
    │   ├── u3miner.go         # U3 hydro
    │   ├── innosilicon.go     # Innosilicon
    │   └── altfirmware.go     # BraiinsOS / Vnish / LuxOS / Hiveon
    ├── rpc/rpc.go             # cgminer JSON-RPC client
    └── network/network.go     # Concurrent subnet scanner
```

## License

MIT
