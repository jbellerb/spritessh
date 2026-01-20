package main

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superfly/sprites-go"
	"golang.org/x/crypto/ssh"
)

var (
	errServerClosed = errors.New("server closed")

	errAlreadyRunning = errors.New("exec already running")
	errDuplicatePTY   = errors.New("session already has an attched pty")
	errUnknownReq     = errors.New("unexpected request type")
	errUnsupportedReq = errors.New("unsupported request type")
)

var maxBackoffDuration = 10 * time.Second

// Bech32 alphabet because I prefer it to RFC 4648 and Crockford's :P
var bech32Encoding = base32.NewEncoding("qpzry9x8gf2tvdw0s3jn54khce6mua7l").
	WithPadding(base32.NoPadding)

type Server struct {
	serverConfig  *ssh.ServerConfig
	spritesConfig *SpriteOptions

	mu        sync.Mutex
	closed    atomic.Bool
	listeners map[net.Listener]struct{}
	cancel    context.CancelFunc
	connGroup sync.WaitGroup
}

type permissionsSpriteKey struct{}

func NewSSHServer(opts *ServeOptions, spriteOpts *SpriteOptions) *Server {
	serverConfig := &ssh.ServerConfig{}
	serverConfig.AddHostKey(opts.HostPrivateEd25519.Key)

	client := sprites.New(spriteOpts.AuthToken, sprites.WithBaseURL(spriteOpts.API))

	ctx, cancel := context.WithCancel(context.Background())
	serverConfig.PublicKeyCallback = func(cm ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
		sprite, err := client.GetSprite(ctx, cm.User())
		if err != nil {
			return nil, &ssh.BannerError{Err: err, Message: "Sprite not found"}
		}

		return &ssh.Permissions{ExtraData: map[any]any{permissionsSpriteKey{}: sprite}}, nil
	}

	return &Server{
		serverConfig:  serverConfig,
		spritesConfig: spriteOpts,
		listeners:     make(map[net.Listener]struct{}),
		cancel:        cancel,
	}
}

func Bind(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	return l, nil
}

func (srv *Server) Serve(ctx context.Context, l net.Listener) error {
	listenCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := srv.trackListener(l, true); err != nil {
		return err
	}
	defer srv.trackListener(l, false)

	for {
		// add an open connection before accepting. This avoids dropping a
		// connection if Shutdown() is called between Accept() and handleConn().
		srv.connGroup.Add(1)
		conn, err := l.Accept()
		if err != nil {
			srv.connGroup.Done()
			return err
		}

		go srv.handleConn(listenCtx, conn, srv.spritesConfig.MaxRetries)
	}
}

func (srv *Server) trackListener(l net.Listener, add bool) error {
	srv.mu.Lock()
	defer srv.mu.Unlock()

	if add {
		if srv.closed.Load() {
			return errServerClosed
		}
		srv.listeners[l] = struct{}{}
	} else {
		delete(srv.listeners, l)
	}
	return nil
}

// Shutdown performs a graceful shutdown. The server closes all listeners and
// waits for all active connections to terminate.
func (srv *Server) Shutdown(ctx context.Context) error {
	if !srv.closed.CompareAndSwap(false, true) {
		return errServerClosed
	}

	// cancel any pending auth attempts
	srv.cancel()

	srv.mu.Lock()
	for l := range srv.listeners {
		if err := l.Close(); err != nil {
			slog.ErrorContext(
				ctx,
				"Failed to close listener",
				"server.addr", l.Addr().String(),
				"exception", err,
			)
		}
		delete(srv.listeners, l)
	}
	srv.mu.Unlock()

	shutdown := make(chan struct{})
	go func() {
		srv.connGroup.Wait()
		shutdown <- struct{}{}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-shutdown:
		return nil
	}
}

type Conn struct {
	conn *ssh.ServerConn
	wg   sync.WaitGroup

	maxSpriteRetries int
}

// Close closes the connection.
func (conn *Conn) Close() error {
	return conn.conn.Close()
}

// Wait waits for all sessions happening over the connection to end.
func (conn *Conn) Wait() {
	conn.wg.Wait()
}

// handleConn handles a full SSH connection.
func (srv *Server) handleConn(ctx context.Context, tcpConn net.Conn, maxSpriteRetries int) {
	defer srv.connGroup.Done()

	newConn, chans, reqs, err := ssh.NewServerConn(tcpConn, srv.serverConfig)
	if err != nil {
		slog.ErrorContext(ctx, "SSH handshake failed", "exception", err)
		return
	}
	conn := &Conn{conn: newConn, maxSpriteRetries: maxSpriteRetries}
	defer conn.Wait()

	sprite := newConn.Permissions.ExtraData[permissionsSpriteKey{}].(*sprites.Sprite)
	connCtx := WithLogAttrs(
		ctx,
		slog.Any("server.addr", newConn.LocalAddr().String()),
		slog.Any("conn.addr", newConn.RemoteAddr().String()),
		slog.Any("conn.id", bech32Encoding.EncodeToString(newConn.SessionID())),
		slog.String("sprite.name", sprite.Name()),
		slog.String("sprite.id", sprite.ID),
		slog.String("sprite.organization", sprite.OrganizationName),
		slog.String("sprite.region", sprite.PrimaryRegion),
	)

	for {
		select {
		case <-ctx.Done():
			conn.Close()
			return
		case newCh := <-chans:
			if newCh == nil {
				return
			}

			switch newCh.ChannelType() {
			case "session":
				go conn.handleSession(connCtx, newCh, sprite)
			case "forwarded-tcpip":
				// host, port, err := parseForwardedTCPRequest()
				// go conn.handleForwardTCP(connCtx, newCh, sprite)
				fallthrough
			default:
				newCh.Reject(ssh.UnknownChannelType, "unknown channel type")
			}
		case req := <-reqs:
			if req == nil {
				return
			}

			err := conn.handleReq(connCtx, req)
			if err != nil {
				slog.DebugContext(
					ctx,
					"Failed to handle connection request",
					"conn.req.type", req.Type,
					"conn.req.payload", hex.EncodeToString(req.Payload),
					"exception", err,
				)
			}
			if req.WantReply {
				req.Reply(err == nil, nil)
			}
		}
	}
}

type DirectTCPRequest struct {
	InitialWindowSize uint32
	MaxPacketSize     uint32

	DestHost   string
	DestPort   uint32
	OriginHost string
	OriginPort uint32
}

type TCPForwardRequest struct {
	Host string
	Port uint32
}

func (conn *Conn) handleReq(_ context.Context, req *ssh.Request) error {
	switch req.Type {
	default:
		return errUnknownReq
	}
}

type Session struct {
	ch     ssh.Channel
	sprite *sprites.Sprite
	cancel context.CancelFunc

	env     []string
	tty     bool
	running atomic.Bool

	win  WindowChangeRequest
	cond *sync.Cond
}

type EnvRequest struct {
	Name, Value string
}

type ExecRequest struct {
	Command string
}

type PtyRequest struct {
	// TERM environment variable
	Term string

	// Initial window parameters
	Cols, Rows, Width, Height uint32

	// Encoded terminal modes
	Modes string
}

type WindowChangeRequest struct {
	// Terminal size in characters
	Cols, Rows uint32

	// Terminal size in pixels
	Width, Height uint32
}

// handleSession handles a single SSH session.
func (conn *Conn) handleSession(ctx context.Context, newCh ssh.NewChannel, sprite *sprites.Sprite) {
	conn.wg.Add(1)
	defer conn.wg.Done()

	ch, reqs, err := newCh.Accept()
	if err != nil {
		slog.ErrorContext(ctx, "Failed to accept channel", "exception", err)
		return
	}
	defer ch.Close()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := Session{
		sprite: sprite,
		ch:     ch,
		cancel: cancel,
		cond:   sync.NewCond(new(sync.Mutex)),
	}

	for {
		select {
		case <-sessionCtx.Done():
			return
		case req := <-reqs:
			if req == nil {
				return
			}

			err := s.handleReq(sessionCtx, req, conn.maxSpriteRetries)
			if err != nil && !errors.Is(err, errUnsupportedReq) {
				slog.DebugContext(
					ctx,
					"Failed to handle session request",
					"session.req.type", req.Type,
					"session.req.payload", hex.EncodeToString(req.Payload),
					"exception", err,
				)
			}
			if req.WantReply {
				req.Reply(err == nil, nil)
			}
		}
	}
}

func (s *Session) handleReq(ctx context.Context, req *ssh.Request, maxSpriteRetries int) error {
	switch req.Type {
	case "env":
		var er EnvRequest
		if err := ssh.Unmarshal(req.Payload, &er); err != nil {
			return err
		} else if s.running.Load() {
			return errAlreadyRunning
		} else {
			s.env = append(s.env, er.Name+"="+er.Value)
			return nil
		}
	case "exec", "shell":
		var er ExecRequest
		if len(req.Payload) > 0 {
			if err := ssh.Unmarshal(req.Payload, &er); err != nil {
				return err
			}
		}

		return s.Exec(ctx, er.Command, maxSpriteRetries)
	case "pty-req":
		var pr PtyRequest
		if err := ssh.Unmarshal(req.Payload, &pr); err != nil {
			return err
		} else if s.running.Load() {
			return errAlreadyRunning
		} else if s.tty {
			return errDuplicatePTY
		}

		s.env = append(s.env, "TERM="+pr.Term)
		s.tty = true
		s.setWindow(WindowChangeRequest{pr.Cols, pr.Rows, pr.Width, pr.Height})

		// TODO: handle terminal modes

		return nil
	case "window-change":
		var wr WindowChangeRequest
		if err := ssh.Unmarshal(req.Payload, &wr); err != nil {
			return err
		}

		s.setWindow(wr)
		return nil
	case "agent-auth-req@openssh.com", "signal", "subsystem", "x11-req":
		return errUnsupportedReq
	default:
		return errUnknownReq
	}
}

func (s *Session) setWindow(win WindowChangeRequest) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()

	s.win = win
	s.cond.Signal()
}

func (s *Session) Exec(ctx context.Context, command string, maxRetries int) error {
	if !s.running.CompareAndSwap(false, true) {
		return errAlreadyRunning
	}

	if command == "" {
		command = "/.sprite/bin/sprite-console"
	}

	go func() {
		attempt := 1
		for {
			err := s.exec(ctx, command)
			if err == nil {
				break
			}

			if shouldRetry(err) && attempt < maxRetries {
				// Backoff before retrying
				delay := min(1<<min(attempt, 63), int64(maxBackoffDuration))
				attempt += 1

				select {
				case <-time.After(time.Duration(mrand.Int63n(delay))):
					continue
				case <-ctx.Done():
					err = ctx.Err()
				}
			}
			slog.ErrorContext(ctx, "Failed to exec sprite", "exception", err)
			break
		}
		s.cancel()
	}()

	return nil
}

// shouldRetry returns if the error was transient and should be retried or not.
func shouldRetry(err error) bool {
	// network timeouts
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}

	// other misc. errors
	transientMessages := []string{
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
	}
	errLower := strings.ToLower(err.Error())
	for _, msg := range transientMessages {
		if strings.Contains(errLower, msg) {
			return true
		}
	}

	return false
}

func (s *Session) exec(ctx context.Context, command string) error {
	cmd := s.sprite.CommandContext(
		ctx, "/usr/bin/sudo", "--user=sprite", "--login", "/bin/sh", "-c",
		// escape single quotes to avoid any word splitting except by $SHELL
		fmt.Sprintf(
			`${SHELL:-/bin/bash} -c '%s'`, strings.ReplaceAll(command, `'`, `'"'"'`),
		),
	)

	cmd.Stdin, cmd.Stdout, cmd.Stderr = s.ch, s.ch, s.ch.Stderr()
	cmd.Env = s.env
	if s.tty {
		cmd.SetTTY(true)
		cmd.SetTTYSize(uint16(s.win.Cols), uint16(s.win.Rows))

		winCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go s.listenForWindowChange(winCtx, cmd)
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	slog.InfoContext(
		ctx,
		"Started exec session",
		"session.exec.tty", s.tty,
		"session.exec.cmd", command,
	)
	// TODO: get session ID for resume

	var exit *sprites.ExitError
	if err := cmd.Wait(); err != nil && !errors.As(err, &exit) {
		return err
	}

	var status [4]byte
	if exit != nil {
		binary.BigEndian.PutUint32(status[:], uint32(exit.ExitCode()))
	}
	if _, err := s.ch.SendRequest("exit-status", false, status[:]); err != nil {
		return err
	}

	return nil
}

func (s *Session) listenForWindowChange(ctx context.Context, cmd *sprites.Cmd) error {
	// register cond to be woken up if the context is cancelled
	stopf := context.AfterFunc(ctx, func() {
		s.cond.L.Lock()
		defer s.cond.L.Unlock()

		s.cond.Broadcast()
	})
	defer stopf()

	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	for {
		s.cond.Wait()
		// we either got woken up by a window change request or by the context
		// being cancelled. Check the context first.
		if err := ctx.Err(); err != nil {
			return err
		} else if err := cmd.SetTTYSize(uint16(s.win.Cols), uint16(s.win.Rows)); err != nil {
			return err
		}
		slog.InfoContext(ctx, "Applied window change")
	}
}
