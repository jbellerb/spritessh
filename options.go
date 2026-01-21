package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jbellerb/spritessh/internal/sprites"
	"golang.org/x/crypto/ssh"
)

const (
	configLogLevelVar            = "SPRITESSH_LOG"
	configShutdownGracePeriodVar = "SPRITESSH_SHUTDOWN_GRACE_PERIOD"

	configSpritesAPI          = "SPRITESSH_SPRITES_API"
	configSpritesAuthToken    = "SPRITESSH_SPRITES_AUTH_TOKEN"
	configSpritesOrganization = "SPRITESSH_SPRITES_ORGANIZATION"
	configSpritesMaxRetries   = "SPRITESSH_SPRITES_MAX_RETRIES"

	configSSHSocketTimeoutVar = "SPRITESSH_SSH_SOCKET_TIMEOUT"
	configSSHListenAddr       = "SPRITESSH_SSH_LISTEN_ADDR"
	configSSHHostKeyEd25519   = "SPRITESSH_SSH_HOST_KEY_ED25519"
)

var (
	defaultHostKeyName    = "spritessh_host_ed25519_key"
	defaultHostKeyComment = "sprite@spritessh"
)

// Options is a set of options for a subcommand.
type Options interface {
	// ReadEnv loads any options set by environment variables.
	ReadEnv() error

	// Flags applies the required flags to the flag set.
	Flags(fs *flag.FlagSet)
}

// ParseOptions loads a new set of options from the command line.
//
// In order of lowest to highest priority, options are set by the default value,
// evironment variables, and command line flags.
func ParseOptions(c Command, opts Options, args []string) ([]string, error) {
	if err := opts.ReadEnv(); err != nil {
		return nil, err
	}

	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.Usage = func() { c.Usage(fs.Output()) }
	opts.Flags(fs)

	var b strings.Builder
	fs.SetOutput(&b)

	if err := fs.Parse(args); err != nil && !errors.Is(err, flag.ErrHelp) {
		return nil, fmt.Errorf("%s", b.String())
	}

	return fs.Args(), nil
}

// RootOptions is the options for the "spritessh" command.
type RootOptions struct {
	LogLevel            LogLevel
	ShutdownGracePeriod time.Duration

	Sprite *SpriteOptions
}

// NewRootOptions returns a new instance of RootOptions.
func NewRootOptions() *RootOptions {
	return &RootOptions{
		LogLevel:            LogLevel{Level: slog.LevelInfo},
		ShutdownGracePeriod: 10 * time.Second,
		Sprite:              NewSpriteOptions(),
	}
}

func (o *RootOptions) ReadEnv() error {
	if err := o.Sprite.ReadEnv(); err != nil {
		return err
	}

	return readEnvValues([]keyValue{
		{configLogLevelVar, &o.LogLevel},
		{configShutdownGracePeriodVar, (*DurationValue)(&o.ShutdownGracePeriod)},
	})
}

func (o *RootOptions) Flags(fs *flag.FlagSet) {
	o.Sprite.Flags(fs)
}

// SpriteOptions are shared options related to the Sprites API.
type SpriteOptions struct {
	sprites.TokenOptions

	MaxRetries int
}

// NewSpriteOptions returns a new instance of SpriteOptions.
func NewSpriteOptions() *SpriteOptions {
	return &SpriteOptions{MaxRetries: 5}
}

func (o *SpriteOptions) ReadEnv() error {
	return readEnvValues([]keyValue{
		{configSpritesAPI, (*StringValue)(&o.API)},
		{configSpritesAuthToken, (*StringValue)(&o.AuthToken)},
		{configSpritesOrganization, (*StringValue)(&o.Organization)},
		{configSpritesMaxRetries, (*IntValue)(&o.MaxRetries)},
	})
}

func (o *SpriteOptions) Flags(fs *flag.FlagSet) {
	fs.Var((*StringValue)(&o.Organization), "o", "")
	fs.Var((*StringValue)(&o.Organization), "org", "")
}

// RootOptions are the options for the "spritessh serve" command.
type ServeOptions struct {
	SocketTimeout      time.Duration
	ListenAddr         string
	HostPrivateEd25519 ssh.Signer
}

func NewServeOptions() *ServeOptions {
	return &ServeOptions{
		SocketTimeout: 10 * time.Second,
		ListenAddr:    ":22",
	}
}

func (o *ServeOptions) ReadEnv() error {
	return readEnvValues([]keyValue{
		{configSSHSocketTimeoutVar, (*DurationValue)(&o.SocketTimeout)},
		{configSSHListenAddr, (*StringValue)(&o.ListenAddr)},
		{configSSHHostKeyEd25519, &Ed25519SignerValue{o.HostPrivateEd25519}},
	})
}

func (o *ServeOptions) Flags(fs *flag.FlagSet) {
	fs.Var((*StringValue)(&o.ListenAddr), "l", "")
	fs.Var((*StringValue)(&o.ListenAddr), "listen-addr", "")
}

type LogLevel struct {
	Level slog.Level
}

func (level *LogLevel) Set(s string) error {
	switch s {
	case "DEBUG":
		level.Level = slog.LevelDebug
	case "INFO":
		level.Level = slog.LevelInfo
	case "WARN":
		level.Level = slog.LevelWarn
	case "ERROR":
		level.Level = slog.LevelError
	default:
		return fmt.Errorf("unknown log level: %s", s)
	}

	return nil
}

func (log *LogLevel) String() string {
	switch log.Level {
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	default:
		return strconv.Itoa(int(log.Level))
	}
}

// defaultHostKeyPath returns the default path to the server host key:
// ~/.ssh/spritessh_host_ed25519_key
func defaultHostKeyPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".ssh", defaultHostKeyName), nil
}

// loadHostKey loads the Ed25519 host key at the given path.
func loadHostKey(path string) (ssh.Signer, error) {
	rawKey, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var k Ed25519SignerValue
	if err := k.Set(string(rawKey)); err != nil {
		return nil, err
	}

	return k.Signer, nil
}

// generateHostKey generates a new Ed25519 host key and writes it to the given
// path.
func generateHostKey(path string) (ssh.Signer, error) {
	rawPub, rawPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	pub, err := ssh.NewPublicKey(rawPub)
	if err != nil {
		return nil, err
	}
	priv, err := ssh.NewSignerFromKey(rawPriv)
	if err != nil {
		return nil, err
	}

	// ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	// write the private key
	privPem, err := ssh.MarshalPrivateKey(rawPriv, defaultHostKeyComment)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pem.Encode(&buf, privPem); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		return nil, err
	}

	// write the public key, ignoring errors
	pubAuth := string(ssh.MarshalAuthorizedKey(pub))
	pubAuth = fmt.Sprintf("%s %s\n", strings.TrimSuffix(pubAuth, "\n"), defaultHostKeyComment)
	_ = os.WriteFile(path+".pub", []byte(pubAuth), 0644)

	return priv, nil
}

type keyValue struct {
	key   string
	value flag.Value
}

func readEnvValues(vars []keyValue) error {
	for _, v := range vars {
		if val, ok := os.LookupEnv(v.key); ok {
			if err := v.value.Set(val); err != nil {
				return err
			}
		}
	}
	return nil
}

type Ed25519SignerValue struct{ ssh.Signer }

func (s *Ed25519SignerValue) Set(val string) error {
	priv, err := ssh.ParsePrivateKey([]byte(val))
	if err != nil {
		return fmt.Errorf("parse SSH private key: %w", err)
	}

	s.Signer, err = ssh.NewSignerWithAlgorithms(
		priv.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoED25519},
	)
	if err != nil {
		return fmt.Errorf("expected Ed25519 private key: %w", err)
	}

	return nil
}

func (s *Ed25519SignerValue) String() string {
	if s.Signer != nil {
		return "[Redacted SSH Private Key]"
	}

	return ""
}

type IntValue int

func (i *IntValue) Set(val string) error {
	n, err := strconv.ParseInt(val, 10, 32)
	if err != nil {
		return err
	}

	*i = IntValue(n)
	return nil
}

func (i *IntValue) String() string {
	return fmt.Sprintf("%d", int(*i))
}

type StringValue string

func (s *StringValue) Set(val string) error {
	*s = StringValue(val)
	return nil
}

func (s *StringValue) String() string {
	return fmt.Sprintf("\"%s\"", string(*s))
}

type DurationValue time.Duration

func (d *DurationValue) Set(s string) error {
	v, err := time.ParseDuration(s)
	*d = DurationValue(v)
	return err
}

func (d *DurationValue) String() string {
	return (*time.Duration)(d).String()
}
