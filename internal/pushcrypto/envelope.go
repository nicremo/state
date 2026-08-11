package pushcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const envelopeVersion = 1

var ErrInvalidEnvelope = errors.New("invalid push envelope")

type Envelope struct {
	Version            int    `json:"version"`
	EphemeralPublicKey []byte `json:"ephemeral_public_key"`
	Nonce              []byte `json:"nonce"`
	Ciphertext         []byte `json:"ciphertext"`
}

func Seal(recipientPublicKey []byte, plaintext []byte, additionalData []byte) (Envelope, error) {
	curve := ecdh.X25519()
	recipient, err := curve.NewPublicKey(recipientPublicKey)
	if err != nil {
		return Envelope{}, fmt.Errorf("parse recipient public key: %w", ErrInvalidEnvelope)
	}
	ephemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return Envelope{}, fmt.Errorf("generate ephemeral key: %w", err)
	}
	sharedSecret, err := ephemeral.ECDH(recipient)
	if err != nil {
		return Envelope{}, fmt.Errorf("derive shared secret: %w", err)
	}
	key, err := deriveKey(sharedSecret, ephemeral.PublicKey().Bytes(), recipientPublicKey)
	if err != nil {
		return Envelope{}, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate nonce: %w", err)
	}
	return Envelope{
		Version:            envelopeVersion,
		EphemeralPublicKey: ephemeral.PublicKey().Bytes(),
		Nonce:              nonce,
		Ciphertext:         aead.Seal(nil, nonce, plaintext, authenticatedData(additionalData)),
	}, nil
}

func Open(recipientPrivateKey []byte, envelope Envelope, additionalData []byte) ([]byte, error) {
	if envelope.Version != envelopeVersion {
		return nil, ErrInvalidEnvelope
	}
	curve := ecdh.X25519()
	privateKey, err := curve.NewPrivateKey(recipientPrivateKey)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	ephemeral, err := curve.NewPublicKey(envelope.EphemeralPublicKey)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	sharedSecret, err := privateKey.ECDH(ephemeral)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	key, err := deriveKey(sharedSecret, envelope.EphemeralPublicKey, privateKey.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(envelope.Nonce) != aead.NonceSize() || len(envelope.Ciphertext) < aead.Overhead() {
		return nil, ErrInvalidEnvelope
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, authenticatedData(additionalData))
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	return plaintext, nil
}

func deriveKey(sharedSecret []byte, ephemeralPublicKey []byte, recipientPublicKey []byte) ([]byte, error) {
	saltInput := make([]byte, 0, len(ephemeralPublicKey)+len(recipientPublicKey))
	saltInput = append(saltInput, ephemeralPublicKey...)
	saltInput = append(saltInput, recipientPublicKey...)
	salt := sha256.Sum256(saltInput)
	key, err := hkdf.Key(sha256.New, sharedSecret, salt[:], "com.fabincrm.state.push.v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive encryption key: %w", err)
	}
	return key, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return aead, nil
}

func authenticatedData(additionalData []byte) []byte {
	result := make([]byte, 0, len(additionalData)+14)
	result = append(result, []byte("state-push-v1\x00")...)
	return append(result, additionalData...)
}
