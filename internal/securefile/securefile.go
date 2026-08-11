package securefile

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadOrCreateAuditSigningKey(path string) (ed25519.PrivateKey, error) {
	encoded, err := loadOrCreate(path, func() (string, error) {
		_, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(privateKey.Seed()), nil
	})
	if err != nil {
		return nil, err
	}
	seed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode audit signing seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid audit signing seed length")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func LoadOrCreateBootstrapToken(path string) (string, error) {
	return loadOrCreate(path, func() (string, error) {
		buffer := make([]byte, 32)
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		return "state_bootstrap_" + base64.RawURLEncoding.EncodeToString(buffer), nil
	})
}

func LoadOrCreateEncryptionKey(path string) ([]byte, error) {
	encoded, err := loadOrCreate(path, func() (string, error) {
		buffer := make([]byte, 32)
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(buffer), nil
	})
	if err != nil {
		return nil, err
	}
	key, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("invalid encryption key length")
	}
	return key, nil
}

func loadOrCreate(path string, generate func() (string, error)) (string, error) {
	if path == "" {
		return "", errors.New("secure file path is empty")
	}
	if value, found, err := load(path); found || err != nil {
		return value, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create secure file directory: %w", err)
	}
	value, err := generate()
	if err != nil {
		return "", fmt.Errorf("generate secure value: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		loaded, _, loadErr := load(path)
		return loaded, loadErr
	}
	if err != nil {
		return "", fmt.Errorf("create secure file: %w", err)
	}
	writeErr := func() error {
		if _, err := file.WriteString(value + "\n"); err != nil {
			return err
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if writeErr != nil {
		return "", fmt.Errorf("write secure file: %w", writeErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close secure file: %w", closeErr)
	}
	return value, nil
}

func load(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect secure file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", true, errors.New("secure file is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", true, fmt.Errorf("secure file permissions %o expose secret data", info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", true, fmt.Errorf("read secure file: %w", err)
	}
	value := strings.TrimSpace(string(contents))
	if value == "" {
		return "", true, errors.New("secure file is empty")
	}
	return value, true, nil
}
