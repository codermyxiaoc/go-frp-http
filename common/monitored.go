package common

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Session struct {
	lastActivity      int64
	connectionTimeout int64
	closeChan         chan struct{}
	rollback          *func()
	rollbackOnce      sync.Once
}

func NewSession(connectionTimeout int64, rollback *func()) *Session {
	return &Session{
		connectionTimeout: connectionTimeout,
		closeChan:         make(chan struct{}),
		lastActivity:      time.Now().Unix(),
		rollback:          rollback,
	}
}

func (s *Session) monitorIdle() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().Unix()-atomic.LoadInt64(&s.lastActivity) > s.connectionTimeout {
				s.rollbackOnce.Do(*s.rollback)
				return
			}
		case <-s.closeChan:
			return
		}
	}
}

func (s *Session) updateActivity() {
	now := time.Now().Unix()
	if now > atomic.LoadInt64(&s.lastActivity) {
		atomic.StoreInt64(&s.lastActivity, now)
	}
}

func (s *Session) Close() {
	select {
	case <-s.closeChan:
	default:
		close(s.closeChan)
	}
}

type Monitored struct {
	Conn    net.Conn
	session *Session
}

func NewMonitored(conn net.Conn, session *Session) *Monitored {
	return &Monitored{
		Conn:    conn,
		session: session,
	}
}

func (m *Monitored) Read(p []byte) (int, error) {
	n, err := m.Conn.Read(p)
	if n > 0 {
		m.session.updateActivity()
	}
	return n, err
}

func (m *Monitored) Write(p []byte) (int, error) {
	n, err := m.Conn.Write(p)
	if n > 0 {
		m.session.updateActivity()
	}
	return n, err
}
