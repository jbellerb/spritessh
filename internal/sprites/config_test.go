package sprites

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenOptions_Resolve_WithExplicitToken(t *testing.T) {
	opts := &TokenOptions{
		AuthToken: "explicit-token-12345",
		API:       "https://api.sprites.dev",
	}

	if err := opts.Resolve(); err != nil {
		t.Fatalf("failed to resolve token options: %v", err)
	}

	if opts.AuthToken != "explicit-token-12345" {
		t.Errorf("expected token 'explicit-token-12345', got '%s'", opts.AuthToken)
	}
	if opts.API != "https://api.sprites.dev" {
		t.Errorf("expected API 'https://api.sprites.dev', got '%s'", opts.API)
	}
}

func TestTokenOptions_Resolve_ErrorWhenNoTokenAndNoConfig(t *testing.T) {
	opts := &TokenOptions{
		AuthToken: "", // no explicit token
		API:       "https://api.sprites.dev",
	}

	// do not create config file
	t.Setenv("HOME", t.TempDir())

	if err := opts.Resolve(); err == nil {
		t.Fatal("expected error when no token provided, got nil")
	}

	if opts.AuthToken != "" {
		t.Errorf("expected empty token on error, got '%s'", opts.AuthToken)
	}
}

func TestTokenOptions_ResolveWithConfig_WithCurrentSelection(t *testing.T) {
	cfg := &Config{
		Version: "1",
		CurrentSelection: &CurrentSelection{
			URL: "https://api.sprites.dev",
			Org: "test-org",
		},
		URLs: map[string]*URLConfig{
			"https://api.sprites.dev": {
				URL: "https://api.sprites.dev",
				Orgs: map[string]*Org{
					"test-org": {
						Name:  "test-org",
						Token: "test-token-from-config",
					},
				},
			},
		},
	}

	opts := &TokenOptions{
		API:          "", // no explicit api or org, should use current_selection
		Organization: "",
	}

	if err := opts.ResolveWithConfig(cfg); err != nil {
		t.Fatalf("failed to resolve token options: %v", err)
	}

	if opts.AuthToken != "test-token-from-config" {
		t.Errorf("expected token 'test-token-from-config', got '%s'", opts.AuthToken)
	}
	if opts.API != "https://api.sprites.dev" {
		t.Errorf("expected API 'https://api.sprites.dev', got '%s'", opts.API)
	}
	if opts.Organization != "test-org" {
		t.Errorf("expected organiation 'test-org', got '%s'", opts.Organization)
	}
}

func TestTokenOptions_ResolveWithConfig_WithExplicitOrg(t *testing.T) {
	cfg := &Config{
		Version: "1",
		CurrentSelection: &CurrentSelection{
			URL: "https://api.sprites.dev",
			Org: "default-org",
		},
		URLs: map[string]*URLConfig{
			"https://api.sprites.dev": {
				URL: "https://api.sprites.dev",
				Orgs: map[string]*Org{
					"default-org": {
						Name:  "default-org",
						Token: "default-token",
					},
					"explicit-org": {
						Name:  "explicit-org",
						Token: "explicit-org-token",
					},
				},
			},
		},
	}

	opts := &TokenOptions{
		API:          "https://api.sprites.dev",
		Organization: "explicit-org", // explicit org should override current_selection
	}

	if err := opts.ResolveWithConfig(cfg); err != nil {
		t.Fatalf("failed to resolve token options: %v", err)
	}

	if opts.AuthToken != "explicit-org-token" {
		t.Errorf("expected token 'explicit-org-token', got '%s'", opts.AuthToken)
	}
}

func TestTokenOptions_ResolveWithConfig_DoesNotOverrideExplicitToken(t *testing.T) {
	cfg := &Config{
		Version: "1",
		CurrentSelection: &CurrentSelection{
			URL: "https://api.sprites.dev",
			Org: "test-org",
		},
		URLs: map[string]*URLConfig{
			"https://api.sprites.dev": {
				URL: "https://api.sprites.dev",
				Orgs: map[string]*Org{
					"test-org": {
						Name:  "test-org",
						Token: "config-token",
					},
				},
			},
		},
	}

	opts := &TokenOptions{
		AuthToken:    "explicit-token",
		API:          "https://api.sprites.dev",
		Organization: "",
	}

	if err := opts.ResolveWithConfig(cfg); err != nil {
		t.Fatalf("failed to resolve token options: %v", err)
	}

	if opts.AuthToken != "explicit-token" {
		t.Errorf("expected token 'explicit-token', got '%s'", opts.AuthToken)
	}
}

func TestTokenOptions_ResolveWithConfig_NilCurrentSelection(t *testing.T) {
	cfg := &Config{
		Version:          "1",
		CurrentSelection: nil, // nil selection should be handled gracefully
		URLs: map[string]*URLConfig{
			"https://api.sprites.dev": {
				URL: "https://api.sprites.dev",
				Orgs: map[string]*Org{
					"test-org": {
						Name:  "test-org",
						Token: "test-token-from-config",
					},
				},
			},
		},
	}

	opts := &TokenOptions{}

	if err := opts.ResolveWithConfig(cfg); err == nil {
		t.Fatal("expected error when CurrentSelection is nil, got nil")
	}
}

func TestTokenOptions_ResolveWithConfig_ExplicitURLAndOrgWithNilSelection(t *testing.T) {
	cfg := &Config{
		Version:          "1",
		CurrentSelection: nil, // even with nil selection, explicit values should work
		URLs: map[string]*URLConfig{
			"https://api.sprites.dev": {
				URL: "https://api.sprites.dev",
				Orgs: map[string]*Org{
					"test-org": {
						Name:  "test-org",
						Token: "test-token-from-config",
					},
				},
			},
		},
	}

	opts := &TokenOptions{
		AuthToken:    "",
		API:          "https://api.sprites.dev",
		Organization: "test-org",
	}

	if err := opts.ResolveWithConfig(cfg); err != nil {
		t.Fatalf("failed to resolve token options: %v", err)
	}

	if opts.AuthToken != "test-token-from-config" {
		t.Errorf("expected token 'test-token-from-config', got '%s'", opts.AuthToken)
	}
}

func TestTokenOptions_ResolveWithConfig_ExplicitAPIDifferentFromCurrentSelection(t *testing.T) {
	// cfg has CurrentSelection pointing to production API, but user explicitly
	// specifies staging API
	cfg := &Config{
		Version: "1",
		CurrentSelection: &CurrentSelection{
			URL: "https://api.sprites.dev",
			Org: "prod-org",
		},
		URLs: map[string]*URLConfig{
			"https://api.sprites.dev": {
				URL: "https://api.sprites.dev",
				Orgs: map[string]*Org{
					"prod-org": {
						Name:  "prod-org",
						Token: "prod-token",
					},
				},
			},
			"https://staging-api.sprites.dev": {
				URL: "https://staging-api.sprites.dev",
				Orgs: map[string]*Org{
					"staging-org": {
						Name:  "staging-org",
						Token: "staging-token",
					},
				},
			},
		},
	}

	opts := &TokenOptions{
		AuthToken:    "",
		API:          "https://staging-api.sprites.dev",
		Organization: "staging-org",
	}

	if err := opts.ResolveWithConfig(cfg); err != nil {
		t.Fatalf("failed to resolve token options: %v", err)
	}

	// should use the explicitly specified API/org, not CurrentSelection
	if opts.AuthToken != "staging-token" {
		t.Errorf("expected token 'staging-token', got '%s'", opts.AuthToken)
	}
	if opts.API != "https://staging-api.sprites.dev" {
		t.Errorf("expected API 'https://staging-api.sprites.dev', got '%s'", opts.API)
	}
	if opts.Organization != "staging-org" {
		t.Errorf("expected org 'staging-org', got '%s'", opts.Organization)
	}
}

func TestTokenOptions_Resolve_WithUserSpecificConfig(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	cfgDir := filepath.Join(tmpHome, ".sprites")

	cfg := &Config{
		Version: "1",
		CurrentSelection: &CurrentSelection{
			URL: "https://api.sprites.dev",
			Org: "test-org",
		},
		URLs: nil, // even with nil URLs, user-specific URLs are used
		Users: []*User{{
			ID:         "test-user",
			ConfigPath: filepath.Join(cfgDir, "users", "test-user.json"),
		}},
		CurrentUser: "test-user",
	}

	userCfg := &Config{
		Version: "1",
		URLs: map[string]*URLConfig{
			"https://api.sprites.dev": {
				URL: "https://api.sprites.dev",
				Orgs: map[string]*Org{
					"test-org": {
						Name:  "test-org",
						Token: "test-token-from-config",
					},
				},
			},
		},
	}

	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfgDir, "users"), 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	cfgRaw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to encode configuration: %v", err)
	}
	userCfgRaw, err := json.Marshal(userCfg)
	if err != nil {
		t.Fatalf("failed to encode user configuration: %v", err)
	}

	err = os.WriteFile(filepath.Join(cfgDir, "sprites.json"), cfgRaw, 0600)
	if err != nil {
		t.Fatalf("failed to write configuration: %v", err)
	}
	err = os.WriteFile(filepath.Join(cfgDir, "users", "test-user.json"), userCfgRaw, 0600)
	if err != nil {
		t.Fatalf("failed to write user configuration: %v", err)
	}

	opts := &TokenOptions{
		AuthToken: "",
		API:       "https://api.sprites.dev",
	}

	if err := opts.Resolve(); err != nil {
		t.Fatalf("failed to resolve token options: %v", err)
	}

	if opts.AuthToken != "test-token-from-config" {
		t.Errorf("expected token 'test-token-from-config', got '%s'", opts.AuthToken)
	}
}

func TestConfig_GetToken_WithFileKeyringFallback(t *testing.T) {
	cfg := &Config{
		Version:     "1",
		CurrentUser: "test-user",
	}

	org := &Org{
		Name:       "test-org",
		KeyringKey: "sprites:org:https://api.sprites.dev:test-org",
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// setup the keyring at ~/.sprite/keyring/sprites-cli-test-user/test-key
	serviceDir := filepath.Join(tmpHome, ".sprite", "keyring", "sprites-cli-test-user")
	keyPath := filepath.Join(serviceDir, "sprites-org-https-", "api.sprites.dev-test-org")

	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		t.Fatalf("failed to create keyring dir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("user-specific-token"), 0600); err != nil {
		t.Fatalf("failed to write keyring file: %v", err)
	}

	token, err := cfg.GetToken(org)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if token != "user-specific-token" {
		t.Errorf("expected token 'user-specific-token', got '%s'", token)
	}
}
