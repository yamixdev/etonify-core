package v2rayxhttp

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitReadCloserDeliversReader(t *testing.T) {
	t.Parallel()

	waitReader := newWaitReadCloser()
	result := make(chan string, 1)
	go func() {
		content, err := io.ReadAll(waitReader)
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		result <- string(content)
	}()
	waitReader.Set(io.NopCloser(strings.NewReader("ready")))
	require.Equal(t, "ready", <-result)
	require.NoError(t, waitReader.Close())
}

func TestWaitReadCloserPropagatesFailure(t *testing.T) {
	t.Parallel()

	expected := errors.New("download failed")
	waitReader := newWaitReadCloser()
	waitReader.Fail(expected)
	_, err := waitReader.Read(make([]byte, 1))
	require.ErrorIs(t, err, expected)
	require.NoError(t, waitReader.Close())
}

func TestSplitConnCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	uploadReader, uploadWriter := io.Pipe()
	downloadReader, downloadWriter := io.Pipe()
	t.Cleanup(func() {
		_ = uploadReader.Close()
		_ = downloadWriter.Close()
	})
	var access sync.Mutex
	closeCount := 0
	conn := &splitConn{
		writer: uploadWriter,
		reader: downloadReader,
		onClose: func() {
			access.Lock()
			closeCount++
			access.Unlock()
		},
	}
	require.NoError(t, conn.Close())
	require.NoError(t, conn.Close())
	access.Lock()
	require.Equal(t, 1, closeCount)
	access.Unlock()
}

func TestSplitConnReadDeadline(t *testing.T) {
	uploadReader, uploadWriter := io.Pipe()
	downloadReader, downloadWriter := io.Pipe()
	defer uploadReader.Close()
	defer downloadWriter.Close()
	conn := &splitConn{writer: uploadWriter, reader: downloadReader}
	defer conn.Close()
	defer downloadWriter.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(20*time.Millisecond)))
	_, err := conn.Read(make([]byte, 1))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
}

func TestSplitConnWriteDeadline(t *testing.T) {
	uploadReader, uploadWriter := io.Pipe()
	downloadReader, downloadWriter := io.Pipe()
	defer uploadReader.Close()
	defer downloadWriter.Close()
	conn := &splitConn{writer: uploadWriter, reader: downloadReader}
	defer conn.Close()
	defer downloadWriter.Close()

	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(20*time.Millisecond)))
	_, err := conn.Write([]byte{1})
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
}

func TestSplitConnClearsDeadline(t *testing.T) {
	uploadReader, uploadWriter := io.Pipe()
	downloadReader, downloadWriter := io.Pipe()
	defer uploadReader.Close()
	defer downloadWriter.Close()
	conn := &splitConn{writer: uploadWriter, reader: downloadReader}
	defer conn.Close()
	defer downloadWriter.Close()

	deadline := time.Now().Add(20 * time.Millisecond)
	require.NoError(t, conn.SetReadDeadline(deadline))
	require.NoError(t, conn.SetReadDeadline(deadline))
	require.NoError(t, conn.SetReadDeadline(time.Time{}))
	time.Sleep(40 * time.Millisecond)
	go func() { _, _ = downloadWriter.Write([]byte{7}) }()
	buffer := make([]byte, 1)
	_, err := conn.Read(buffer)
	require.NoError(t, err)
	require.Equal(t, byte(7), buffer[0])
}
