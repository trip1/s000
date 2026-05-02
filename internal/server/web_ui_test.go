package server

import "testing"

func TestNormalizeFolderMarkerKeyPreservesLiteralEscapes(t *testing.T) {
	t.Parallel()

	key := normalizeFolderMarkerKey("photos%2F", "2026%2F")
	if key != "photos%2F/2026%2F/" {
		t.Fatalf("expected literal escapes to be preserved, got %q", key)
	}
}

func TestNormalizeFolderMarkerKeyLeavesInvalidEscapes(t *testing.T) {
	t.Parallel()

	key := normalizeFolderMarkerKey("photos/", "bad%zz")
	if key != "photos/bad%zz/" {
		t.Fatalf("expected invalid escape to be preserved, got %q", key)
	}
}

func TestKeyPathEscapePreservesFolderSeparators(t *testing.T) {
	t.Parallel()

	got := keyPathEscape("photos/2026/a b%2Fc.txt")
	if got != "photos/2026/a%20b%252Fc.txt" {
		t.Fatalf("escaped key path = %q", got)
	}
}

func TestResolveUploadObjectKeyAppliesPrefixToExplicitSingleFileKey(t *testing.T) {
	t.Parallel()

	got := resolveUploadObjectKey("sites/docs/", "index.html", "index.html", "", true, 1)
	if got != "sites/docs/index.html" {
		t.Fatalf("expected upload key under current folder, got %q", got)
	}
}

func TestResolveUploadObjectKeyDoesNotDoublePrefixExplicitSingleFileKey(t *testing.T) {
	t.Parallel()

	got := resolveUploadObjectKey("sites/docs/", "sites/docs/index.html", "index.html", "", true, 1)
	if got != "sites/docs/index.html" {
		t.Fatalf("expected already-prefixed upload key to be preserved, got %q", got)
	}
}

func TestResolveUploadObjectKeyPreservesFolderUploadStructureUnderPrefix(t *testing.T) {
	t.Parallel()

	got := resolveUploadObjectKey("sites/", "", "app.css", "assets/css/app.css", false, 2)
	if got != "sites/assets/css/app.css" {
		t.Fatalf("expected folder upload structure under prefix, got %q", got)
	}
}
