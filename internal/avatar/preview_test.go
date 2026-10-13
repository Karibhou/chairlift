package avatar

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"testing"
)

// TestEmbeddedPreviewCoversEveryCatalogEntry guards the embedded preview set
// against drift: every catalog row must have a PreviewSize PNG under the
// preview ceiling, so a stale asset left behind by an earlier size is caught.
func TestEmbeddedPreviewCoversEveryCatalogEntry(t *testing.T) {
	for _, entry := range Catalog() {
		data, err := PreviewPNG(entry.ID)
		if err != nil {
			t.Errorf("PreviewPNG(%q): %v", entry.ID, err)
			continue
		}
		if len(data) > MaxPreviewPNGBytes {
			t.Errorf("%s: preview PNG is %d bytes, over the %d-byte ceiling", entry.ID, len(data), MaxPreviewPNGBytes)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Errorf("%s: preview is not a decodable PNG: %v", entry.ID, err)
			continue
		}
		if want := image.Rect(0, 0, PreviewSize, PreviewSize); img.Bounds() != want {
			t.Errorf("%s: preview bounds = %v, want %v", entry.ID, img.Bounds(), want)
		}
	}
}

// TestPreviewPNGRejectsAnUnknownID keeps the preview lookup as literal as the
// catalog's own.
func TestPreviewPNGRejectsAnUnknownID(t *testing.T) {
	if _, err := PreviewPNG("stegosaurus"); !errors.Is(err, ErrAvatarNotFound) {
		t.Errorf("PreviewPNG(unknown) error = %v, want ErrAvatarNotFound", err)
	}
}
