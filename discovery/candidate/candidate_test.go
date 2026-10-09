package candidate

import (
	"testing"
	"time"
)

func at(min int) time.Time {
	return time.Date(2026, 1, 1, 0, min, 0, 0, time.UTC)
}

func TestValidateRequiresAnIdentity(t *testing.T) {
	tests := []struct {
		name   string
		c      Candidate
		reason string
	}{
		{"nothing at all", Candidate{}, ReasonNotCompany},
		{"whitespace name", Candidate{Name: "   "}, ReasonNotCompany},
		{
			"no evidence",
			Candidate{Name: "Acme"},
			ReasonMalformed,
		},
		{
			"evidence without a method",
			Candidate{Name: "Acme", Evidence: []Evidence{{Source: SourceSeed}}},
			ReasonMalformed,
		},
		{
			"evidence without a source",
			Candidate{Name: "Acme", Evidence: []Evidence{{Method: MethodSeedImport}}},
			ReasonMalformed,
		},
		{
			"confidence above one",
			Candidate{
				Name: "Acme", Confidence: 1.4,
				Evidence: []Evidence{{Source: SourceSeed, Method: MethodSeedImport}},
			},
			ReasonMalformed,
		},
		{
			"negative confidence",
			Candidate{
				Name: "Acme", Confidence: -0.1,
				Evidence: []Evidence{{Source: SourceSeed, Method: MethodSeedImport}},
			},
			ReasonMalformed,
		},
		{
			"valid with a name only",
			Candidate{
				Name:     "Acme",
				Evidence: []Evidence{{Source: SourceSeed, Method: MethodSeedImport}},
			},
			"",
		},
		{
			"valid with a domain only",
			Candidate{
				Domain:   "acme.com",
				Evidence: []Evidence{{Source: SourceCertificate, Method: MethodCertificate}},
			},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.c.Validate()
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want reason %q", tt.reason)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("Validate() = %T, want *ValidationError", err)
			}
			if ve.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", ve.Reason, tt.reason)
			}
		})
	}
}

func TestAddEvidenceTracksWindowAndSources(t *testing.T) {
	c := Candidate{Name: "Acme"}
	c.AddEvidence(Evidence{Source: SourceSeed, Method: MethodSeedImport, ObservedAt: at(30)})
	c.AddEvidence(Evidence{Source: SourceDirectory, Method: MethodListPage, ObservedAt: at(10)})
	c.AddEvidence(Evidence{Source: SourceDirectory, Method: MethodAPIRecord, ObservedAt: at(50)})

	if c.SourceCount != 2 {
		t.Errorf("SourceCount = %d, want 2; the same source twice is not corroboration", c.SourceCount)
	}
	if !c.FirstSeen.Equal(at(10)) {
		t.Errorf("FirstSeen = %v, want the earliest observation", c.FirstSeen)
	}
	if !c.LastSeen.Equal(at(50)) {
		t.Errorf("LastSeen = %v, want the latest observation", c.LastSeen)
	}
}

func TestAddEvidenceStampsMissingTime(t *testing.T) {
	// A provider that does not set ObservedAt must not produce a zero-time
	// window, which would make the candidate look older than any other.
	before := time.Now().UTC()
	c := Candidate{Name: "Acme"}
	c.AddEvidence(Evidence{Source: SourceSeed, Method: MethodSeedImport})
	if c.FirstSeen.IsZero() || c.LastSeen.IsZero() {
		t.Fatal("observation window was left zero")
	}
	if c.FirstSeen.Before(before.Add(-time.Minute)) {
		t.Errorf("FirstSeen = %v is implausibly early", c.FirstSeen)
	}
}

func TestMergePrefersTheBetterValuePerField(t *testing.T) {
	// The seed names the company and knows nothing else; a directory listing
	// knows the domain and industry but garbles the name. A last-write-wins
	// merge would keep the garbled name and lose nothing useful, which is the
	// best case; a first-write-wins merge would lose the domain entirely.
	seed := Candidate{
		Name:       "Acme Industrial Solutions Incorporated",
		Country:    "US",
		Confidence: 0.95,
		Evidence:   []Evidence{{Source: SourceSeed, Method: MethodSeedImport, URL: "seed://acme"}},
	}
	directory := Candidate{
		Name:     "Acme",
		Domain:   "acme.com",
		Industry: "Industrial Automation",
		Keywords: []string{"Acme Industrial"},
		URL:      "https://acme.com",
		Evidence: []Evidence{{Source: SourceDirectory, Method: MethodListPage, URL: "https://dir.example/1"}},
		LastSeen: at(60),
	}

	merged := seed
	merged.Merge(directory)

	if merged.Domain != "acme.com" {
		t.Errorf("Domain = %q, want the value only the directory knew", merged.Domain)
	}
	if merged.Name != "Acme Industrial Solutions Incorporated" {
		t.Errorf("Name = %q, want the more specific name kept", merged.Name)
	}
	if merged.Country != "US" || merged.Industry != "Industrial Automation" {
		t.Errorf("Country=%q Industry=%q, want both merged", merged.Country, merged.Industry)
	}
	if merged.Confidence != 0.95 {
		t.Errorf("Confidence = %v, want the higher value kept", merged.Confidence)
	}
	if len(merged.Evidence) != 2 {
		t.Errorf("Evidence has %d entries, want 2", len(merged.Evidence))
	}
	if merged.SourceCount != 2 {
		t.Errorf("SourceCount = %d, want 2", merged.SourceCount)
	}
}

func TestMergeIsIdempotentOnEvidence(t *testing.T) {
	// Replaying the same discovery run must not double the evidence, or a
	// crash-and-retry loop would manufacture corroboration out of nothing.
	a := Candidate{Name: "Acme", Evidence: []Evidence{
		{Source: SourceSeed, Method: MethodSeedImport, URL: "seed://acme", Query: "q1"},
	}}
	b := Candidate{Name: "Acme", Evidence: []Evidence{
		{Source: SourceSeed, Method: MethodSeedImport, URL: "seed://acme", Query: "q1"},
	}}
	c := a
	c.Merge(b)
	if len(c.Evidence) != 1 {
		t.Fatalf("Evidence has %d entries, want 1 after replaying the same observation", len(c.Evidence))
	}
	if c.SourceCount != 1 {
		t.Errorf("SourceCount = %d, want 1", c.SourceCount)
	}
}

func TestMergeTakesTheLaterSeenWindow(t *testing.T) {
	older := Candidate{Name: "Acme", FirstSeen: at(1), LastSeen: at(5),
		Evidence: []Evidence{{Source: SourceSeed, Method: MethodSeedImport, ObservedAt: at(5)}}}
	newer := Candidate{Name: "Acme", FirstSeen: at(3), LastSeen: at(90),
		Evidence: []Evidence{{Source: SourceDirectory, Method: MethodListPage, ObservedAt: at(90)}}}

	older.Merge(newer)
	if !older.FirstSeen.Equal(at(1)) {
		t.Errorf("FirstSeen = %v, want the earlier of the two windows", older.FirstSeen)
	}
	if !older.LastSeen.Equal(at(90)) {
		t.Errorf("LastSeen = %v, want the later of the two windows", older.LastSeen)
	}
}

func TestMergeKeepsAnIDOnlyWhenUnset(t *testing.T) {
	// Providers must not assign IDs, so an incoming ID is a bug in the provider.
	// Silently adopting it would let a provider re-point a record at another
	// company; overwriting a real ID would lose the record's identity.
	existing := Candidate{ID: "cand_1", Name: "Acme"}
	existing.Merge(Candidate{ID: "cand_2", Name: "Acme"})
	if existing.ID != "cand_1" {
		t.Errorf("ID = %q, want the existing ID kept", existing.ID)
	}
	empty := Candidate{Name: "Acme"}
	empty.Merge(Candidate{ID: "cand_2"})
	if empty.ID != "cand_2" {
		t.Errorf("ID = %q, want the incoming ID adopted when there was none", empty.ID)
	}
}

func TestHasKeywordIgnoresCaseAndSpacing(t *testing.T) {
	c := Candidate{Keywords: []string{"Acme Industrial"}}
	for _, k := range []string{"acme industrial", "ACME   INDUSTRIAL", " Acme Industrial "} {
		if !c.HasKeyword(k) {
			t.Errorf("HasKeyword(%q) = false, want true", k)
		}
	}
	if c.HasKeyword("acme") {
		t.Error("HasKeyword(acme) = true, want false")
	}
	if c.HasKeyword("   ") {
		t.Error("a blank keyword must not match")
	}
}

func TestSourceCountsDistinctSurfacesNotEntries(t *testing.T) {
	// Five evidence rows from one source is one source. Ranking treats these
	// very differently, and conflating them would let a chatty provider look
	// corroborated.
	c := Candidate{Name: "Acme"}
	for i := 0; i < 5; i++ {
		c.AddEvidence(Evidence{Source: SourceDirectory, Method: MethodListPage, URL: "https://d/" + string(rune('a'+i))})
	}
	if c.SourceCount != 1 {
		t.Errorf("SourceCount = %d, want 1", c.SourceCount)
	}
	if len(c.Evidence) != 5 {
		t.Errorf("Evidence has %d entries, want all 5 retained", len(c.Evidence))
	}
}
