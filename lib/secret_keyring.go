package lib

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/werf/lockgate"
	"github.com/werf/lockgate/pkg/file_locker"
	"github.com/zalando/go-keyring"
)

type KeyringStore struct {
	locker         lockgate.Locker   `json:"-"`
	lockResource   string            `json:"-"`
	user           string            `json:"-"`
	service        string            `json:"-"`
	AWSCredentials map[string]string `json:"credentials"`
}

func NewKeyringStore(provider string) *KeyringStore {
	dir := filepath.Join(os.TempDir(), "aws-cli-oidc-lock")
	l, err := file_locker.NewFileLocker(dir)
	if err != nil {
		Writeln("Can't setup lock dir: %s", dir)
		Exit(err)
	}
	s := &KeyringStore{
		locker:         l,
		lockResource:   "aws-cli-oidc",
		user:           os.Getenv("USER"),
		service:        "aws-cli-oidc/" + provider,
		AWSCredentials: make(map[string]string),
	}
	return s
}

func (s *KeyringStore) Load() error {
	acquired, lock, err := s.locker.Acquire(s.lockResource, lockgate.AcquireOptions{Shared: false, Timeout: 3 * time.Minute})
	if err != nil {
		return fmt.Errorf("can't load secret due to locked now: %w", err)
	}
	defer func() {
		if acquired {
			if err := s.locker.Release(lock); err != nil {
				Exit(fmt.Errorf("can't unlock: %w", err))
			}
		}
	}()
	if !acquired {
		return fmt.Errorf("can't load secret due to locked now: %w", err)
	}

	jsonStr, err := keyring.Get(s.service, s.user)
	if err != nil {
		if err == keyring.ErrNotFound {
			return nil
		}
		return fmt.Errorf("can't load secret due to unexpected error: %w", err)
	}
	if err := json.Unmarshal([]byte(jsonStr), &s); err != nil {
		return fmt.Errorf("can't load secret due to broken data: %w", err)
	}
	return nil
}

func (s *KeyringStore) Get(roleArn string) (*AWSCredentials, error) {
	jsonStr, ok := s.AWSCredentials[roleArn]
	if !ok {
		return nil, fmt.Errorf("not found the credential for %s", roleArn)
	}
	Writeln("Got credential from OS secret store for %s", roleArn)
	var cred AWSCredentials
	if err := json.Unmarshal([]byte(jsonStr), &cred); err != nil {
		return nil, fmt.Errorf("can't load secret due to the broken data: %w", err)
	}
	return &cred, nil
}

func (s *KeyringStore) Save(roleArn, cred string) error {
	acquired, lock, err := s.locker.Acquire(s.lockResource, lockgate.AcquireOptions{Shared: false, Timeout: 3 * time.Minute})
	if err != nil {
		return fmt.Errorf("can't save secret due to locked now: %w", err)
	}
	defer func() {
		if acquired {
			if err := s.locker.Release(lock); err != nil {
				Exit(errors.New("can't unlock"))
			}
		}
	}()
	// Load the latest credentials
	jsonStr, err := keyring.Get(s.service, s.user)
	if err != nil {
		if err != keyring.ErrNotFound {
			Exit(fmt.Errorf("can't load secret due to unexpected error: %w", err))
		}
	}
	if jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), &s); err != nil {
			return fmt.Errorf("can't load secret due to broken data: %w", err)
		}
	}
	// Add/Update credential
	s.AWSCredentials[roleArn] = cred
	// Save
	newJsonStr, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("can't marshal data: %w", err)
	}
	if err := keyring.Set(s.service, s.user, string(newJsonStr)); err != nil {
		return fmt.Errorf("can't save secret: %w", err)
	}
	return nil
}

func (s *KeyringStore) Clear() error {
	return keyring.Delete(s.service, s.user)
}
