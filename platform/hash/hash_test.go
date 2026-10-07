package hash

import "testing"

func TestDocumentIgnoresWhitespaceOnlyDifferences(t *testing.T) {
	a := Document([]byte("<p>hello world</p>"))
	b := Document([]byte("<p>hello \n\t   world</p>"))
	if a != b {
		t.Fatalf("whitespace-only difference changed digest:\n a=%s\n b=%s", a, b)
	}
}

func TestDocumentDistinguishesContent(t *testing.T) {
	if Document([]byte("acme")) == Document([]byte("acme inc")) {
		t.Fatal("different content produced the same digest")
	}
}

func TestCombineIsLengthPrefixed(t *testing.T) {
	// Without length prefixing these two would hash identically.
	if Combine("ab", "c") == Combine("a", "bc") {
		t.Fatal("Combine is ambiguous across part boundaries")
	}
}

func TestCombineSetIsOrderIndependent(t *testing.T) {
	a := CombineSet("e1", "e2", "e3")
	b := CombineSet("e3", "e1", "e2")
	if a != b {
		t.Fatalf("CombineSet depends on order: %s != %s", a, b)
	}
	if a == CombineSet("e1", "e2") {
		t.Fatal("CombineSet lost an element")
	}
}

func TestDigestValid(t *testing.T) {
	if !Bytes([]byte("x")).Valid() {
		t.Error("Bytes produced an invalid digest")
	}
	if Digest("md5:abc").Valid() {
		t.Error("digest with the wrong prefix was accepted")
	}
	if Digest(Prefix + "nothex").Valid() {
		t.Error("digest with non-hex body was accepted")
	}
}

func TestShort(t *testing.T) {
	if got := Short(Bytes([]byte("x"))); len(got) != 12 {
		t.Fatalf("Short returned %q with length %d, want 12", got, len(got))
	}
}
