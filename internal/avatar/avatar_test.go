package avatar

import (
	"bytes"
	"errors"
	"path"
	"strings"
	"testing"
)

// swapLocalArtwork points the bundled-artwork seam at fn for one test and
// restores the real one afterwards, so a test can reach the missing-artwork
// path without the embedded copy answering first.
func swapLocalArtwork(t *testing.T, fn ArtworkFunc) {
	t.Helper()
	original := LocalArtwork
	LocalArtwork = fn
	t.Cleanup(func() { LocalArtwork = original })
}

func TestCatalogParsesEveryManifestLine(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(dinoManifest), "\n")
	entries := Catalog()

	if len(entries) == 0 {
		t.Fatal("the embedded catalog parsed to zero entries")
	}
	if len(entries) != len(lines) {
		t.Errorf("catalog holds %d entries for %d manifest lines; a malformed line is being dropped silently",
			len(entries), len(lines))
	}

	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			t.Errorf("line %d has %d tab-separated columns, want 4: %q", i+1, len(fields), line)
			continue
		}
		for j, field := range fields {
			if strings.TrimSpace(field) == "" {
				t.Errorf("line %d column %d is empty: %q", i+1, j, line)
			}
			if field != strings.TrimSpace(field) {
				t.Errorf("line %d column %d carries surrounding whitespace: %q", i+1, j, field)
			}
		}
	}
}

func TestCatalogIdentifiersAndPathsAreUnique(t *testing.T) {
	seenID := make(map[string]int)
	seenPath := make(map[string]int)
	for i, entry := range Catalog() {
		if previous, ok := seenID[entry.ID]; ok {
			t.Errorf("id %q appears at entries %d and %d", entry.ID, previous, i)
		}
		seenID[entry.ID] = i

		if previous, ok := seenPath[entry.Path]; ok {
			t.Errorf("path %q appears at entries %d and %d", entry.Path, previous, i)
		}
		seenPath[entry.Path] = i
	}
}

// TestCatalogSpeciesAreUnique keeps the chooser from offering one dinosaur
// twice under two names (#536: "Torosaurus" and "Bob" were both
// Torosaurus latus).
// TestCatalogArtworkBaseNamesAreUnique keeps the flat bundle addressable per
// entry. The repository's tree is not flat, so the bundled copy is keyed by
// base name; two paths sharing one would silently serve a single file for
// both entries and every other coverage test would still pass.
func TestCatalogArtworkBaseNamesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, entry := range Catalog() {
		base := path.Base(entry.Path)
		if previous, ok := seen[base]; ok {
			t.Errorf("base name %q belongs to both %q and %q", base, previous, entry.ID)
		}
		seen[base] = entry.ID
	}
}

func TestCatalogSpeciesAreUnique(t *testing.T) {
	seen := make(map[string]string)
	for _, entry := range Catalog() {
		if previous, ok := seen[entry.Species]; ok {
			t.Errorf("species %q is listed by both %q and %q", entry.Species, previous, entry.ID)
		}
		seen[entry.Species] = entry.ID
	}
}

func TestCatalogPathsAreLiteralRepositoryPaths(t *testing.T) {
	for _, entry := range Catalog() {
		switch {
		case strings.HasPrefix(entry.Path, "/"):
			t.Errorf("%s: path %q is absolute, want repository-relative", entry.ID, entry.Path)
		case strings.HasPrefix(entry.Path, "./"):
			t.Errorf("%s: path %q carries a ./ prefix, want the literal repository path", entry.ID, entry.Path)
		case path.Clean(entry.Path) != entry.Path:
			t.Errorf("%s: path %q is not in clean form (%q)", entry.ID, entry.Path, path.Clean(entry.Path))
		case strings.Contains(entry.Path, "\\"):
			t.Errorf("%s: path %q uses a backslash", entry.ID, entry.Path)
		case !strings.HasSuffix(entry.Path, ".webp"):
			t.Errorf("%s: path %q is not a WebP illustration", entry.ID, entry.Path)
		}
	}
}

func TestLookupResolvesEveryCatalogIdentifier(t *testing.T) {
	for _, want := range Catalog() {
		got, ok := Lookup(want.ID)
		if !ok {
			t.Errorf("Lookup(%q) missed an id the catalog lists", want.ID)
			continue
		}
		if got != want {
			t.Errorf("Lookup(%q) = %+v, want %+v", want.ID, got, want)
		}
	}
}

func TestLookupMissesIdentifiersTheCatalogDoesNotList(t *testing.T) {
	for _, id := range []string{"", " ", "Bluefin", "bluefin ", "torosaurus-latus", "katharina"} {
		if got, ok := Lookup(id); ok {
			t.Errorf("Lookup(%q) resolved to %+v; ids are resolved literally", id, got)
		}
	}
}

func TestLookupPathResolvesTheLiteralColumn(t *testing.T) {
	for _, want := range Catalog() {
		got, ok := LookupPath(want.Path)
		if !ok {
			t.Errorf("LookupPath(%q) missed a path the catalog lists", want.Path)
			continue
		}
		if got != want {
			t.Errorf("LookupPath(%q) = %+v, want %+v", want.Path, got, want)
		}
	}
}

func TestLookupPathRejectsPathsNoColumnCarries(t *testing.T) {
	entries := Catalog()

	// A path derived from an identifier is the tempting shortcut this lookup
	// exists to refuse: dakotaraptor's artwork is dakota.webp, so the derived
	// form is a real file in the repository but not this entry's path.
	cases := []string{
		"",
		"public/characters/karl.webp ",
		" public/characters/karl.webp",
		"public/characters/KARL.webp",
		"./public/characters/karl.webp",
		"public/characters/dakotaraptor.webp",
		"public/characters/kentrosaurus.webp",
		"bluefin.webp",
		"characters/bluefin.webp",
	}
	for _, p := range cases {
		if got, ok := LookupPath(p); ok {
			t.Errorf("LookupPath(%q) resolved to %+v; resolution is literal and strict", p, got)
		}
	}

	// Guard the shortcut itself: the derived path must belong to no entry, or
	// the case above would pass for the wrong reason.
	derived := make(map[string]string, len(entries))
	for _, entry := range entries {
		derived["public/characters/"+entry.ID+".webp"] = entry.Path
	}
	for candidate, owner := range derived {
		if candidate == owner {
			continue
		}
		if _, ok := LookupPath(candidate); ok {
			t.Errorf("LookupPath(%q) resolved; expected it to be absent", candidate)
		}
	}
}

func TestFetchAvatarRejectsAnUnknownID(t *testing.T) {
	if _, err := FetchAvatar("stegosaurus"); !errors.Is(err, ErrAvatarNotFound) {
		t.Errorf("FetchAvatar(unknown) error = %v, want ErrAvatarNotFound", err)
	}
}

// TestFetchAvatarReportsAMissingBundledArtwork keeps a manifest row added
// without its illustration a named failure. Without the bundle the entry
// would otherwise render as an empty preview the chooser cannot explain.
func TestFetchAvatarReportsAMissingBundledArtwork(t *testing.T) {
	swapLocalArtwork(t, func(string) ([]byte, bool) { return nil, false })

	if _, err := FetchAvatar("karl"); !errors.Is(err, ErrArtworkNotFound) {
		t.Errorf("FetchAvatar without a bundled copy error = %v, want ErrArtworkNotFound", err)
	}
}

// TestEmbeddedArtworkCoversEveryCatalogEntry guards the bundled set against
// drift: a manifest row whose illustration was never committed is
// ErrArtworkNotFound the moment a pick lands, so this catches it before a
// user does.
func TestEmbeddedArtworkCoversEveryCatalogEntry(t *testing.T) {
	for _, entry := range Catalog() {
		data, ok := embeddedArtwork(entry.Path)
		if !ok {
			t.Errorf("no embedded artwork for %s (%s)", entry.ID, entry.Path)
			continue
		}
		if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
			t.Errorf("%s: embedded artwork is not a WebP container (%d bytes)", entry.ID, len(data))
		}
	}
}

// TestFetchAvatarReturnsTheBundledArtwork pins where every catalog entry is
// read from: the copy in the binary, not a service the chooser has to reach.
func TestFetchAvatarReturnsTheBundledArtwork(t *testing.T) {
	for _, entry := range Catalog() {
		got, err := FetchAvatar(entry.ID)
		if err != nil {
			t.Fatalf("FetchAvatar(%q): %v", entry.ID, err)
		}
		want, ok := embeddedArtwork(entry.Path)
		if !ok {
			t.Fatalf("no bundled artwork for %s", entry.ID)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("FetchAvatar(%q) did not return the bundled artwork", entry.ID)
		}
	}
}
