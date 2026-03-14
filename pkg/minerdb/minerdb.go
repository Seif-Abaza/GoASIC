// Package minerdb provides a compile-time embedded lookup table of all known
// ASIC miner models, parsed from MinerDB.csv. Drivers call DB.Get(modelName)
// to retrieve the expected hashrate, power, algorithm, cooling, and chip count.
package minerdb

import (
	_ "embed"
	"encoding/csv"
	"strings"
	"strconv"
	"sync"
)

//go:embed minerdb.csv
var csvData string

// Algorithm represents the mining proof-of-work algorithm.
type Algorithm string

const (
	AlgoSHA256d    Algorithm = "SHA-256d"
	AlgoScrypt     Algorithm = "Scrypt"
	AlgoX11        Algorithm = "X11"
	AlgoEquihash   Algorithm = "Equihash"
	AlgoKHeavyHash Algorithm = "kHeavyHash"
	AlgoBlake2b    Algorithm = "Blake2B+SHA3"
	AlgoUnknown    Algorithm = "Unknown"
)

// Cooling represents the cooling method used by the miner.
type Cooling string

const (
	CoolingAir       Cooling = "Air"
	CoolingHydro     Cooling = "Hydro"
	CoolingImmersion Cooling = "Immersion"
	CoolingUnknown   Cooling = "Unknown"
)

// MinerSpec holds the static specification for one miner model.
type MinerSpec struct {
	Model        string
	HashrateRaw  string  // e.g. "270T", "9050M", "840K"
	HashrateTHS  float64 // normalised to TH/s (or TH/s-equivalent)
	Cooling      Cooling
	PowerWatts   int
	Fans         int
	ASICChips    int
	Algorithm    Algorithm
}

var (
	once sync.Once
	db   map[string]*MinerSpec
)

// Get looks up a model by name (case-insensitive, partial match allowed).
// Returns nil if no match is found.
func Get(modelName string) *MinerSpec {
	once.Do(buildDB)
	key := strings.ToLower(strings.TrimSpace(modelName))
	if spec, ok := db[key]; ok {
		return spec
	}
	// Partial match fallback
	for k, spec := range db {
		if strings.Contains(key, k) || strings.Contains(k, key) {
			return spec
		}
	}
	return nil
}

// All returns all known MinerSpec entries.
func All() []*MinerSpec {
	once.Do(buildDB)
	result := make([]*MinerSpec, 0, len(db))
	seen := map[string]bool{}
	for _, spec := range db {
		if !seen[spec.Model] {
			result = append(result, spec)
			seen[spec.Model] = true
		}
	}
	return result
}

// Count returns the number of known models.
func Count() int {
	once.Do(buildDB)
	return len(db)
}

func buildDB() {
	db = make(map[string]*MinerSpec)
	r := csv.NewReader(strings.NewReader(csvData))
	records, err := r.ReadAll()
	if err != nil {
		return
	}
	for i, rec := range records {
		if i == 0 {
			continue // skip header
		}
		if len(rec) < 7 {
			continue
		}
		model       := strings.TrimSpace(rec[0])
		hashrateRaw := strings.TrimSpace(rec[1])
		coolingStr  := strings.TrimSpace(rec[2])
		powerStr    := strings.TrimSpace(rec[4])
		fansStr     := strings.TrimSpace(rec[5])
		chipsStr    := strings.TrimSpace(rec[6])

		if model == "" {
			continue
		}

		power, _  := strconv.Atoi(powerStr)
		fans, _   := strconv.Atoi(fansStr)
		chips, _  := strconv.Atoi(chipsStr)

		spec := &MinerSpec{
			Model:       model,
			HashrateRaw: hashrateRaw,
			HashrateTHS: parseHashrateToTHS(hashrateRaw),
			Cooling:     parseCooling(coolingStr),
			PowerWatts:  power,
			Fans:        fans,
			ASICChips:   chips,
			Algorithm:   inferAlgorithm(model),
		}
		db[strings.ToLower(model)] = spec
	}
}

// parseHashrateToTHS converts "270T"→270.0, "9050M"→0.00905, "15G"→0.015, "840K"→0.00000084
func parseHashrateToTHS(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	suffix := string(s[len(s)-1])
	numStr := s[:len(s)-1]
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(suffix) {
	case "T":
		return num
	case "G":
		return num / 1_000
	case "M":
		return num / 1_000_000
	case "K":
		return num / 1_000_000_000
	default:
		return num
	}
}

func parseCooling(s string) Cooling {
	switch strings.ToLower(s) {
	case "air":
		return CoolingAir
	case "hyd", "hydro":
		return CoolingHydro
	case "imm", "immersion":
		return CoolingImmersion
	default:
		return CoolingUnknown
	}
}

func inferAlgorithm(model string) Algorithm {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, " l7") || strings.Contains(m, " l9") || strings.Contains(m, " l3"):
		return AlgoScrypt
	case strings.Contains(m, " d7") || strings.Contains(m, " d9"):
		return AlgoX11
	case strings.Contains(m, " z15") || strings.Contains(m, " z9"):
		return AlgoEquihash
	case strings.Contains(m, " ks") || strings.Contains(m, "iceriver") || strings.Contains(m, " k7"):
		return AlgoKHeavyHash
	case strings.Contains(m, "sia") || strings.Contains(m, " a3"):
		return AlgoBlake2b
	default:
		return AlgoSHA256d
	}
}

// Silence unused import warning from math
