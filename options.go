package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	configLogLevelVar            = "SPRITESSH_LOG"
	configShutdownGracePeriodVar = "SPRITESSH_SHUTDOWN_GRACE_PERIOD"

	configSpritesAPI        = "SPRITESSH_SPRITES_API"
	configSpritesAuthToken  = "SPRITESSH_SPRITES_AUTH_TOKEN"
	configSpritesMaxRetries = "SPRITESSH_SPRITES_MAX_RETRIES"

	configSSHSocketTimeoutVar = "SPRITESSH_SSH_SOCKET_TIMEOUT"
	configSSHListenAddr       = "SPRITESSH_SSH_LISTEN_ADDR"
	configSSHHostKeyEd25519   = "SPRITESSH_SSH_HOST_KEY_ED25519"
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
	return readEnvValues([]keyValue{
		{configLogLevelVar, &o.LogLevel},
		{configShutdownGracePeriodVar, (*DurationValue)(&o.ShutdownGracePeriod)},
		{configSpritesAPI, (*StringValue)(&o.Sprite.API)},
		{configSpritesAuthToken, (*StringValue)(&o.Sprite.AuthToken)},
		{configSpritesMaxRetries, (*IntValue)(&o.Sprite.MaxRetries)},
	})
}

func (o *RootOptions) Flags(fs *flag.FlagSet) {}

// SpriteOptions are shared options related to the Sprites API.
type SpriteOptions struct {
	API       string
	AuthToken string

	MaxRetries int
}

// NewSpriteOptions returns a new instance of SpriteOptions.
func NewSpriteOptions() *SpriteOptions {
	return &SpriteOptions{
		API:        "https://api.sprites.dev",
		MaxRetries: 5,
	}
}

// RootOptions are the options for the "spritessh serve" command.
type ServeOptions struct {
	SocketTimeout      time.Duration
	ListenAddr         string
	HostPrivateEd25519 Ed25519SigningKey
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
		{configSSHHostKeyEd25519, &o.HostPrivateEd25519},
	})
}

func (o *ServeOptions) Flags(fs *flag.FlagSet) {
	fs.Var((*StringValue)(&o.ListenAddr), "l", "")
	fs.Var((*StringValue)(&o.ListenAddr), "listen-addr", "")
}

func (o *ServeOptions) Validate() error {
	if o.HostPrivateEd25519.Key == nil {
		return fmt.Errorf("no host private keys set")
	}

	return nil
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

type Ed25519SigningKey struct {
	Key ssh.Signer
}

func (key *Ed25519SigningKey) Set(s string) error {
	private, err := ssh.ParsePrivateKey([]byte(s))
	if err != nil {
		return fmt.Errorf("parse Ed25519 private key: %w", err)
	}

	key.Key, err = ssh.NewSignerWithAlgorithms(private.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoED25519})
	if err != nil {
		return fmt.Errorf("expected Ed25519 private key: %w", err)
	}

	return nil
}

func (key *Ed25519SigningKey) String() string {
	if key.Key != nil {
		return "[Redacted Ed25519 Private Key]"
	}

	return ""
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

type IntValue int

func (s *IntValue) Set(val string) error {
	n, err := strconv.ParseInt(val, 10, 32)
	if err != nil {
		return err
	}

	*s = IntValue(n)
	return nil
}

func (s *IntValue) String() string {
	return fmt.Sprintf("%d", int(*s))
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
