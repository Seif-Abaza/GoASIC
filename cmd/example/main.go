package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/goasic/goasic"
)

func main() {
	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Define the subnet to scan
	subnet := "10.10.0.1/24"
	maxConcurrent := 100

	fmt.Printf("Scanning subnet %s with concurrency %d...\n", subnet, maxConcurrent)

	// Scan the subnet for miners
	miners, err := goasic.ScanSubnet(ctx, subnet, maxConcurrent)
	if err != nil {
		log.Fatalf("Scan failed: %v", err)
	}

	if len(miners) == 0 {
		fmt.Println("No miners found.")
		return
	}

	fmt.Printf("Found %d miner(s):\n\n", len(miners))

	// Print header
	fmt.Printf("%-15s %-10s %-20s %-10s %-10s %-10s %-10s\n",
		"IP", "Brand", "Model", "Hashrate", "Temp Max", "Power", "Mining")
	fmt.Println("─────────────────────────────────────────────────────────────────────────────")

	// Fetch and display data for each miner
	for _, miner := range miners {
		data, err := miner.GetData(ctx)
		if err != nil {
			fmt.Printf("%-15s Error: %v\n", miner.IP(), err)
			continue
		}

		// Format hashrate
		hashrate := "—"
		if data.Hashrate != nil {
			hashrate = fmt.Sprintf("%.2f TH/s", *data.Hashrate)
		}

		// Format max temperature
		tempMax := "—"
		if len(data.Temperature) > 0 {
			max := data.Temperature[0]
			for _, t := range data.Temperature[1:] {
				if t > max {
					max = t
				}
			}
			tempMax = fmt.Sprintf("%.0f°C", max)
		}

		// Format power
		power := "—"
		if data.Wattage != nil {
			power = fmt.Sprintf("%dW", *data.Wattage)
		}

		// Format mining status
		mining := "✓"
		if !data.IsMining {
			mining = "✗"
		}

		fmt.Printf("%-15s %-10s %-20s %-10s %-10s %-10s %-10s\n",
			data.IP, data.Make, data.Model, hashrate, tempMax, power, mining)
	}

	fmt.Printf("\nTotal: %d miner(s) found\n", len(miners))
}