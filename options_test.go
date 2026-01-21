package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHostKey_WithExistingValidKey(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test_host_ed25519_key")

	// key taken from pyca/cryptography's test vectors
	validKey := `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACDdZgztgAFFC7T5PifrUy/kMu0Pnwq1au3vStKHe7FFMAAAAJhNWbUCTVm1
AgAAAAtzc2gtZWQyNTUxOQAAACDdZgztgAFFC7T5PifrUy/kMu0Pnwq1au3vStKHe7FFMA
AAAECQxzIh6s9TpOOlHcnFpjQIdZWmrhsU3eTq05iGHQejl91mDO2AAUULtPk+J+tTL+Qy
7Q+fCrVq7e9K0od7sUUwAAAAEWVkMjU1MTktbm9wc3cua2V5AQIDBA==
-----END OPENSSH PRIVATE KEY-----`
	if err := os.WriteFile(path, []byte(validKey), 0600); err != nil {
		t.Fatalf("failed to write test key file: %v", err)
	}

	signer, err := loadHostKey(path)
	if err != nil {
		t.Fatalf("failed to load SSH host key: %v", err)
	}

	if signer == nil {
		t.Fatal("expected non-nil signer, got nil")
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("expected key type 'ssh-ed25519', got '%s'", signer.PublicKey().Type())
	}
}

func TestLoadHostKey_WithMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "nonexistent_host_ed25519_key")

	_, err := loadHostKey(keyPath)
	if err == nil {
		t.Fatal("expected error when loading nonexistent file, got nil")
	}

	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist error, got: %v", err)
	}
}

func TestLoadHostKey_WithCorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "corrupted_host_ed25519_key")

	corruptedData := []byte("this is not a valid SSH key")
	if err := os.WriteFile(keyPath, corruptedData, 0600); err != nil {
		t.Fatalf("failed to write corrupted key file: %v", err)
	}

	_, err := loadHostKey(keyPath)
	if err == nil {
		t.Fatal("expected error when loading corrupted key, got nil")
	}

	// verify file still exists and wasn't overwritten
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read key file after load attempt: %v", err)
	}

	if string(data) != string(corruptedData) {
		t.Error("corrupted key file was modified, expected it to remain unchanged")
	}
}

func TestGenerateHostKey_CreatesValidKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "generated_host_ed25519_key")

	signer, err := generateHostKey(keyPath)
	if err != nil {
		t.Fatalf("failed to generate SSH host key: %v", err)
	}

	if signer == nil {
		t.Fatal("expected non-nil signer, got nil")
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("expected key type 'ssh-ed25519', got '%s'", signer.PublicKey().Type())
	}

	// verify the file exists
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Fatal("expected key file to exist after generation, but it doesn't")
	}

	// verify it has the correct permissions
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("failed to stat generated key file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected file permissions 0600, got %04o", info.Mode().Perm())
	}

	// verify we can load the generated key
	if loadHostKey(keyPath); err != nil {
		t.Errorf("failed to load generated SSH host key: %v", err)
	}
}
