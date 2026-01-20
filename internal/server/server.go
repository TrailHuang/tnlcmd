package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"cmdline/internal/session"
)

type TelnetServer struct {
	addr     string
	listener net.Listener
	mu       sync.RWMutex
	sessions map[*session.Session]struct{}
	done     chan struct{}
}

func NewTelnetServer(addr string) *TelnetServer {
	return &TelnetServer{
		addr:     addr,
		sessions: make(map[*session.Session]struct{}),
		done:     make(chan struct{}),
	}
}

func (s *TelnetServer) Start(ctx context.Context) error {
	var err error
	s.listener, err = net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	go s.acceptLoop(ctx)
	go s.cleanupLoop(ctx)

	return nil
}

func (s *TelnetServer) acceptLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			log.Printf("Accept error: %v", err)
			continue
		}

		go s.handleConnection(ctx, conn)
	}
}

func (s *TelnetServer) handleConnection(ctx context.Context, conn net.Conn) {
	log.Printf("New connection from %s", conn.RemoteAddr())

	sess := session.NewSession(conn)
	s.addSession(sess)
	defer s.removeSession(sess)

	if err := sess.Handle(ctx); err != nil {
		log.Printf("Session error: %v", err)
	}

	conn.Close()
	log.Printf("Connection closed from %s", conn.RemoteAddr())
}

func (s *TelnetServer) addSession(sess *session.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess] = struct{}{}
}

func (s *TelnetServer) removeSession(sess *session.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sess)
}

func (s *TelnetServer) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
			s.cleanupStaleSessions()
		}
	}
}

func (s *TelnetServer) cleanupStaleSessions() {
	s.mu.RLock()
	sessions := make([]*session.Session, 0, len(s.sessions))
	for sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.RUnlock()

	for _, sess := range sessions {
		if sess.IsStale() {
			sess.Close()
		}
	}
}

func (s *TelnetServer) Stop(ctx context.Context) error {
	close(s.done)

	if s.listener != nil {
		s.listener.Close()
	}

	s.mu.RLock()
	sessions := make([]*session.Session, 0, len(s.sessions))
	for sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.RUnlock()

	for _, sess := range sessions {
		sess.Close()
	}

	return nil
}