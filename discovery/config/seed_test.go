package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSeedsTextOnePerLine(t *testing.T) {
	path := write(t, "seeds.txt", "Acme Industries\n\n# a comment\nContoso GmbH\n")
	got, err := LoadSeeds(path, 100)
	if err != nil {
		t.Fatalf("LoadSeeds: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Name != "Acme Industries" || got[1].Name != "Contoso GmbH" {
		t.Errorf("unexpected entries: %+v", got)
	}
}

func TestLoadSeedsTextKeepsACommaInsideAName(t *testing.T) {
	// "Acme, Inc." is one company. Splitting it would invent a second candidate
	// called "Inc." that no later stage can explain.
	path := write(t, "seeds.txt", "Acme, Inc.\n")
	got, err := LoadSeeds(path, 100)
	if err != nil {
		t.Fatalf("LoadSeeds: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(got), got)
	}
	if got[0].Name != "Acme, Inc." {
		t.Errorf("Name = %q, want the comma kept", got[0].Name)
	}
}

func TestLoadSeedsTextSplitsNameAndDomain(t *testing.T) {
	cases := []struct{ line, name, domain string }{
		{"Acme,acme.com", "Acme", "acme.com"},
		{"Acme\tacme.com", "Acme", "acme.com"},
		{"Acme, https://acme.com/about", "Acme", "acme.com"},
		{"Acme, WWW.Acme.COM", "Acme", "www.acme.com"},
		{"Acme, acme.co.uk", "Acme", "acme.co.uk"},
		{"Acme, münchen.de", "Acme", "münchen.de"},
	}
	for _, tc := range cases {
		path := write(t, "seeds.txt", tc.line+"\n")
		got, err := LoadSeeds(path, 100)
		if err != nil {
			t.Errorf("LoadSeeds(%q): %v", tc.line, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("LoadSeeds(%q) = %d entries, want 1", tc.line, len(got))
			continue
		}
		if got[0].Name != tc.name || got[0].Domain != tc.domain {
			t.Errorf("LoadSeeds(%q) = %+v, want name %q domain %q", tc.line, got[0], tc.name, tc.domain)
		}
	}
}

func TestLoadSeedsTextRejectsAnOverlongLineAsAnError(t *testing.T) {
	// A line that is not a company name is a mistake worth reporting, not a row
	// to skip: skipping half a seed file produces a run that looks fine.
	huge := strings.Repeat("A", 2<<20)
	path := write(t, "seeds.txt", "Acme\n"+huge+"\n")
	_, err := LoadSeeds(path, 100)
	if err == nil {
		t.Fatal("LoadSeeds = nil, want an error for a line beyond the size cap")
	}
	var pe *ParseError
	if !asParseError(err, &pe) {
		t.Fatalf("error is %T, want *ParseError naming the line", err)
	}
	if pe.Line != 2 {
		t.Errorf("Line = %d, want 2", pe.Line)
	}
}

func TestLoadSeedsCSVUsesTheHeader(t *testing.T) {
	// Column order is never guaranteed in a hand-made file, so guessing means
	// reading "acme.com,Acme" as a company named "acme.com".
	path := write(t, "seeds.csv", `domain,name,country,industry,employees,confidence
acme.com,Acme,US,Robotics,11-50,0.8
contoso.de,Contoso,DE,Logistics,51-200,
`)
	got, err := LoadSeeds(path, 100)
	if err != nil {
		t.Fatalf("LoadSeeds: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Name != "Acme" || got[0].Domain != "acme.com" || got[0].Country != "US" {
		t.Errorf("first entry mis-mapped: %+v", got[0])
	}
	if got[0].EmployeeHint != "11-50" {
		t.Errorf("EmployeeHint = %q, want 11-50", got[0].EmployeeHint)
	}
	if got[0].Confidence == nil || *got[0].Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8", got[0].Confidence)
	}
	if got[1].Confidence != nil {
		t.Errorf("Confidence = %v, want nil for a blank cell", *got[1].Confidence)
	}
}

func TestLoadSeedsCSVNeedsANameOrDomainColumn(t *testing.T) {
	path := write(t, "seeds.csv", "url,sector\nhttps://acme.com,robotics\n")
	_, err := LoadSeeds(path, 100)
	if err == nil {
		t.Fatal("a header with neither name nor domain must be an error, not a guess")
	}
}

func TestLoadSeedsCSVRejectsAMalformedRow(t *testing.T) {
	path := write(t, "seeds.csv", "name,domain\nAcme,acme.com\n\"unterminated,acme.com\n")
	_, err := LoadSeeds(path, 100)
	if err == nil {
		t.Fatal("a malformed CSV row must be an error")
	}
	if !strings.Contains(err.Error(), ":2:") && !strings.Contains(err.Error(), ":3:") {
		t.Errorf("error %q should name the offending line", err)
	}
}

func TestLoadSeedsCSVEmptyFileIsAnEmptyList(t *testing.T) {
	path := write(t, "seeds.csv", "")
	got, err := LoadSeeds(path, 100)
	if err != nil {
		t.Fatalf("an empty seed file should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

func TestLoadSeedsJSONArray(t *testing.T) {
	path := write(t, "seeds.json", `[
	  {"name":"Acme","domain":"ACME.com","country":"us"},
	  {"name":"Contoso","domain":"contoso.de","notes":"EU only"}
	]`)
	got, err := LoadSeeds(path, 100)
	if err != nil {
		t.Fatalf("LoadSeeds: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Domain != "acme.com" {
		t.Errorf("Domain = %q, want it lowercased", got[0].Domain)
	}
	if got[0].Country != "US" {
		t.Errorf("Country = %q, want it uppercased", got[0].Country)
	}
	if got[1].Notes != "EU only" {
		t.Errorf("Notes = %q", got[1].Notes)
	}
}

func TestLoadSeedsJSONObjectForms(t *testing.T) {
	for _, body := range []string{
		`{"companies":[{"name":"Acme","domain":"acme.com"}]}`,
		`{"entries":[{"name":"Acme","domain":"acme.com"}]}`,
	} {
		path := write(t, "seeds.json", body)
		got, err := LoadSeeds(path, 100)
		if err != nil {
			t.Errorf("LoadSeeds(%s): %v", body, err)
			continue
		}
		if len(got) != 1 || got[0].Name != "Acme" {
			t.Errorf("LoadSeeds(%s) = %+v", body, got)
		}
	}
}

func TestLoadSeedsJSONRejectsAnUnrecognisedShape(t *testing.T) {
	path := write(t, "seeds.json", `{"acme":{"name":"Acme"}}`)
	if _, err := LoadSeeds(path, 100); err == nil {
		t.Error("an object with no companies or entries key must be an error")
	}
}

func TestLoadSeedsJSONRejectsInvalidJSON(t *testing.T) {
	path := write(t, "seeds.json", `[{"name":`)
	if _, err := LoadSeeds(path, 100); err == nil {
		t.Error("invalid JSON must be an error")
	}
}

func TestLoadSeedsEnforcesTheEntryCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("Company\n")
	}
	path := write(t, "seeds.txt", b.String())
	if _, err := LoadSeeds(path, 10); err == nil {
		t.Error("exceeding the configured entry cap must be an error")
	}
	if _, err := LoadSeeds(path, 50); err != nil {
		t.Errorf("exactly the cap should be allowed: %v", err)
	}
}

func TestLoadSeedsRejectsAnUnknownExtension(t *testing.T) {
	// Guessing the format means reading a CSV as one column per line and
	// inventing company names out of the header.
	path := write(t, "seeds.xlsx", "name\nAcme\n")
	_, err := LoadSeeds(path, 100)
	if err == nil || !strings.Contains(err.Error(), "extension") {
		t.Errorf("LoadSeeds = %v, want an error naming the extension", err)
	}
}

func TestLoadSeedsNoPathIsNoSeeds(t *testing.T) {
	got, err := LoadSeeds("", 100)
	if err != nil || got != nil {
		t.Errorf("LoadSeeds(\"\") = %+v, %v; want nil, nil", got, err)
	}
}

func TestLoadSeedsMissingFile(t *testing.T) {
	_, err := LoadSeeds(filepath.Join(t.TempDir(), "absent.csv"), 100)
	if err == nil {
		t.Error("a named but missing seed file must be an error")
	}
}

func TestValidateRejectsBadSeedRows(t *testing.T) {
	bad := 0.5
	cfg := Default()
	cfg.seeds = []SeedEntry{
		{Name: "Fine", Domain: "fine.com"},
		{Name: "", Domain: ""},
		{Name: "Bad Country", Country: "USA"},
		{Name: "Good Confidence", Confidence: &bad},
		{Name: "Negative", Confidence: func() *float64 { v := -0.5; return &v }()},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want errors for the malformed rows")
	}
	msg := err.Error()
	for _, want := range []string{"entry 2", "entry 3", "entry 5"} {
		if !strings.Contains(msg, want) {
			t.Errorf("joined error is missing %q:\n%v", want, msg)
		}
	}
	if strings.Contains(msg, "entry 1") || strings.Contains(msg, "entry 4") {
		t.Errorf("a valid seed row was reported as a problem:\n%v", msg)
	}
}

func TestWriteSeedsJSONRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	in := []SeedEntry{{Name: "Acme", Domain: "acme.com", Country: "US"}}
	if err := WriteSeedsJSON(path, in); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSeeds(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Acme" || got[0].Domain != "acme.com" {
		t.Errorf("round trip produced %+v", got)
	}
}

func TestLoadSeedFileThroughConfig(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	seed := filepath.Join(dir, "seeds.csv")
	if err := os.WriteFile(seed, []byte("name,domain\nAcme,acme.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "discovery.yaml")
	if err := os.WriteFile(conf, []byte("seed:\n  path: "+seed+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ConfigFileEnv, conf)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	seeds := cfg.Seeds()
	if len(seeds) != 1 || seeds[0].Name != "Acme" {
		t.Errorf("Seeds() = %+v, want the file to be read at load time", seeds)
	}
}

func asParseError(err error, target **ParseError) bool {
	pe, ok := err.(*ParseError)
	if ok {
		*target = pe
	}
	return ok
}
