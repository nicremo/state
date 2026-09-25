// Package notesai is the server side of State's AI-managed notes: the
// private media store, the OpenRouter gateway, the notes agent and the
// worker that processes notes.
package notesai

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// ErrMediaRejected covers everything the store refuses to keep: too large,
// an unknown type, or bytes that do not look like the type they claim.
var ErrMediaRejected = errors.New("media rejected")

// MediaStore keeps note photos and recordings on the server's own disk,
// content addressed and readable only by the server user. Nothing here is
// ever served publicly or handed to OpenRouter as a URL.
type MediaStore struct {
	root string
}

type StoredMedia struct {
	SHA256 string
	Size   int64
	Mime   string
}

var allowedMedia = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/heic": true,
	"audio/mp4":  true,
	"audio/m4a":  true,
	"audio/aac":  true,
	"audio/mpeg": true,
	"audio/wav":  true,
}

// AllowedMedia reports whether the store accepts this MIME type.
func AllowedMedia(mime string) bool {
	return allowedMedia[mime]
}

func NewMediaStore(root string) (*MediaStore, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create media directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("protect media directory: %w", err)
	}
	return &MediaStore{root: root}, nil
}

// Put stores the content if it is an allowed type, within maxBytes and
// starts with that type's signature. The same content is stored once.
func (store *MediaStore) Put(reader io.Reader, maxBytes int64, mime string) (StoredMedia, error) {
	staged, err := store.Stage(reader, maxBytes, mime)
	if err != nil {
		return StoredMedia{}, err
	}
	if err := staged.Commit(); err != nil {
		return StoredMedia{}, err
	}
	return staged.StoredMedia, nil
}

// StagedMedia is an upload that is checked but not kept yet. Commit keeps
// it once the note accepted the attachment; Discard throws it away, so a
// refused upload never occupies the disk.
type StagedMedia struct {
	StoredMedia
	store *MediaStore
	path  string
}

// Stage receives and checks an upload into a temporary file.
func (store *MediaStore) Stage(reader io.Reader, maxBytes int64, mime string) (*StagedMedia, error) {
	if !allowedMedia[mime] || maxBytes <= 0 {
		return nil, ErrMediaRejected
	}
	temporary, err := os.CreateTemp(store.root, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("create upload file: %w", err)
	}
	keep := false
	defer func() {
		temporary.Close()
		if !keep {
			os.Remove(temporary.Name())
		}
	}()

	hash := sha256.New()
	head := &headBuffer{limit: 16}
	written, err := io.Copy(io.MultiWriter(temporary, hash, head), io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("store upload: %w", err)
	}
	if written == 0 || written > maxBytes || !matchesSignature(mime, head.Bytes()) {
		return nil, ErrMediaRejected
	}
	if err := temporary.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("protect upload: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return nil, fmt.Errorf("flush upload: %w", err)
	}
	keep = true
	return &StagedMedia{
		StoredMedia: StoredMedia{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: written, Mime: mime},
		store:       store,
		path:        temporary.Name(),
	}, nil
}

// Commit moves the upload to its content address. Content that is already
// stored stays as it is.
func (staged *StagedMedia) Commit() error {
	directory := filepath.Join(staged.store.root, staged.SHA256[:2])
	if err := os.MkdirAll(directory, 0o700); err != nil {
		staged.Discard()
		return fmt.Errorf("create media directory: %w", err)
	}
	if err := os.Rename(staged.path, filepath.Join(directory, staged.SHA256)); err != nil {
		staged.Discard()
		return fmt.Errorf("keep upload: %w", err)
	}
	return nil
}

// Discard removes an upload that was refused.
func (staged *StagedMedia) Discard() {
	_ = os.Remove(staged.path)
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Open returns the stored content for a hash. Anything but a plain hash is
// refused, so a stored reference can never point outside the store.
func (store *MediaStore) Open(digest string) (io.ReadCloser, int64, error) {
	if !digestPattern.MatchString(digest) {
		return nil, 0, ErrMediaRejected
	}
	file, err := os.Open(filepath.Join(store.root, digest[:2], digest))
	if err != nil {
		return nil, 0, fmt.Errorf("open media: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("stat media: %w", err)
	}
	return file, info.Size(), nil
}

// ReadAll returns stored content, for sending one attachment to the model.
func (store *MediaStore) ReadAll(digest string) ([]byte, error) {
	reader, _, err := store.Open(digest)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

type headBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *headBuffer) Write(content []byte) (int, error) {
	if room := buffer.limit - buffer.Len(); room > 0 {
		if len(content) < room {
			room = len(content)
		}
		buffer.Buffer.Write(content[:room])
	}
	return len(content), nil
}

// matchesSignature checks the first bytes against the claimed type, so a
// renamed HTML page or script never enters the store as a photo.
func matchesSignature(mime string, head []byte) bool {
	switch mime {
	case "image/jpeg":
		return bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF})
	case "image/png":
		return bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n"))
	case "image/heic", "audio/mp4", "audio/m4a":
		return len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp"))
	case "audio/mpeg":
		return bytes.HasPrefix(head, []byte("ID3")) || (len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0)
	case "audio/wav":
		return len(head) >= 12 && bytes.HasPrefix(head, []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WAVE"))
	case "audio/aac":
		return len(head) >= 2 && head[0] == 0xFF && head[1]&0xF0 == 0xF0
	default:
		return false
	}
}
