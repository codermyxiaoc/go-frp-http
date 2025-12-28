package common

import (
	"context"
	"io"
	"net"
	"time"

	"golang.org/x/time/rate"
)

const defaultRateLimitTimeout = 30 * time.Second

type RateLimitedConn struct {
	net.Conn
	rl      *RateLimiter
	timeout time.Duration
}

func NewRateLimitedConn(conn net.Conn, limiter *rate.Limiter) net.Conn {
	return &RateLimitedConn{
		Conn:    conn,
		rl:      NewRateLimited(conn, limiter),
		timeout: defaultRateLimitTimeout,
	}
}

func (c *RateLimitedConn) Read(p []byte) (int, error) {
	return c.rl.ReadWithTimeout(p, c.timeout)
}

func (c *RateLimitedConn) Write(p []byte) (int, error) {
	return c.rl.WriteWithTimeout(p, c.timeout)
}

type RateLimiter struct {
	rw      io.ReadWriter
	limiter *rate.Limiter
}

func NewRateLimited(rw io.ReadWriter, limiter *rate.Limiter) *RateLimiter {
	return &RateLimiter{
		rw:      rw,
		limiter: limiter,
	}
}

func (r *RateLimiter) Read(p []byte) (int, error) {
	return r.ReadWithTimeout(p, defaultRateLimitTimeout)
}

func (r *RateLimiter) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	if r.rw == nil {
		return 0, io.ErrUnexpectedEOF
	}

	n, err := r.rw.Read(p)
	if n > 0 && r.limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if waitErr := r.limiter.WaitN(ctx, n); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}

func (r *RateLimiter) Write(p []byte) (int, error) {
	return r.WriteWithTimeout(p, defaultRateLimitTimeout)
}

func (r *RateLimiter) WriteWithTimeout(p []byte, timeout time.Duration) (int, error) {
	if r.rw == nil {
		return 0, io.ErrUnexpectedEOF
	}

	if r.limiter != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := r.limiter.WaitN(ctx, len(p)); err != nil {
			return 0, err
		}
	}
	return r.rw.Write(p)
}
