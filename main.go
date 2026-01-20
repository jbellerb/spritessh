package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
)

var (
	// vMajor.Minor.Patch of the current release.
	Version = "v0.0.0"
	// Date of the current commit.
	CommitDate = "1970-01-01"
	// Shortened (8 character) hash of the current commit.
	CommitHash = "00000000"
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			Version = info.Main.Version
		}

		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.time":
				CommitDate = setting.Value[:10]
			case "vcs.revision":
				CommitHash = setting.Value[:8]
			}
		}
	}
}

var (
	errUnknownCommand = errors.New("unknown command")
)

// Command is a subcommand of the program.
type Command interface {
	// Run executes the command.
	Run(ctx context.Context, args []string) error

	// Usage writes the usage information to an [io.Writer].
	Usage(w io.Writer)
}

func main() {
	cmd := NewMain()

	if err := cmd.Run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func waitForGracefulShutdown(ctx context.Context, cancel context.CancelFunc) {
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-done:
		slog.InfoContext(ctx, "Starting graceful shutdown", "shutdown.signal", sig)
		cancel()
	case <-ctx.Done():
		return
	}

	select {
	case sig := <-done:
		slog.ErrorContext(ctx, "Aborting", "shutdown.abort_signal", sig)
		os.Exit(1)
	case <-ctx.Done():
		return
	}
}

// Main is a the main command.
type Main struct {
	opts *RootOptions
}

// NewMain returns a new instance of Main.
func NewMain() *Main {
	return &Main{}
}

func (c *Main) Run(ctx context.Context, args []string) error {
	c.opts = NewRootOptions()
	args, err := ParseOptions(c, c.opts, args)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return c.help(nil)
	} else if args[0] == "help" {
		return c.help(args[1:])
	}

	subcmd, err := c.subcommand(args[0])
	if err != nil {
		return fmt.Errorf("spritessh %s: %w", args[0], err)
	}

	initLogger(&c.opts.LogLevel)
	mainCtx := WithLogAttrs(
		ctx,
		slog.String("svc.version", Version),
		slog.String("svc.commit.date", CommitDate),
		slog.String("svc.commit.hash", CommitHash),
	)

	return subcmd.Run(mainCtx, args[1:])
}

func (c *Main) help(args []string) error {
	var cmd Command
	var err error

	if len(args) == 0 {
		cmd = c
	} else if len(args) == 1 {
		cmd, err = c.subcommand(args[0])
	} else {
		err = errUnknownCommand
	}
	if err != nil {
		if errors.Is(err, errUnknownCommand) {
			input := strings.Join(args, " ")
			return fmt.Errorf("spritessh help %s: unknown help topic", input)
		}
		return err
	}

	cmd.Usage(os.Stderr)
	return nil
}

func (c *Main) subcommand(name string) (Command, error) {
	switch name {
	case "serve":
		return NewServeCommand(c.opts), nil
	case "version":
		return NewVersionCommand(), nil
	default:
		return nil, errUnknownCommand
	}
}

func (c *Main) Usage(w io.Writer) {
	fmt.Fprint(w, `Usage: spritessh <command>

Proxy SSH connections to sprites.

Commands:
  serve     run the SSH server
  version   print the spritessh version
  help      get help on other commands

Examples:
  Open an SSH server proxying sprites on port 2222.
  $ spritessh serve -l ':2222'

  Open an SSH server proxying sprites from the 'cool-sprites-123' organization.
  $ spritessh serve -o 'cool-sprites-123'

Notes:
  Use `+"`spritessh help <command>`"+` for details on a subcommand.
`)
}

// ServeCommand is a subcommand to start the SSH server.
type ServeCommand struct {
	rootOpts *RootOptions
}

// NewServeCommand returns a new instance of ServeCommand.
func NewServeCommand(opts *RootOptions) *ServeCommand {
	return &ServeCommand{rootOpts: opts}
}

// Run executes the command.
func (c *ServeCommand) Run(ctx context.Context, args []string) error {
	opts := NewServeOptions()
	_, err := ParseOptions(c, opts, args)
	if err != nil {
		return err
	} else if err := opts.Validate(); err != nil {
		return err
	}

	if err := c.rootOpts.Sprite.TokenOptions.Resolve(); err != nil {
		return err
	}
	srv := NewSSHServer(opts, c.rootOpts.Sprite)

	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go waitForGracefulShutdown(serveCtx, cancel)

	bindCtx, cancel := context.WithTimeout(serveCtx, opts.SocketTimeout)
	defer cancel()
	l, err := Bind(bindCtx, opts.ListenAddr)
	if err != nil {
		if bindCtx.Err() != context.Canceled {
			return fmt.Errorf("failed to bind to socket: %w", err)
		}
		return nil
	}

	initLogger(&c.rootOpts.LogLevel)

	slog.InfoContext(serveCtx, "Started SSH server", "server.addr", l.Addr().String())
	server := make(chan error)
	go func() { server <- srv.Serve(serveCtx, l) }()

	select {
	case <-serveCtx.Done():
		// create a new context for the grace period. serveCtx was closed when
		// graceful shutdown started, but the parent context is still open.
		shutdownCtx, cancel := context.WithTimeout(
			ctx, c.rootOpts.ShutdownGracePeriod,
		)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.WarnContext(
				shutdownCtx,
				"Shutdown did not complete in time, closing anyways",
				"svc.shutdown_timeout", c.rootOpts.ShutdownGracePeriod,
			)
		}

		return nil
	case err := <-server:
		if err != nil {
			slog.ErrorContext(serveCtx, "Socket unexpectedly closed", "exception", err)
		}

		return fmt.Errorf("server unexpectedly stopped")
	}
}

// Usage writes the usage information to an [io.Writer].
func (c *ServeCommand) Usage(w io.Writer) {
	fmt.Fprint(w, `Usage: spritessh serve [-l <addr>] [-o <organization>]

Run the SSH server for sprites.

Options:
  -l, --listen-addr <addr>    address to listen for connections on. Defaults
                              ":22".
  -o, --org <org>             fly.io organization of sprites to serve
                              connections for. Defaults to

Examples:
  Open an SSH server proxying sprites on port 2222.
  $ spritessh serve -l ':2222'

  Open an SSH server proxying sprites from the 'cool-sprites-123' organization.
  $ spritessh serve -o 'cool-sprites-123'
`)
}

// VersionCommand is a subcommand to print the version.
type VersionCommand struct{}

// NewVersionCommand returns a new instance of VersionCommand.
func NewVersionCommand() *VersionCommand {
	return &VersionCommand{}
}

func (c *VersionCommand) Run(_ context.Context, _ []string) error {
	fmt.Printf(
		"spritessh %s (built on %s from commit %s)\n",
		Version, CommitDate, CommitHash,
	)
	return nil
}

func (c *VersionCommand) Usage(w io.Writer) {
	fmt.Fprint(w, `Usage: spritessh version

Print the version and build information.
`)
}

type loggingContextKey struct{}

type loggingContext struct {
	parent *loggingContext
	attrs  []slog.Attr
}

// WithLogAttrs adds a set of attributes to the context for [ContextHandler] to
// include in log output.
func WithLogAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	parent, _ := ctx.Value(loggingContextKey{}).(*loggingContext)
	return context.WithValue(ctx, loggingContextKey{}, &loggingContext{parent, attrs})
}

// ContextHandler is a [slog.Handler] that pulls in extra attributes added to
// the context by [WithLogAttrs].
type ContextHandler struct {
	slog.Handler
}

// NewContextHandler returns a new instance of ContextHandler.
func NewContextHandler(handler slog.Handler) *ContextHandler {
	return &ContextHandler{handler}
}

func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	data, ok := ctx.Value(loggingContextKey{}).(*loggingContext)
	if ok && data != nil {
		r.AddAttrs(slog.Bool("event", true))
		for data != nil {
			r.AddAttrs(data.attrs...)
			data = data.parent
		}
	}

	return h.Handler.Handle(ctx, r)
}

func initLogger(level *LogLevel) {
	jsonHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level.Level})
	ctxHandler := NewContextHandler(jsonHandler)
	slog.SetDefault(slog.New(ctxHandler))
}
