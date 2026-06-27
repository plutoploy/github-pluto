package store

import (
	"encoding/json"
	"os"
	"sync"
)

type InstallationStore struct {
	mu            sync.RWMutex
	filePath      string
	Installations map[int64]*Installation `json:"installations"`
}

type Installation struct {
	ID              int64    `json:"id"`
	AccountLogin    string   `json:"account_login"`
	AccountType    string   `json:"account_type"`
	RepositorySelection string `json:"repository_selection"`
	Repositories   []string `json:"repositories"`
}

func NewFileStore(filePath string) (*InstallationStore, error) {
	store := &InstallationStore{
		filePath:      filePath,
		Installations: make(map[int64]*Installation),
	}

	data, err := os.ReadFile(filePath)
	if err == nil {
		json.Unmarshal(data, &store.Installations)
	}

	return store, nil
}

func (s *InstallationStore) Save(installationID int64, inst *Installation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Installations[installationID] = inst
	data, err := json.MarshalIndent(s.Installations, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}

func (s *InstallationStore) Get(installationID int64) (*Installation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.Installations[installationID]
	return inst, ok
}

func (s *InstallationStore) List() []*Installation {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []*Installation
	for _, inst := range s.Installations {
		list = append(list, inst)
	}
	return list
}

func (s *InstallationStore) Delete(installationID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.Installations, installationID)
	data, err := json.MarshalIndent(s.Installations, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}
