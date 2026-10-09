package config

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadSeeds reads an operator-supplied company list.
//
// Three formats are accepted, chosen by extension: .csv, .json, and .txt for the
// common one-column case. The distinction that matters is that a seed is the
// operator's own statement about who to look for, so a malformed line is an
// error rather than a skipped row: silently dropping half a seed list produces
// a run that looks successful and quietly misses companies.
//
// The exception is a fully blank line, which is normal in a hand-edited file.
func LoadSeeds(path string, max int) ([]SeedEntry, error) {
	if path == "" {
		return nil, nil
	}
	if max <= 0 {
		return nil, fmt.Errorf("config: seed max entries must be positive, got %d", max)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open seed file %s: %w", path, err)
	}
	defer f.Close()

	var (
		entries []SeedEntry
		perr    *ParseError
	)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		entries, perr = parseSeedCSV(f, max)
	case ".json":
		entries, perr = parseSeedJSON(f, max)
	case ".txt", "":
		entries, perr = parseSeedText(f, max)
	default:
		// An unknown extension is a guess about the format, and guessing wrong
		// means reading a CSV as one column per line and producing garbage
		// company names.
		return nil, fmt.Errorf("config: seed file %s has unsupported extension %q; use .csv, .json or .txt",
			path, filepath.Ext(path))
	}
	if perr != nil {
		perr.Path = path
		return nil, perr
	}
	return entries, nil
}

// ParseError reports a bad row with its line number, so an operator can open
// the file at exactly the right place instead of hunting.
type ParseError struct {
	Path string
	Line int
	Err  error
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("config: %s:%d: %v", e.Path, e.Line, e.Err)
	}
	return fmt.Sprintf("config: %s: %v", e.Path, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

var errTooManySeeds = errors.New("seed file has more entries than the configured maximum")

// parseSeedText reads one company per line. A line may be "Name" or
// "Name<TAB>domain" or "Name,domain", because all three are what a person
// actually types into a text file.
func parseSeedText(r io.Reader, max int) ([]SeedEntry, *ParseError) {
	sc := bufio.NewScanner(r)
	// A line longer than the default 64KiB is not a company name. Raising the
	// cap is still bounded, so a binary file cannot exhaust memory.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var out []SeedEntry
	line := 1
	for ; sc.Scan(); line++ {
		raw := sc.Text()
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		if len(out) >= max {
			return nil, &ParseError{Line: line, Err: errTooManySeeds}
		}
		e, err := parseSeedLine(raw)
		if err != nil {
			return nil, &ParseError{Line: line, Err: err}
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		// line is the first line the scanner could not deliver, so the error
		// points at the mistake. An error with no location is useless to
		// someone holding a text file and trying to find it.
		return nil, &ParseError{Line: line, Err: fmt.Errorf("read: %w", err)}
	}
	return out, nil
}

func parseSeedLine(raw string) (SeedEntry, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\t' || r == ','
	})
	switch len(fields) {
	case 0:
		return SeedEntry{}, errors.New("empty entry")
	case 1:
		return SeedEntry{Name: strings.TrimSpace(fields[0])}, nil
	case 2:
		// Two fields are ambiguous: "Acme, Inc." is one name with a comma in
		// it, and "Acme,acme.com" is a name and a domain. Treat the second
		// field as a domain only when it actually looks like one, which keeps
		// "Acme, Inc." intact.
		if domain, ok := domainCandidate(fields[1]); ok {
			return SeedEntry{Name: strings.TrimSpace(fields[0]), Domain: domain}, nil
		}
		return SeedEntry{Name: strings.TrimSpace(raw)}, nil
	default:
		return SeedEntry{}, fmt.Errorf("too many fields; quote the name if it contains a comma")
	}
}

// domainCandidate interprets a field as a domain, returning the cleaned form.
// The cleaning matters as much as the decision: an operator pasting
// "https://acme.com/about" meant acme.com, and storing the URL verbatim would
// hand the rest of the pipeline something it must parse again.
func domainCandidate(raw string) (string, bool) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return "", false
	}
	// A full URL is a common paste. Take the host from it rather than rejecting
	// the row, because the operator clearly meant the domain. The path, query
	// and fragment all have to go: leaving "acme.com/about" in place would fail
	// the label check below and turn the row into a single-field name.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:] // drop any userinfo
	}
	s = strings.TrimSuffix(s, ".")
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") {
		return "", false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c >= 0x80
			if !ok {
				return "", false
			}
		}
	}
	return s, true
}

// parseSeedCSV reads a header-driven CSV. The header decides which column is
// which, because column order in a hand-made file is never guaranteed and
// guessing means reading "acme.com,Acme" as a company named "acme.com".
func parseSeedCSV(r io.Reader, max int) ([]SeedEntry, *ParseError) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // reported per row so the error can name a line
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = false

	header, err := cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil // an empty file is an empty seed list
		}
		return nil, &ParseError{Line: 1, Err: fmt.Errorf("read header: %w", err)}
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	required := []string{"name", "domain"}
	haveOne := false
	for _, req := range required {
		if _, ok := idx[req]; ok {
			haveOne = true
		}
	}
	if !haveOne {
		return nil, &ParseError{
			Line: 1,
			Err:  fmt.Errorf("header must contain a name or domain column, got %v", header),
		}
	}

	var out []SeedEntry
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, &ParseError{Line: line, Err: fmt.Errorf("malformed row: %w", err)}
		}
		if isBlankRecord(rec) {
			continue
		}
		if len(out) >= max {
			return nil, &ParseError{Line: line, Err: errTooManySeeds}
		}
		e := SeedEntry{
			Name:         cell(rec, idx, "name"),
			Domain:       strings.ToLower(cell(rec, idx, "domain")),
			Country:      strings.ToUpper(cell(rec, idx, "country")),
			Industry:     cell(rec, idx, "industry"),
			EmployeeHint: cell(rec, idx, "employee_hint", "employees", "size"),
			Notes:        cell(rec, idx, "notes", "note", "description"),
		}
		if raw := cell(rec, idx, "confidence"); raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, &ParseError{Line: line, Err: fmt.Errorf("confidence %q: %w", raw, err)}
			}
			e.Confidence = &v
		}
		out = append(out, e)
	}
	return out, nil
}

func cell(rec []string, idx map[string]int, names ...string) string {
	for _, n := range names {
		if i, ok := idx[n]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
	}
	return ""
}

func isBlankRecord(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// parseSeedJSON accepts either a bare array or an object with a "companies"
// key, because both shapes are what people export.
func parseSeedJSON(r io.Reader, max int) ([]SeedEntry, *ParseError) {
	// A seed row is small. Capping the read at a generous multiple of the entry
	// limit means a file that is not really a seed list cannot exhaust memory
	// before the entry check runs.
	limit := int64(max)*4096 + 1<<20
	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, &ParseError{Err: fmt.Errorf("read: %w", err)}
	}
	if int64(len(data)) >= limit {
		return nil, &ParseError{Err: errTooManySeeds}
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}

	var list []SeedEntry
	if trimmed[0] == '[' {
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, &ParseError{Err: fmt.Errorf("parse json array: %w", err)}
		}
	} else {
		var wrapper struct {
			Companies []SeedEntry `json:"companies"`
			Entries   []SeedEntry `json:"entries"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return nil, &ParseError{Err: fmt.Errorf("parse json object: %w", err)}
		}
		switch {
		case wrapper.Companies != nil:
			list = wrapper.Companies
		case wrapper.Entries != nil:
			list = wrapper.Entries
		default:
			return nil, &ParseError{Err: errors.New(`json must be an array or an object with a "companies" or "entries" key`)}
		}
	}
	if len(list) > max {
		return nil, &ParseError{Err: errTooManySeeds}
	}
	for i := range list {
		list[i].Domain = strings.ToLower(strings.TrimSpace(list[i].Domain))
		list[i].Country = strings.ToUpper(strings.TrimSpace(list[i].Country))
	}
	return list, nil
}

// WriteSeedsJSON writes a seed list. It exists so the CLI can produce the
// format it documents, which is the only reliable way to keep a documented
// example honest.
func WriteSeedsJSON(path string, entries []SeedEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode seeds: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("config: create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
