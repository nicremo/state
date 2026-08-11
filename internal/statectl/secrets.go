package statectl

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const keyringService = "com.fabincrm.state.statectl"

var ErrCredentialNotFound = errors.New("statectl credential not found")

type SecretStore interface {
	Set(account string, value string) error
	Get(account string) (string, error)
	Delete(account string) error
}

type KeyringSecretStore struct{}

func (KeyringSecretStore) Set(account string, value string) error {
	return keyring.Set(keyringService, account, value)
}

func (KeyringSecretStore) Get(account string) (string, error) {
	value, err := keyring.Get(keyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrCredentialNotFound
	}
	return value, err
}

func (KeyringSecretStore) Delete(account string) error {
	err := keyring.Delete(keyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
