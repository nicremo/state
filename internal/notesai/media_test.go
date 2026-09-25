package notesai

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("x"), 100)...)

func TestMediaStoreKeepsContentPrivateAndAddressedByHash(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "media")
	store, err := NewMediaStore(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Put(bytes.NewReader(jpegBytes), 1<<20, "image/jpeg")
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	second, err := store.Put(bytes.NewReader(jpegBytes), 1<<20, "image/jpeg")
	if err != nil || second.SHA256 != first.SHA256 || first.Size != int64(len(jpegBytes)) {
		t.Fatalf("second put = %+v, %v", second, err)
	}
	path := filepath.Join(root, first.SHA256[:2], first.SHA256)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", info, err)
	}
	directory, _ := os.Stat(root)
	if directory.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v", directory.Mode().Perm())
	}
	reader, size, err := store.Open(first.SHA256)
	if err != nil || size != int64(len(jpegBytes)) {
		t.Fatalf("Open() = %d, %v", size, err)
	}
	read, _ := io.ReadAll(reader)
	_ = reader.Close()
	if !bytes.Equal(read, jpegBytes) {
		t.Fatal("content changed")
	}
	entries, _ := os.ReadDir(filepath.Join(root, first.SHA256[:2]))
	if len(entries) != 1 {
		t.Fatalf("stored %d files, want 1 (temporary files must not stay)", len(entries))
	}
}

func TestMediaStoreRejectsWhatItCannotTrust(t *testing.T) {
	t.Parallel()
	store, _ := NewMediaStore(filepath.Join(t.TempDir(), "media"))
	cases := []struct {
		name    string
		content []byte
		limit   int64
		mime    string
	}{
		{"too large", jpegBytes, 10, "image/jpeg"},
		{"wrong signature", []byte("<html>not an image</html>"), 1 << 20, "image/jpeg"},
		{"unknown type", jpegBytes, 1 << 20, "application/pdf"},
		{"empty", nil, 1 << 20, "image/jpeg"},
	}
	for _, testCase := range cases {
		if _, err := store.Put(bytes.NewReader(testCase.content), testCase.limit, testCase.mime); !errors.Is(err, ErrMediaRejected) {
			t.Errorf("%s: error = %v, want ErrMediaRejected", testCase.name, err)
		}
	}
	if _, _, err := store.Open("../" + strings.Repeat("a", 61)); !errors.Is(err, ErrMediaRejected) {
		t.Fatalf("path escape error = %v", err)
	}
}

func TestMediaSignatures(t *testing.T) {
	t.Parallel()
	valid := map[string][]byte{
		"image/png":  []byte("\x89PNG\r\n\x1a\n...."),
		"image/heic": []byte("\x00\x00\x00\x18ftypheic...."),
		"audio/mp4":  []byte("\x00\x00\x00\x1cftypM4A ...."),
		"audio/m4a":  []byte("\x00\x00\x00\x1cftypM4A ...."),
		"audio/mpeg": []byte("ID3\x04......"),
		"audio/wav":  []byte("RIFF....WAVEfmt "),
		"audio/aac":  {0xFF, 0xF1, 0x50, 0x80, 0, 0, 0, 0},
	}
	for mime, content := range valid {
		if !matchesSignature(mime, content) {
			t.Errorf("%s rejected", mime)
		}
	}
	if matchesSignature("image/png", jpegBytes) {
		t.Error("JPEG accepted as PNG")
	}
}
