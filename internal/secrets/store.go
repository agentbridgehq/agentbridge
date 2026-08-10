package secrets

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/99designs/keyring"
)

// ErrNotFound is returned when a secret is not stored.
var ErrNotFound = errors.New("secret not found")

// Store holds secret values.
type Store interface {
	// Get returns a secret's value.
	Get(name string) (string, error)
	// Set stores a secret.
	Set(name, value string) error
	// Delete removes a secret. Removing an absent secret is not an error.
	Delete(name string) error
	// List returns the names of stored secrets. Values are never listed.
	List() ([]string, error)
	// Describe names the backing store, for messages.
	Describe() string
}

// ServiceName is the keychain service entries are filed under.
const ServiceName = "agentbridge"

// EnvPrefix is the environment variable prefix the environment backend reads.
const EnvPrefix = "AGENTBRIDGE_SECRET_"

// Keyring stores secrets in the operating system's credential store: Keychain
// on macOS, Credential Manager on Windows, and Secret Service on Linux.
//
// This is the whole point of the package. A value in a keychain is mediated by
// the OS — it can require the keychain to be unlocked, it is not readable by
// every process that can read the user's home directory, and it does not end up
// in a backup, a screen share, or a repository the way a config file does.
type Keyring struct{ ring keyring.Keyring }

// OpenKeyring opens the platform credential store.
func OpenKeyring() (*Keyring, error) {
	ring, err := keyring.Open(keyring.Config{
		ServiceName: ServiceName,
		// A file-backed fallback is deliberately not offered. Falling back to
		// an encrypted file on disk when no credential store is available
		// would quietly reintroduce the problem this package exists to solve,
		// and would do it at exactly the moment nobody is watching. Headless
		// environments use the environment backend instead, which at least
		// makes the exposure obvious.
		AllowedBackends: []keyring.BackendType{
			keyring.KeychainBackend,
			keyring.WinCredBackend,
			keyring.SecretServiceBackend,
			keyring.KWalletBackend,
		},
		KeychainTrustApplication: true,
		KeychainSynchronizable:   false,
	})
	if err != nil {
		return nil, fmt.Errorf("no OS credential store is available: %w", err)
	}
	return &Keyring{ring: ring}, nil
}

// Get implements Store.
func (k *Keyring) Get(name string) (string, error) {
	item, err := k.ring.Get(name)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return "", fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	if err != nil {
		return "", err
	}
	return string(item.Data), nil
}

// Set implements Store.
func (k *Keyring) Set(name, value string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	return k.ring.Set(keyring.Item{
		Key:         name,
		Data:        []byte(value),
		Label:       ServiceName + ": " + name,
		Description: "agentbridge plugin secret",
	})
}

// Delete implements Store.
func (k *Keyring) Delete(name string) error {
	err := k.ring.Remove(name)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return nil
	}
	return err
}

// List implements Store.
func (k *Keyring) List() ([]string, error) {
	keys, err := k.ring.Keys()
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

// Describe implements Store.
func (k *Keyring) Describe() string { return "OS credential store" }

// Env reads secrets from environment variables, for CI and headless machines
// where no credential store exists.
//
// A secret named "acme.db/api-token" is read from AGENTBRIDGE_SECRET_ACME_DB_API_TOKEN.
// It is read-only by design: a build agent supplies secrets, it does not store
// them, and pretending otherwise would imply a persistence this backend does
// not have.
type Env struct{}

// EnvVarName returns the environment variable a secret name maps to.
func EnvVarName(name string) string {
	upper := strings.ToUpper(name)
	replaced := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, upper)
	return EnvPrefix + replaced
}

// Get implements Store.
func (Env) Get(name string) (string, error) {
	if v, ok := os.LookupEnv(EnvVarName(name)); ok {
		return v, nil
	}
	return "", fmt.Errorf("%q (%s): %w", name, EnvVarName(name), ErrNotFound)
}

// Set implements Store.
func (Env) Set(name, value string) error {
	return fmt.Errorf("the environment backend is read-only: set %s in the environment instead", EnvVarName(name))
}

// Delete implements Store.
func (Env) Delete(name string) error {
	return fmt.Errorf("the environment backend is read-only: unset %s instead", EnvVarName(name))
}

// List implements Store.
func (Env) List() ([]string, error) {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, EnvPrefix) {
			continue
		}
		if name, _, ok := strings.Cut(kv, "="); ok {
			out = append(out, strings.TrimPrefix(name, EnvPrefix))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Describe implements Store.
func (Env) Describe() string { return "environment (" + EnvPrefix + "*)" }

// Chain tries each store in order.
//
// The environment comes first so a CI run can override a developer's local
// keychain without either knowing about the other — the same lockfile then
// works on a laptop and on a build agent, which is the property that makes
// secret references usable at all.
type Chain []Store

// Open returns the default store chain.
func Open() Store {
	chain := Chain{Env{}}
	if ring, err := OpenKeyring(); err == nil {
		chain = append(chain, ring)
	}
	return chain
}

// Get implements Store.
func (c Chain) Get(name string) (string, error) {
	var lastErr error
	for _, s := range c {
		v, err := s.Get(name)
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return "", err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	return "", lastErr
}

// Set implements Store, writing to the first store that accepts it.
func (c Chain) Set(name, value string) error {
	for _, s := range c {
		if err := s.Set(name, value); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no writable secret store is available; " +
		"on a headless machine, supply secrets through the environment instead")
}

// Delete implements Store.
func (c Chain) Delete(name string) error {
	for _, s := range c {
		if _, err := s.Get(name); err != nil {
			continue
		}
		if err := s.Delete(name); err == nil {
			return nil
		}
	}
	return nil
}

// List implements Store.
func (c Chain) List() ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, s := range c {
		names, err := s.List()
		if err != nil {
			continue
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Describe implements Store.
func (c Chain) Describe() string {
	parts := make([]string, 0, len(c))
	for _, s := range c {
		parts = append(parts, s.Describe())
	}
	return strings.Join(parts, ", then ")
}

// Memory is an in-process store, for tests.
type Memory struct{ values map[string]string }

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{values: map[string]string{}} }

// Get implements Store.
func (m *Memory) Get(name string) (string, error) {
	v, ok := m.values[name]
	if !ok {
		return "", fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	return v, nil
}

// Set implements Store.
func (m *Memory) Set(name, value string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	m.values[name] = value
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(name string) error { delete(m.values, name); return nil }

// List implements Store.
func (m *Memory) List() ([]string, error) {
	out := make([]string, 0, len(m.values))
	for k := range m.values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// Describe implements Store.
func (m *Memory) Describe() string { return "in-memory" }
