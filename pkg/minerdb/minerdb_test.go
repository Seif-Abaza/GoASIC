package minerdb

import (
	"testing"
)

// ── parseHashrateToTHS ────────────────────────────────────────────────────────

func TestParseHashrateToTHS(t *testing.T) {
	cases := []struct {
		input string
		want  float64
	}{
		{"270T", 270.0},
		{"141T", 141.0},
		{"860T", 860.0},
		{"9050M", 9050.0 / 1_000_000},
		{"8800M", 8800.0 / 1_000_000},
		{"15G", 15.0 / 1_000},
		{"16.5G", 16.5 / 1_000},
		{"840K", 840.0 / 1_000_000_000},
		{"36T", 36.0},
		{"200T", 200.0},
		{"", 0.0},
	}
	for _, c := range cases {
		got := parseHashrateToTHS(c.input)
		if !approxEqual(got, c.want, 1e-12) {
			t.Errorf("parseHashrateToTHS(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ── parseCooling ──────────────────────────────────────────────────────────────

func TestParseCooling(t *testing.T) {
	cases := []struct {
		input string
		want  Cooling
	}{
		{"Air", CoolingAir},
		{"air", CoolingAir},
		{"Hyd", CoolingHydro},
		{"Hydro", CoolingHydro},
		{"hydro", CoolingHydro},
		{"Imm", CoolingImmersion},
		{"Immersion", CoolingImmersion},
		{"IMMERSION", CoolingImmersion},
		{"unknown", CoolingUnknown},
		{"", CoolingUnknown},
	}
	for _, c := range cases {
		got := parseCooling(c.input)
		if got != c.want {
			t.Errorf("parseCooling(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ── inferAlgorithm ────────────────────────────────────────────────────────────

func TestInferAlgorithm(t *testing.T) {
	cases := []struct {
		model string
		want  Algorithm
	}{
		{"Antminer L7", AlgoScrypt},
		{"Antminer L9", AlgoScrypt},
		{"Antminer L3+", AlgoScrypt},
		{"Antminer D7", AlgoX11},
		{"Antminer D9", AlgoX11},
		{"Antminer Z15 Pro", AlgoEquihash},
		{"Antminer Z9", AlgoEquihash},
		{"Antminer K7", AlgoKHeavyHash},
		{"Antminer KS5", AlgoKHeavyHash},
		{"IceRiver KS7", AlgoKHeavyHash},
		{"Antminer S21 XP", AlgoSHA256d},
		{"Antminer S19 Pro", AlgoSHA256d},
		{"Whatsminer M60", AlgoSHA256d},
		{"U3 S21 XP Hyd", AlgoSHA256d},
	}
	for _, c := range cases {
		got := inferAlgorithm(c.model)
		if got != c.want {
			t.Errorf("inferAlgorithm(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

// ── DB.Get ────────────────────────────────────────────────────────────────────

func TestDBGet_ExactMatch(t *testing.T) {
	spec := Get("Antminer S21 XP")
	if spec == nil {
		t.Fatal("Get('Antminer S21 XP') returned nil")
	}
	if spec.HashrateTHS != 270.0 {
		t.Errorf("HashrateTHS = %v, want 270.0", spec.HashrateTHS)
	}
	if spec.Cooling != CoolingAir {
		t.Errorf("Cooling = %v, want Air", spec.Cooling)
	}
	if spec.PowerWatts != 3645 {
		t.Errorf("PowerWatts = %v, want 3645", spec.PowerWatts)
	}
	if spec.Algorithm != AlgoSHA256d {
		t.Errorf("Algorithm = %v, want SHA-256d", spec.Algorithm)
	}
}

func TestDBGet_CaseInsensitive(t *testing.T) {
	upper := Get("ANTMINER S21 XP")
	lower := Get("antminer s21 xp")
	mixed := Get("Antminer S21 XP")
	if upper == nil || lower == nil || mixed == nil {
		t.Errorf("case-insensitive lookup failed: upper=%v lower=%v mixed=%v", upper, lower, mixed)
	}
}

func TestDBGet_HydroModel(t *testing.T) {
	spec := Get("Antminer S21 XP Hyd")
	if spec == nil {
		t.Fatal("Get('Antminer S21 XP Hyd') returned nil")
	}
	if spec.Cooling != CoolingHydro {
		t.Errorf("Cooling = %v, want Hydro", spec.Cooling)
	}
	if spec.Fans != 0 {
		t.Errorf("Fans = %v, want 0 (hydro has no fans)", spec.Fans)
	}
	if spec.HashrateTHS != 473.0 {
		t.Errorf("HashrateTHS = %v, want 473.0", spec.HashrateTHS)
	}
}

func TestDBGet_ImmersionModel(t *testing.T) {
	spec := Get("Antminer S21 Immersion")
	if spec == nil {
		t.Fatal("Get('Antminer S21 Immersion') returned nil")
	}
	if spec.Cooling != CoolingImmersion {
		t.Errorf("Cooling = %v, want Immersion", spec.Cooling)
	}
}

func TestDBGet_IceRiver(t *testing.T) {
	spec := Get("IceRiver KS71")
	if spec == nil {
		t.Fatal("Get('IceRiver KS71') returned nil")
	}
	if spec.Algorithm != AlgoKHeavyHash {
		t.Errorf("Algorithm = %v, want kHeavyHash", spec.Algorithm)
	}
	if spec.HashrateTHS != 36.0 {
		t.Errorf("HashrateTHS = %v, want 36.0", spec.HashrateTHS)
	}
}

func TestDBGet_U3Model(t *testing.T) {
	spec := Get("U3 S21 XP Hyd")
	if spec == nil {
		t.Fatal("Get('U3 S21 XP Hyd') returned nil")
	}
	if spec.HashrateTHS != 860.0 {
		t.Errorf("HashrateTHS = %v, want 860.0", spec.HashrateTHS)
	}
	if spec.Cooling != CoolingHydro {
		t.Errorf("Cooling = %v, want Hydro", spec.Cooling)
	}
}

func TestDBGet_ScryptL7(t *testing.T) {
	spec := Get("Antminer L71")
	if spec == nil {
		t.Fatal("Get('Antminer L71') returned nil")
	}
	if spec.Algorithm != AlgoScrypt {
		t.Errorf("Algorithm = %v, want Scrypt", spec.Algorithm)
	}
	// L7 hashrate is ~0.00905 TH/s-equivalent (9050 MH/s)
	expected := 9050.0 / 1_000_000
	if !approxEqual(spec.HashrateTHS, expected, 1e-8) {
		t.Errorf("HashrateTHS = %v, want %v (9050M)", spec.HashrateTHS, expected)
	}
}

func TestDBGet_EquihashZ15(t *testing.T) {
	spec := Get("Antminer Z15 Pro")
	if spec == nil {
		t.Fatal("Get('Antminer Z15 Pro') returned nil")
	}
	if spec.Algorithm != AlgoEquihash {
		t.Errorf("Algorithm = %v, want Equihash", spec.Algorithm)
	}
}

func TestDBGet_UnknownModel(t *testing.T) {
	spec := Get("NonExistentMiner XYZ 9999")
	if spec != nil {
		t.Errorf("Get for unknown model should return nil, got %v", spec.Model)
	}
}

// ── DB.Count ──────────────────────────────────────────────────────────────────

func TestDBCount(t *testing.T) {
	count := Count()
	if count < 30 {
		t.Errorf("Count() = %d, expected at least 30 models", count)
	}
	t.Logf("Total models in DB: %d", count)
}

// ── DB.All ────────────────────────────────────────────────────────────────────

func TestDBAll_NoDuplicates(t *testing.T) {
	all := All()
	seen := map[string]bool{}
	for _, spec := range all {
		if seen[spec.Model] {
			t.Errorf("Duplicate model in DB.All(): %q", spec.Model)
		}
		seen[spec.Model] = true
	}
}

func TestDBAll_NoZeroHashrate(t *testing.T) {
	for _, spec := range All() {
		if spec.HashrateTHS == 0 && spec.HashrateRaw != "" {
			t.Errorf("Model %q has non-empty HashrateRaw %q but parsed to 0 TH/s",
				spec.Model, spec.HashrateRaw)
		}
	}
}

func TestDBAll_ValidAlgorithms(t *testing.T) {
	valid := map[Algorithm]bool{
		AlgoSHA256d: true, AlgoScrypt: true, AlgoX11: true,
		AlgoEquihash: true, AlgoKHeavyHash: true, AlgoBlake2b: true, AlgoUnknown: true,
	}
	for _, spec := range All() {
		if !valid[spec.Algorithm] {
			t.Errorf("Model %q has invalid Algorithm %q", spec.Model, spec.Algorithm)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func approxEqual(a, b, tolerance float64) bool {
	if a == b {
		return true
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}
