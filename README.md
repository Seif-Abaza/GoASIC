# goasic

A Go library for managing Bitcoin and alt-coin ASIC miners — the Go equivalent of [pyasic](https://github.com/UpstreamData/pyasic).

Supports **58 models** across **5 brands** as of 2026.

---

## Features

- Auto-detects miner brand (Antminer, Whatsminer, Avalonminer, IceRiver, U3)
- All firmware classes handled: Air / Hydro / Immersion / Alt-algorithm / BTMiner token-auth
- Embedded `MinerDB.csv` for expected hashrate, chip count, algorithm, cooling lookup
- Concurrent subnet scanner
- JSON or table output from the CLI tool
- Context-aware — all network calls respect `context.Context` deadlines and cancellation

---

## Supported Models

### Antminer (Bitmain) — 42 models

| Family | Models | Algorithm | Cooling |
|--------|--------|-----------|---------|
| S9 | S9, S9K, S9SE | SHA-256d | Air |
| S19 | S19, S19 Pro, S19j, S19j Pro, S19K Pro (×2), S19 XP (×5), S19e XP Hyd, S19 XP+ Hyd, T19 | SHA-256d | Air / Hydro |
| S21 | S21, S21a, S21b, S21 Pro, S21 XP, S21 Imm, S21 XP Imm, S21e Hyd (×3), S21+ Hyd (×3), S21 XP Hyd | SHA-256d | Air / Hydro / Immersion |
| Scrypt | L7 (×2), L9 (×4) | Scrypt | Air |
| X11 | D7 | X11 | Air |
| Kaspa | K7, KS5 | kHeavyHash | Air |
| Equihash | Z15 Pro | Equihash | Air |

### Whatsminer (MicroBT) — 6 models
M30S, M31S, M50, M50S++, M60, M61

### Avalonminer (Canaan) — 6 models
Nano 3, Nano 3S, Mini 3, A1346, A1366, A15 XP

### IceRiver — 3 models (KS7 variants)
KS7 (36T), KS7 (40T), KS7 (45T)

### U3 — 1 model
S21 XP Hyd (860T)

---

## Installation

```bash
go get github.com/goasic/goasic
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "github.com/goasic/goasic"
)

func main() {
    ctx := context.Background()

    // Auto-detect miner type and connect
    miner, err := goasic.Detect(ctx, "192.168.1.50")
    if err != nil {
        panic(err)
    }

    // Get live snapshot
    data, err := miner.GetData(ctx)
    if err != nil {
        panic(err)
    }
    fmt.Println(goasic.Summary(data))
    // [192.168.1.50] Antminer S21 XP | 268.45 TH/s (99.4%) | mining | algo:SHA-256d | cool:Air

    // Push pool config
    cfg := goasic.MinerConfig{}
    cfg.AddPool("stratum+tcp://pool.example.com:3333", "worker.001", "x")
    miner.SendConfig(ctx, cfg)

    // Fault LED
    miner.FaultLightOn(ctx)

    // Scan a whole subnet
    miners, _ := goasic.ScanSubnet(ctx, "192.168.1.0/24", 100)
    fmt.Printf("Found %d miners\n", len(miners))

    // Look up a model in the DB
    spec := goasic.DBGet("Antminer S21 XP")
    fmt.Printf("Expected: %.0f TH/s, %dW\n", spec.HashrateTHS, spec.PowerWatts)
}
```

## CLI Tool

```bash
# Scan and print a table
go run ./cmd/scan -subnet 192.168.1.0/24 -concurrency 100

# JSON output
go run ./cmd/scan -subnet 192.168.1.0/24 -json

# Flags
-subnet       CIDR to scan          (default: 192.168.1.0/24)
-concurrency  parallel probes       (default: 100)
-timeout      total scan timeout    (default: 2m)
-json         JSON output
```

## Project Structure

```
goasic/
├── goasic.go              # Public API (top-level package)
├── go.mod
├── cmd/
│   └── scan/main.go       # CLI scan tool
└── pkg/
    ├── minerdb/
    │   ├── minerdb.go     # Embedded MinerDB.csv lookup
    │   └── minerdb.csv    # 58-model database
    ├── miners/
    │   ├── types.go       # MinerData, MinerConfig, Miner interface
    │   ├── factory.go     # Auto-detection + IP validation
    │   ├── antminer.go    # Bitmain driver (Air/Hydro/Immersion/AltAlgo)
    │   ├── whatsminer.go  # MicroBT driver (Legacy + BTMiner token-auth)
    │   ├── avalonminer.go # Canaan driver
    │   ├── iceriver.go    # IceRiver REST driver (port 8080)
    │   └── u3miner.go     # U3 hydro driver (RPC + REST port 8888)
    ├── rpc/
    │   └── rpc.go         # cgminer JSON-RPC client (port 4028)
    └── network/
        └── network.go     # Concurrent subnet scanner
```

## Firmware Classes Handled

| Brand | Class | Details |
|-------|-------|---------|
| Antminer | Standard Air | CGI endpoints, GHS 5s hashrate |
| Antminer | Alt-Algo Air | L7→MHS, Z15→KSols/s, D7/K7→GHS |
| Antminer | Hydro | Token auth via `/cgi-bin/get_token.cgi`, JSON config push |
| Antminer | Immersion | Token auth, fan endpoints absent |
| Whatsminer | Legacy | Plain `updatePools` RPC |
| Whatsminer | BTMiner Token-Auth | MD5(salt+MD5(password)) enc token, `update_pools` |
| IceRiver | REST | HTTP on port 8080, GH/TH unit normalisation |
| U3 | Hydro REST | RPC on 4028 + REST on 8888 for hydro data |

## License

MIT
