// Command scan discovers ASIC miners on a subnet and prints a live data table.
//
// Usage:
//
//	go run ./cmd/scan -subnet 192.168.1.0/24 -concurrency 100
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"github.com/goasic/goasic"
)

func main() {
	subnet      := flag.String("subnet", "192.168.1.0/24", "CIDR subnet to scan")
	concurrency := flag.Int("concurrency", 100, "max concurrent probes")
	jsonOut     := flag.Bool("json", false, "output JSON instead of table")
	timeout     := flag.Duration("timeout", 2*time.Minute, "total scan timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	log.Printf("Scanning %s with concurrency %d …", *subnet, *concurrency)

	found, err := goasic.ScanSubnet(ctx, *subnet, *concurrency)
	if err != nil {
		log.Fatalf("scan failed: %v", err)
	}

	if len(found) == 0 {
		fmt.Println("No miners found.")
		return
	}

	// Fetch data for each miner
	type row struct {
		IP        string   `json:"ip"`
		Brand     string   `json:"brand"`
		Model     string   `json:"model"`
		Algorithm string   `json:"algorithm"`
		Cooling   string   `json:"cooling"`
		HashrTHS  *float64 `json:"hashrate_ths"`
		PctOfNom  *float64 `json:"hashrate_pct"`
		TempMax   float64  `json:"temp_max_c"`
		Power     *int     `json:"power_w"`
		EffJTH    *float64 `json:"efficiency_jth"`
		Pool      string   `json:"pool1_url"`
		Mining    bool     `json:"is_mining"`
		Uptime    string   `json:"uptime"`
	}

	var rows []row
	for _, m := range found {
		d, err := m.GetData(ctx)
		if err != nil {
			log.Printf("  %s: data error: %v", m.IP(), err)
			continue
		}
		r := row{
			IP:        d.IP,
			Brand:     d.Make,
			Model:     d.Model,
			Algorithm: d.Algorithm,
			Cooling:   d.Cooling,
			HashrTHS:  d.Hashrate,
			PctOfNom:  d.HashratePct,
			Power:     d.Wattage,
			EffJTH:    d.Efficiency,
			Pool:      d.Pool1URL,
			Mining:    d.IsMining,
		}
		if len(d.Temperature) > 0 {
			max := d.Temperature[0]
			for _, t := range d.Temperature[1:] {
				if t > max {
					max = t
				}
			}
			r.TempMax = max
		}
		if d.Uptime != nil {
			hours := *d.Uptime / 3600
			mins := (*d.Uptime % 3600) / 60
			r.Uptime = fmt.Sprintf("%dh%02dm", hours, mins)
		}
		rows = append(rows, r)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rows)
		return
	}

	// Table output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IP\tBrand\tModel\tAlgo\tCooling\tHashrate\t%Nom\tMaxTemp\tPower\tJ/TH\tMining\tUptime\tPool")
	fmt.Fprintln(w, "──\t─────\t─────\t────\t───────\t────────\t────\t───────\t─────\t────\t──────\t──────\t────")
	for _, r := range rows {
		hr := "—"
		if r.HashrTHS != nil {
			hr = fmt.Sprintf("%.2f TH/s", *r.HashrTHS)
		}
		pct := "—"
		if r.PctOfNom != nil {
			pct = fmt.Sprintf("%.1f%%", *r.PctOfNom)
		}
		power := "—"
		if r.Power != nil {
			power = fmt.Sprintf("%dW", *r.Power)
		}
		eff := "—"
		if r.EffJTH != nil {
			eff = fmt.Sprintf("%.1f", *r.EffJTH)
		}
		temp := "—"
		if r.TempMax > 0 {
			temp = fmt.Sprintf("%.0f°C", r.TempMax)
		}
		mining := "✓"
		if !r.Mining {
			mining = "✗"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.IP, r.Brand, r.Model, r.Algorithm, r.Cooling,
			hr, pct, temp, power, eff, mining, r.Uptime, r.Pool)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d miner(s) found\n", len(rows))
}
