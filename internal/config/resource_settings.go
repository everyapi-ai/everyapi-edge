package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

// ResourcePolicyStore keeps machine-owned admission settings beside the Edge identity. Environment values remain the fallback until the operator saves an explicit policy in Control Room.
type ResourcePolicyStore struct {
	path        string
	fallback    protocol.ResourcePolicy
	totalVRAMGB int
	mu          sync.RWMutex
}

func NewResourcePolicyStore(path string, fallback protocol.ResourcePolicy, totalVRAMGB int) *ResourcePolicyStore {
	return &ResourcePolicyStore{path: path, fallback: fallback, totalVRAMGB: totalVRAMGB}
}

func (s *ResourcePolicyStore) Load() (protocol.ResourcePolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.load()
}

func (s *ResourcePolicyStore) load() (protocol.ResourcePolicy, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s.fallback, nil
	}
	if err != nil {
		return protocol.ResourcePolicy{}, fmt.Errorf("read resource policy: %w", err)
	}
	var policy protocol.ResourcePolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return protocol.ResourcePolicy{}, fmt.Errorf("decode resource policy: %w", err)
	}
	if err := validateResourcePolicy(policy, s.totalVRAMGB); err != nil {
		return protocol.ResourcePolicy{}, fmt.Errorf("validate stored resource policy: %w", err)
	}
	return policy, nil
}

func (s *ResourcePolicyStore) Save(policy protocol.ResourcePolicy) error {
	if err := validateResourcePolicy(policy, s.totalVRAMGB); err != nil {
		return err
	}
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Errorf("encode resource policy: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create resource policy directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".resource-policy-*.tmp")
	if err != nil {
		return fmt.Errorf("create resource policy temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(append(data, '\n'))
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write resource policy: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace resource policy: %w", err)
	}
	return nil
}
