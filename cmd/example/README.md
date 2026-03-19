# Simple Network Scan Example

This example demonstrates how to scan a network subnet for ASIC miners and display their data.

## Usage

Run the example with:

```bash
go run ./cmd/example
```

## What it does

1. Scans the subnet `10.10.0.1/24` for ASIC miners
2. Detects miner types automatically
3. Fetches live data from each discovered miner
4. Displays a table with key information:
   - IP address
   - Brand (e.g., Antminer, Whatsminer)
   - Model
   - Current hashrate
   - Maximum temperature
   - Power consumption
   - Mining status (✓ = active, ✗ = inactive)

## Output Example

```
Scanning subnet 10.10.0.1/24 with concurrency 100...
scanner: 255 hosts in subnet 10.10.0.1/24
scanner: found miner at 10.10.0.150 (Antminer)
scanner: complete — 1 miner(s) found
Found 1 miner(s):

IP              Brand      Model                Hashrate   Temp Max   Power      Mining
─────────────────────────────────────────────────────────────────────────────
10.10.0.150     Antminer                        118.34 TH/s —          2712W      ✓

Total: 1 miner(s) found
```

## Customization

You can modify the subnet and concurrency in the code:

```go
subnet := "10.10.0.1/24"  // Change this to your subnet
maxConcurrent := 100      // Adjust concurrency (50-200 recommended)
```