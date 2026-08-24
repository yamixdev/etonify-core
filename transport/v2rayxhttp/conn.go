package v2rayxhttp

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// splitConn implements net.Conn over independent XHTTP upload and download
// streams. Closing it must stop both streams and release the owning client
// lease exactly once.
type splitConn struct {
	writer     io.WriteCloser
	reader     io.ReadCloser
	remoteAddr net.Addr
	localAddr  net.Addr
	onClose    func()
	closeOnce  sync.Once
	closeErr   error

	writerCloseOnce sync.Once
	readerCloseOnce sync.Once
	writerCloseErr  error
	readerCloseErr  error
	deadlineAccess  sync.Mutex
	readDeadline    time.Time
	writeDeadline   time.Time
	readGeneration  uint64
	writeGeneration uint64
	readTimer       *time.Timer
	writeTimer      *time.Timer
	readExpired     atomic.Bool
	writeExpired    atomic.Bool
}

func (c *splitConn) Write(b []byte) (int, error) {
	written, err := c.writer.Write(b)
	if err != nil && c.writeExpired.Load() {
		return written, os.ErrDeadlineExceeded
	}
	return written, err
}

func (c *splitConn) Read(b []byte) (int, error) {
	read, err := c.reader.Read(b)
	if err != nil && c.readExpired.Load() {
		return read, os.ErrDeadlineExceeded
	}
	return read, err
}

func (c *splitConn) Close() error {
	c.closeOnce.Do(func() {
		c.stopDeadlineTimers()
		writerErr := c.closeWriter()
		if c.onClose != nil {
			c.onClose()
		}
		readerErr := c.closeReader()
		c.closeErr = errors.Join(writerErr, readerErr)
	})
	return c.closeErr
}

func (c *splitConn) LocalAddr() net.Addr {
	if c.localAddr != nil {
		return c.localAddr
	}
	return &net.TCPAddr{}
}

func (c *splitConn) RemoteAddr() net.Addr {
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return &net.TCPAddr{}
}

func (c *splitConn) SetDeadline(deadline time.Time) error {
	c.setReadDeadline(deadline)
	c.setWriteDeadline(deadline)
	return nil
}

func (c *splitConn) SetReadDeadline(deadline time.Time) error {
	c.setReadDeadline(deadline)
	return nil
}

func (c *splitConn) SetWriteDeadline(deadline time.Time) error {
	c.setWriteDeadline(deadline)
	return nil
}

func (c *splitConn) NeedAdditionalReadDeadline() bool {
	return true
}

func (c *splitConn) setReadDeadline(deadline time.Time) {
	c.deadlineAccess.Lock()
	if c.readTimer != nil {
		c.readTimer.Stop()
		c.readTimer = nil
	}
	c.readDeadline = deadline
	c.readGeneration++
	generation := c.readGeneration
	c.readExpired.Store(false)
	if !deadline.IsZero() {
		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		c.readTimer = time.AfterFunc(delay, func() { c.expireRead(deadline, generation) })
	}
	c.deadlineAccess.Unlock()
}

func (c *splitConn) setWriteDeadline(deadline time.Time) {
	c.deadlineAccess.Lock()
	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
	c.writeDeadline = deadline
	c.writeGeneration++
	generation := c.writeGeneration
	c.writeExpired.Store(false)
	if !deadline.IsZero() {
		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		c.writeTimer = time.AfterFunc(delay, func() { c.expireWrite(deadline, generation) })
	}
	c.deadlineAccess.Unlock()
}

func (c *splitConn) expireRead(deadline time.Time, generation uint64) {
	c.deadlineAccess.Lock()
	if c.readGeneration != generation || c.readDeadline != deadline || deadline.IsZero() {
		c.deadlineAccess.Unlock()
		return
	}
	c.readTimer = nil
	c.readExpired.Store(true)
	c.deadlineAccess.Unlock()
	_ = c.closeReader()
}

func (c *splitConn) expireWrite(deadline time.Time, generation uint64) {
	c.deadlineAccess.Lock()
	if c.writeGeneration != generation || c.writeDeadline != deadline || deadline.IsZero() {
		c.deadlineAccess.Unlock()
		return
	}
	c.writeTimer = nil
	c.writeExpired.Store(true)
	c.deadlineAccess.Unlock()
	_ = c.closeWriter()
}

func (c *splitConn) stopDeadlineTimers() {
	c.deadlineAccess.Lock()
	if c.readTimer != nil {
		c.readTimer.Stop()
		c.readTimer = nil
	}
	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
	c.readDeadline = time.Time{}
	c.writeDeadline = time.Time{}
	c.deadlineAccess.Unlock()
}

func (c *splitConn) closeWriter() error {
	c.writerCloseOnce.Do(func() { c.writerCloseErr = c.writer.Close() })
	return c.writerCloseErr
}

func (c *splitConn) closeReader() error {
	c.readerCloseOnce.Do(func() { c.readerCloseErr = c.reader.Close() })
	return c.readerCloseErr
}

// waitReadCloser lets DialContext return before the download response arrives.
// Its state is protected because HTTP completion and connection cancellation
// run on different goroutines.
type waitReadCloser struct {
	access sync.Mutex
	ready  chan struct{}
	once   sync.Once
	reader io.ReadCloser
	err    error
	closed bool
}

func newWaitReadCloser() *waitReadCloser {
	return &waitReadCloser{ready: make(chan struct{})}
}

func (w *waitReadCloser) Set(reader io.ReadCloser) {
	w.access.Lock()
	if w.closed || w.reader != nil || w.err != nil {
		w.access.Unlock()
		_ = reader.Close()
		return
	}
	w.reader = reader
	w.signalLocked()
	w.access.Unlock()
}

func (w *waitReadCloser) Fail(err error) {
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	w.access.Lock()
	if !w.closed && w.reader == nil && w.err == nil {
		w.err = err
		w.signalLocked()
	}
	w.access.Unlock()
}

func (w *waitReadCloser) Read(buffer []byte) (int, error) {
	<-w.ready
	w.access.Lock()
	reader := w.reader
	err := w.err
	closed := w.closed
	w.access.Unlock()
	if reader == nil {
		if err != nil {
			return 0, err
		}
		if closed {
			return 0, io.ErrClosedPipe
		}
		return 0, io.ErrUnexpectedEOF
	}
	return reader.Read(buffer)
}

func (w *waitReadCloser) Close() error {
	w.access.Lock()
	if w.closed {
		w.access.Unlock()
		return nil
	}
	w.closed = true
	reader := w.reader
	if reader == nil && w.err == nil {
		w.err = io.ErrClosedPipe
	}
	w.signalLocked()
	w.access.Unlock()
	if reader != nil {
		return reader.Close()
	}
	return nil
}

func (w *waitReadCloser) signalLocked() {
	w.once.Do(func() { close(w.ready) })
}
