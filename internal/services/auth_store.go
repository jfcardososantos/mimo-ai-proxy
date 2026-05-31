package services

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const authStorePath = "data/auth.json"

type StoredAuth struct {
	XiaomiCookie  string `json:"xiaomiCookie,omitempty"`
	ServiceToken  string `json:"serviceToken,omitempty"`
	UserID        string `json:"userId,omitempty"`
	XiaomiChatbot string `json:"xiaomiChatbotPh,omitempty"`
}

func authConfigPath() string {
	if custom := strings.TrimSpace(os.Getenv("AUTH_STORE_PATH")); custom != "" {
		return custom
	}
	return authStorePath
}

func AuthStorePathForDisplay() string {
	return authConfigPath()
}

func LoadStoredAuth() (StoredAuth, error) {
	path := authConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StoredAuth{}, nil
		}
		return StoredAuth{}, err
	}

	var stored StoredAuth
	if err := json.Unmarshal(data, &stored); err != nil {
		return StoredAuth{}, err
	}

	return stored, nil
}

func SaveStoredAuth(stored StoredAuth) error {
	path := authConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

func ClearStoredAuth() error {
	path := authConfigPath()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
