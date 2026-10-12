// Package dawserve holds one Studio Pro session open and answers ucdaw
// requests over a Unix socket, so each CLI or MCP call skips discovery, the
// licence handshake and the snapshot. It reconnects when Studio Pro restarts.
package dawserve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/steveclarke/ucmix/internal/daw"
	"github.com/steveclarke/ucmix/internal/dawschema"
)

type wireRequest struct {
	Request daw.Request `json:"request"`
}

type wireError struct {
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type wireResponse struct {
	Result *daw.Result `json:"result,omitempty"`
	Error  *wireError  `json:"error,omitempty"`
}

// Connector opens a Studio Pro session (daw.Connect in production).
type Connector func(ctx context.Context) (*daw.Session, error)

// Server is the held session plus its socket listener.
type Server struct {
	Schema  *dawschema.Catalog
	Connect Connector
	Log     func(format string, args ...any)

	mu      sync.Mutex // guards session and since
	session *daw.Session
	since   time.Time
	work    sync.Mutex // serializes requests that change the song
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// current returns a live session, connecting if there is none.
func (s *Server) current(ctx context.Context) (*daw.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		select {
		case <-s.session.Done():
			s.logf("Studio Pro connection lost")
			s.session = nil
		default:
			return s.session, nil
		}
	}
	session, err := s.Connect(ctx)
	if err != nil {
		return nil, err
	}
	s.session, s.since = session, time.Now()
	e := session.Endpoint()
	s.logf("connected to Studio Pro on %s (%s)", e.Name, e.IP)
	return session, nil
}

// keepConnected reconnects in the background after Studio Pro goes away, so
// the model is current before the next request arrives.
func (s *Server) keepConnected(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		session, err := s.current(ctx)
		if err != nil {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 5*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		select {
		case <-session.Done():
		case <-ctx.Done():
			return
		}
	}
}

// Do answers one request on the held session.
func (s *Server) Do(ctx context.Context, req daw.Request) (daw.Result, error) {
	switch req.Op {
	case "params":
		return daw.Params(s.Schema, req.Filter), nil
	case "status":
		return s.status(), nil
	case "plugins":
		return daw.Plugins(req.Filter)
	}
	session, err := s.current(ctx)
	if err != nil {
		return daw.Result{}, err
	}
	// Stop must never wait behind a timed play.
	if !(req.Op == "transport" && req.Action == "stop") {
		s.work.Lock()
		defer s.work.Unlock()
	}
	return daw.NewStudio(session, s.Schema).Do(ctx, req)
}

func (s *Server) status() daw.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return daw.Result{Message: "ucdaw serve is running but not connected to Studio Pro."}
	}
	e := s.session.Endpoint()
	return daw.Result{
		Message: fmt.Sprintf("Connected to Studio Pro on %s since %s.", e.Name, s.since.Format("15:04")),
		Data:    e,
	}
}

// Serve listens on socketPath until ctx ends.
func (s *Server) Serve(ctx context.Context, socketPath string) error {
	if conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond); err == nil {
		_ = conn.Close()
		return daw.Problem{
			Message: "ucdaw serve is already running.",
			Hint:    "stop the other one first, or just use it",
		}
	}
	if err := ensurePrivateDir(filepath.Dir(socketPath)); err != nil {
		return err
	}
	if err := clearStaleSocket(socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("dawserve: listening on %s: %w", socketPath, err)
	}
	_ = os.Chmod(socketPath, 0o600)
	defer func() { _ = os.Remove(socketPath) }()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	go s.keepConnected(ctx)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.closeSession()
				return nil
			}
			return fmt.Errorf("dawserve: accepting: %w", err)
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) closeSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		_ = s.session.Close()
		s.session = nil
	}
}

// maxRequest caps one request line.
const maxRequest = 64 << 10

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if err := checkPeer(conn); err != nil {
		s.logf("refused a connection: %v", err)
		return
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), maxRequest)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		var in wireRequest
		var out wireResponse
		if err := json.Unmarshal(scanner.Bytes(), &in); err != nil {
			out.Error = &wireError{Message: "bad request: " + err.Error()}
		} else {
			result, err := s.Do(ctx, in.Request)
			if err != nil {
				out.Error = toWireError(err)
			} else {
				out.Result = &result
			}
		}
		if err := encoder.Encode(out); err != nil {
			return
		}
	}
}

func toWireError(err error) *wireError {
	var p daw.Problem
	if errors.As(err, &p) {
		return &wireError{Message: p.Message, Hint: p.Hint}
	}
	return &wireError{Message: err.Error()}
}

// Client sends requests to a running serve process.
type Client struct {
	Socket string
}

// Available reports whether a serve process run by this user answers on the
// socket.
func (c Client) Available() bool {
	if c.Socket == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", c.Socket, 200*time.Millisecond)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	return checkPeer(conn) == nil
}

// Do implements daw.Engine over the socket.
func (c Client) Do(ctx context.Context, req daw.Request) (daw.Result, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return daw.Result{}, fmt.Errorf("dawserve: connecting to serve: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := checkPeer(conn); err != nil {
		return daw.Result{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if err := json.NewEncoder(conn).Encode(wireRequest{Request: req}); err != nil {
		return daw.Result{}, fmt.Errorf("dawserve: sending request: %w", err)
	}
	var out wireResponse
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&out); err != nil {
		if ctx.Err() != nil {
			return daw.Result{}, ctx.Err()
		}
		return daw.Result{}, fmt.Errorf("dawserve: reading reply: %w", err)
	}
	if out.Error != nil {
		return daw.Result{}, daw.Problem{Message: out.Error.Message, Hint: out.Error.Hint}
	}
	if out.Result == nil {
		return daw.Result{}, errors.New("dawserve: empty reply")
	}
	return *out.Result, nil
}
