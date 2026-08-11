package pushcrypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"testing"
)

func TestSealAndOpenEnvelope(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	plaintext := []byte(`{"title":"Prepare review","occurrence_id":"01989f"}`)
	aad := []byte("route-01989f")
	envelope, err := Seal(privateKey.PublicKey().Bytes(), plaintext, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded Envelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	opened, err := Open(privateKey.Bytes(), decoded, aad)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened plaintext = %q, want %q", opened, plaintext)
	}
}

func TestOpenRejectsTamperingAndWrongContext(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	envelope, err := Seal(privateKey.PublicKey().Bytes(), []byte("secret"), []byte("route-a"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := Open(privateKey.Bytes(), envelope, []byte("route-b")); err == nil {
		t.Fatal("Open() accepted wrong additional authenticated data")
	}
	envelope.Ciphertext[len(envelope.Ciphertext)-1] ^= 0x01
	if _, err := Open(privateKey.Bytes(), envelope, []byte("route-a")); err == nil {
		t.Fatal("Open() accepted tampered ciphertext")
	}
}

func TestSealRejectsInvalidPublicKey(t *testing.T) {
	t.Parallel()

	if _, err := Seal([]byte("short"), []byte("secret"), nil); err == nil {
		t.Fatal("Seal() accepted invalid public key")
	}
}
