package miners

import (
	"testing"
)

// ── DetectFirmwareClass ───────────────────────────────────────────────────────

func TestDetectFirmwareClass(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"Antminer S21 XP Hyd", "hydro"},
		{"Antminer S19e XP Hyd1", "hydro"},
		{"Antminer S21+ Hyd2", "hydro"},
		{"Antminer S19 XP+ Hyd", "hydro"},
		{"U3 S21 XP Hyd", "hydro"},
		{"Antminer S21 Immersion", "immersion"},
		{"Antminer S21 XP Imm", "immersion"},
		{"Antminer L71", "altair"},
		{"Antminer L72", "altair"},
		{"Antminer L91", "altair"},
		{"Antminer D7", "altair"},
		{"Antminer K7", "altair"},
		{"Antminer KS5", "altair"},
		{"Antminer Z15 Pro", "altair"},
		{"Antminer S21 XP", "standard"},
		{"Antminer S19 Pro", "standard"},
		{"Antminer S9", "standard"},
		{"Whatsminer M60", "standard"},
	}
	for _, c := range cases {
		got := DetectFirmwareClass(c.model)
		if got != c.want {
			t.Errorf("DetectFirmwareClass(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}

// ── MinerData.EnrichFromDB ────────────────────────────────────────────────────

func TestMinerDataEnrichFromDB_ExpectedHashrate(t *testing.T) {
	d := &MinerData{Model: "Antminer S21 XP"}
	d.EnrichFromDB()
	if d.ExpectedHashrate == nil {
		t.Fatal("EnrichFromDB: ExpectedHashrate is nil")
	}
	if *d.ExpectedHashrate != 270.0 {
		t.Errorf("ExpectedHashrate = %v, want 270.0", *d.ExpectedHashrate)
	}
}

func TestMinerDataEnrichFromDB_Cooling(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"Antminer S21 XP Hyd", "Hydro"},
		{"Antminer S21 Immersion", "Immersion"},
		{"Antminer S21 XP", "Air"},
	}
	for _, c := range cases {
		d := &MinerData{Model: c.model}
		d.EnrichFromDB()
		if d.Cooling != c.want {
			t.Errorf("EnrichFromDB(%q).Cooling = %q, want %q", c.model, d.Cooling, c.want)
		}
	}
}

func TestMinerDataEnrichFromDB_Algorithm(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"Antminer L71", "Scrypt"},
		{"Antminer Z15 Pro", "Equihash"},
		{"IceRiver KS71", "kHeavyHash"},
		{"Antminer S21 XP", "SHA-256d"},
	}
	for _, c := range cases {
		d := &MinerData{Model: c.model}
		d.EnrichFromDB()
		if d.Algorithm != c.want {
			t.Errorf("EnrichFromDB(%q).Algorithm = %q, want %q", c.model, d.Algorithm, c.want)
		}
	}
}

func TestMinerDataEnrichFromDB_HashratePct(t *testing.T) {
	actual := 270.0
	d := &MinerData{
		Model:    "Antminer S21 XP",
		Hashrate: &actual,
	}
	d.EnrichFromDB()
	if d.HashratePct == nil {
		t.Fatal("HashratePct is nil after enrichment")
	}
	// 270 / 270 * 100 = 100%
	if !approxEq(*d.HashratePct, 100.0, 0.01) {
		t.Errorf("HashratePct = %.2f, want ~100.0", *d.HashratePct)
	}
}

func TestMinerDataEnrichFromDB_PartialHashratePct(t *testing.T) {
	actual := 256.5 // less than rated 270
	d := &MinerData{
		Model:    "Antminer S21 XP",
		Hashrate: &actual,
	}
	d.EnrichFromDB()
	if d.HashratePct == nil {
		t.Fatal("HashratePct is nil")
	}
	expected := 256.5 / 270.0 * 100
	if !approxEq(*d.HashratePct, expected, 0.01) {
		t.Errorf("HashratePct = %.2f, want %.2f", *d.HashratePct, expected)
	}
}

func TestMinerDataEnrichFromDB_Efficiency(t *testing.T) {
	actual := 270.0
	watts := 3645
	d := &MinerData{
		Model:    "Antminer S21 XP",
		Hashrate: &actual,
		Wattage:  &watts,
	}
	d.EnrichFromDB()
	if d.Efficiency == nil {
		t.Fatal("Efficiency is nil")
	}
	// 3645W / 270TH = 13.5 J/TH
	expected := 3645.0 / 270.0
	if !approxEq(*d.Efficiency, expected, 0.001) {
		t.Errorf("Efficiency = %.4f J/TH, want %.4f", *d.Efficiency, expected)
	}
}

func TestMinerDataEnrichFromDB_DoesNotOverwriteExisting(t *testing.T) {
	// If fields are already set, EnrichFromDB should not overwrite them
	existingAlgo := "CustomAlgo"
	existingCooling := "CustomCooling"
	chips := 999
	d := &MinerData{
		Model:     "Antminer S21 XP",
		Algorithm: existingAlgo,
		Cooling:   existingCooling,
		ChipCount: &chips,
	}
	d.EnrichFromDB()
	if d.Algorithm != existingAlgo {
		t.Errorf("EnrichFromDB overwrote Algorithm: got %q", d.Algorithm)
	}
	if d.Cooling != existingCooling {
		t.Errorf("EnrichFromDB overwrote Cooling: got %q", d.Cooling)
	}
	if *d.ChipCount != 999 {
		t.Errorf("EnrichFromDB overwrote ChipCount: got %d", *d.ChipCount)
	}
}

func TestMinerDataEnrichFromDB_UnknownModel(t *testing.T) {
	d := &MinerData{Model: "FakeMiner X9999"}
	d.EnrichFromDB()
	// Should not crash; fields remain empty
	if d.ExpectedHashrate != nil {
		t.Error("Unknown model should not set ExpectedHashrate")
	}
}

// ── Antminer.parseHashrate ────────────────────────────────────────────────────

func TestAntminerParseHashrate(t *testing.T) {
	a := &Antminer{}

	cases := []struct {
		model   string
		fields  map[string]interface{}
		wantTHS float64
		wantNil bool
	}{
		// Standard SHA-256d — GHS 5s → /1000
		{
			model:   "Antminer S21 XP",
			fields:  map[string]interface{}{"GHS 5s": 270000.0},
			wantTHS: 270.0,
		},
		// Scrypt L9 — GHS 5s → /1000
		{
			model:   "Antminer L91",
			fields:  map[string]interface{}{"GHS 5s": 15000.0},
			wantTHS: 15.0,
		},
		// Scrypt L7 — MHS 5s → /1,000,000
		{
			model:   "Antminer L71",
			fields:  map[string]interface{}{"MHS 5s": 9050.0},
			wantTHS: 9050.0 / 1_000_000,
		},
		// L7 does NOT use GHS 5s, falls back to MHS
		{
			model:   "Antminer L71",
			fields:  map[string]interface{}{"GHS 5s": 0.0, "MHS 5s": 8800.0},
			wantTHS: 8800.0 / 1_000_000,
		},
		// Equihash Z15 Pro — KSols/s
		{
			model:   "Antminer Z15 Pro",
			fields:  map[string]interface{}{"KSols/s": 840.0},
			wantTHS: 840.0,
		},
		// Equihash fallback: Sol/s → /1000
		{
			model:   "Antminer Z15 Pro",
			fields:  map[string]interface{}{"Sol/s": 840000.0},
			wantTHS: 840.0,
		},
		// X11 D7 — GHS 5s
		{
			model:   "Antminer D7",
			fields:  map[string]interface{}{"GHS 5s": 1286000.0},
			wantTHS: 1286.0,
		},
		// kHeavyHash K7 — GHS 5s
		{
			model:   "Antminer K7",
			fields:  map[string]interface{}{"GHS 5s": 63500.0},
			wantTHS: 63.5,
		},
		// No hashrate fields at all
		{
			model:   "Antminer S19 Pro",
			fields:  map[string]interface{}{},
			wantNil: true,
		},
	}

	for _, c := range cases {
		got := a.parseHashrate(c.fields, c.model)
		if c.wantNil {
			if got != nil {
				t.Errorf("parseHashrate(%q) = %v, want nil", c.model, *got)
			}
			continue
		}
		if got == nil {
			t.Errorf("parseHashrate(%q) = nil, want %v TH/s", c.model, c.wantTHS)
			continue
		}
		if !approxEq(*got, c.wantTHS, 1e-9) {
			t.Errorf("parseHashrate(%q) = %.10f, want %.10f", c.model, *got, c.wantTHS)
		}
	}
}

// ── Avalonminer sensor parsing ────────────────────────────────────────────────

func TestParseAvalonField_Temps(t *testing.T) {
	encoded := "TA[55 60 58] FAN[1200 1350] TEMP[65]"
	temps := parseAvalonField(encoded, "TA")
	if len(temps) != 3 {
		t.Fatalf("parseAvalonField temps: got %d values, want 3", len(temps))
	}
	want := []float64{55, 60, 58}
	for i, w := range want {
		if temps[i] != w {
			t.Errorf("temps[%d] = %v, want %v", i, temps[i], w)
		}
	}
}

func TestParseAvalonField_Fans(t *testing.T) {
	encoded := "TA[55 60] FAN[1200 1350 0] TEMP[65]"
	fans := parseAvalonFieldInt(encoded, "FAN")
	// 0-value fans should be excluded
	if len(fans) != 2 {
		t.Fatalf("parseAvalonFieldInt fans: got %d values, want 2", len(fans))
	}
	if fans[0] != 1200 || fans[1] != 1350 {
		t.Errorf("fans = %v, want [1200 1350]", fans)
	}
}

func TestParseAvalonField_MissingTag(t *testing.T) {
	encoded := "FAN[1200] TEMP[65]"
	temps := parseAvalonField(encoded, "TA")
	if len(temps) != 0 {
		t.Errorf("parseAvalonField missing tag: got %v, want empty", temps)
	}
}

func TestParseAvalonField_Empty(t *testing.T) {
	temps := parseAvalonField("", "TA")
	if len(temps) != 0 {
		t.Errorf("parseAvalonField empty: got %v, want empty", temps)
	}
}

func TestParseAvalonField_MultipleModules(t *testing.T) {
	// Simulate calling for each MM IDx field
	modules := []string{
		"TA[50 55] FAN[1100]",
		"TA[60 65] FAN[1200]",
	}
	var allTemps []float64
	for _, m := range modules {
		allTemps = append(allTemps, parseAvalonField(m, "TA")...)
	}
	if len(allTemps) != 4 {
		t.Errorf("multi-module temps: got %d values, want 4", len(allTemps))
	}
}

// ── Whatsminer MD5 token ──────────────────────────────────────────────────────

func TestComputeWhatsminerToken(t *testing.T) {
	// Verify the two-step MD5 computation:
	// MD5("admin") = "21232f297a57a5a743894a0e4a801fc3"
	// MD5("abc" + "21232f297a57a5a743894a0e4a801fc3") = known value

	// Known test vector: salt="abc", password="admin"
	// Step 1: MD5("admin") = "21232f297a57a5a743894a0e4a801fc3"
	// Step 2: MD5("abc21232f297a57a5a743894a0e4a801fc3")
	token := computeWhatsminerToken("abc", "admin")
	if len(token) != 32 {
		t.Errorf("token length = %d, want 32 hex chars", len(token))
	}
	// Verify it's all hex
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("token contains non-hex char %q: %s", c, token)
		}
	}

	// Deterministic: same inputs always produce same output
	token2 := computeWhatsminerToken("abc", "admin")
	if token != token2 {
		t.Error("computeWhatsminerToken is not deterministic")
	}

	// Different salt → different token
	token3 := computeWhatsminerToken("xyz", "admin")
	if token == token3 {
		t.Error("different salts should produce different tokens")
	}

	// Different password → different token
	token4 := computeWhatsminerToken("abc", "password123")
	if token == token4 {
		t.Error("different passwords should produce different tokens")
	}
}

func TestComputeWhatsminerToken_EmptyInputs(t *testing.T) {
	// Should not panic
	token := computeWhatsminerToken("", "")
	if len(token) != 32 {
		t.Errorf("empty input token length = %d, want 32", len(token))
	}
}

// ── MinerConfig helpers ───────────────────────────────────────────────────────

func TestMinerConfigAddPool(t *testing.T) {
	cfg := &MinerConfig{}
	cfg.AddPool("stratum+tcp://pool.example.com:3333", "worker.001", "x")
	cfg.AddPool("stratum+tcp://backup.example.com:3333", "worker.002", "x")

	if len(cfg.Pools) != 2 {
		t.Fatalf("len(Pools) = %d, want 2", len(cfg.Pools))
	}
	if cfg.Pools[0].URL != "stratum+tcp://pool.example.com:3333" {
		t.Errorf("Pools[0].URL = %q", cfg.Pools[0].URL)
	}
	if cfg.Pools[1].User != "worker.002" {
		t.Errorf("Pools[1].User = %q", cfg.Pools[1].User)
	}
}

func TestMinerConfigPool_OutOfBounds(t *testing.T) {
	cfg := &MinerConfig{}
	cfg.AddPool("url", "user", "pw")
	p := cfg.Pool(5) // out of range
	if p.URL != "" || p.User != "" {
		t.Errorf("Pool(out-of-bounds) should return zero PoolConfig, got %+v", p)
	}
}

func TestPoolPW_Default(t *testing.T) {
	// poolPW returns "x" when password is empty
	p := PoolConfig{URL: "url", User: "user", Password: ""}
	if pw := poolPW(p); pw != "x" {
		t.Errorf("poolPW empty password = %q, want 'x'", pw)
	}
	p2 := PoolConfig{URL: "url", User: "user", Password: "mypass"}
	if pw := poolPW(p2); pw != "mypass" {
		t.Errorf("poolPW with password = %q, want 'mypass'", pw)
	}
}

// ── IceRiver unit conversion ──────────────────────────────────────────────────

func TestConvertHashrateToTHS(t *testing.T) {
	cases := []struct {
		value float64
		unit  string
		want  float64
	}{
		{36000.0, "GH/S", 36.0},
		{36000.0, "GHS", 36.0},
		{36.0, "TH/s", 36.0},
		{36.0, "TH/S", 36.0},
		{36000000.0, "MH/S", 36.0},
		{36000000000.0, "KH/S", 36.0},
		{36.0, "", 36.0}, // default TH/s
	}
	for _, c := range cases {
		got := convertHashrateToTHS(c.value, c.unit)
		if !approxEq(got, c.want, 1e-6) {
			t.Errorf("convertHashrateToTHS(%v, %q) = %v, want %v", c.value, c.unit, got, c.want)
		}
	}
}

// ── FormatSummary ─────────────────────────────────────────────────────────────

func TestFormatSummary_Mining(t *testing.T) {
	hr := 268.45
	pct := 99.4
	d := &MinerData{
		IP:          "192.168.1.50",
		Make:        "Antminer",
		Model:       "S21 XP",
		Hashrate:    &hr,
		HashratePct: &pct,
		Algorithm:   "SHA-256d",
		Cooling:     "Air",
		IsMining:    true,
	}
	got := FormatSummary(d)
	if got == "" {
		t.Error("FormatSummary returned empty string")
	}
	for _, substr := range []string{"192.168.1.50", "Antminer", "268.45", "99.4", "mining", "SHA-256d", "Air"} {
		found := false
		for i := 0; i <= len(got)-len(substr); i++ {
			if got[i:i+len(substr)] == substr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FormatSummary output missing %q: got %q", substr, got)
		}
	}
}

func TestFormatSummary_Idle(t *testing.T) {
	d := &MinerData{
		IP:       "10.0.0.1",
		Make:     "Whatsminer",
		IsMining: false,
	}
	got := FormatSummary(d)
	for i := 0; i <= len(got)-len("idle"); i++ {
		if got[i:i+4] == "idle" {
			return
		}
	}
	t.Errorf("FormatSummary idle: expected 'idle' in output, got %q", got)
}

func TestFormatSummary_NoHashrate(t *testing.T) {
	d := &MinerData{IP: "10.0.0.2", Make: "IceRiver"}
	got := FormatSummary(d)
	if got == "" {
		t.Error("FormatSummary should not return empty even with nil hashrate")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func approxEq(a, b, tol float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tol
}
