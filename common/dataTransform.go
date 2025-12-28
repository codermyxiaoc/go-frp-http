package common

import (
	"io"
	"net"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

type TransformConfig struct {
	DstName              string
	SrcName              string
	EnableLongConnection bool
	ConnectionTimeout    int64
	EnableLimit          bool
	LimitBufferSize      int
}

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 32*1024)
	},
}

func Transform(dstConn, srcConn net.Conn, config TransformConfig) {
	if tcpConn, ok := dstConn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}
	if tcpConn, ok := srcConn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}

	var closeOnce sync.Once
	var wg sync.WaitGroup
	wg.Add(2)

	closeConn := func() {
		_ = dstConn.Close()
		_ = srcConn.Close()
	}

	var monitoredDst io.ReadWriter = dstConn
	var monitoredSrc io.ReadWriter = srcConn

	if !config.EnableLongConnection {
		session := NewSession(config.ConnectionTimeout, &closeConn)
		defer session.Close()
		monitoredDst = NewMonitored(dstConn, session)
		monitoredSrc = NewMonitored(srcConn, session)
		go session.monitorIdle()
	}

	if config.EnableLimit {
		limiter := rate.NewLimiter(rate.Limit(config.LimitBufferSize), config.LimitBufferSize)
		monitoredDst = NewRateLimited(monitoredDst, limiter)
		monitoredSrc = NewRateLimited(monitoredSrc, limiter)
	}

	bufA := bufferPool.Get().([]byte)
	bufB := bufferPool.Get().([]byte)
	defer bufferPool.Put(bufA)
	defer bufferPool.Put(bufB)

	go func() {
		defer func() {
			wg.Done()
			closeOnce.Do(closeConn)
		}()
		if _, err := io.CopyBuffer(monitoredDst, monitoredSrc, bufA); err != nil && !IsClosedError(err) {
			logrus.Errorf("%s->%s 转发异常: %v", config.DstName, config.SrcName, err)
		}
	}()

	go func() {
		defer func() {
			wg.Done()
			closeOnce.Do(closeConn)
		}()
		if _, err := io.CopyBuffer(monitoredSrc, monitoredDst, bufB); err != nil && !IsClosedError(err) {
			logrus.Errorf("%s->%s 转发异常: %v", config.DstName, config.SrcName, err)
		}
	}()

	wg.Wait()
}
