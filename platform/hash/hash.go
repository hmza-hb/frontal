// Package hash centralises content addressing.
//
// Every cache, dedupe and novelty decision in the platform is keyed by one of
// these digests, so the normalisation rules live here rather than being
// re-invented (slightly differently) in every module.
package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Digest is a lower-case hex SHA-256 digest.
type Digest string

// Prefix marks the algorithm so a stored digest stays interpretable if the
// algorithm is ever upgraded.
const Prefix = "sha256:"

// Bytes returns the digest of b.
func Bytes(b []byte) Digest { return digest(b) }

// String returns the digest of s.
func String(s string) Digest { return digest([]byte(s)) }

// Combine returns a stable digest over the concatenation of parts. Each part is
// length-prefixed so ("ab","c") and ("a","bc") cannot collide.
func Combine(parts ...string) Digest {
	h := sha256.New()
	for _, p := range parts {
		var lenBuf [8]byte
		n := uint64(len(p))
		for i := 0; i < 8; i++ {
			lenBuf[i] = byte(n >> (8 * i))
		}
		h.Write(lenBuf[:])
		h.Write([]byte(p))
	}
	return Digest(Prefix + hex.EncodeToString(h.Sum(nil)))
}

// CombineSet is Combine over an unordered collection. Duplicate entries are
// preserved (two identical evidence IDs are not the same as one) but order is
// not significant.
func CombineSet(parts ...string) Digest {
	c := append([]string(nil), parts...)
	sort.Strings(c)
	return Combine(c...)
}

// Document is the digest used to dedupe fetched pages. Whitespace is collapsed
// and markup-insignificant differences are ignored, so a page that differs only
// by a rotating build timestamp still dedupes.
func Document(body []byte) Digest { return Bytes(NormalizeHTML(body)) }

// NormalizeHTML collapses runs of whitespace and trims the ends.
func NormalizeHTML(b []byte) []byte {
	var out strings.Builder
	out.Grow(len(b))
	space := false
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			if !space {
				out.WriteByte(' ')
				space = true
			}
		default:
			out.WriteByte(c)
			space = false
		}
	}
	return []byte(strings.TrimSpace(out.String()))
}

// Short returns the first 12 hex characters, for log lines and IDs.
func Short(d Digest) string {
	s := string(d)
	if len(s) <= len(Prefix) {
		return s
	}
	return s[len(Prefix) : len(Prefix)+12]
}

// Valid reports whether d is well-formed.
func (d Digest) Valid() bool {
	s := string(d)
	if !strings.HasPrefix(s, Prefix) {
		return false
	}
	_, err := hex.DecodeString(s[len(Prefix):])
	return err == nil
}

func digest(b []byte) Digest {
	sum := sha256.Sum256(b)
	return Digest(Prefix + hex.EncodeToString(sum[:]))
}
