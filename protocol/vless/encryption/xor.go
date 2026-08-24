package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"io"
	"net"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"

	"lukechampine.com/blake3"
)

func newCTR(key, initializationVector []byte) cipher.Stream {
	derivedKey := make([]byte, 32)
	blake3.DeriveKey(derivedKey, "VLESS", key)
	block, _ := aes.NewCipher(derivedKey)
	clear(derivedKey)
	return cipher.NewCTR(block, initializationVector)
}

// xorConn only wraps CommonConn-owned output buffers. It is intentionally not
// exposed because the header transformation mutates those transient buffers.
type xorConn struct {
	net.Conn
	writeAccess sync.Mutex
	readAccess  sync.Mutex
	ctr         cipher.Stream
	peerCTR     cipher.Stream
	outSkip     int
	outHeader   []byte
	inSkip      int
	inHeader    []byte
}

func newXORConn(conn net.Conn, ctr, peerCTR cipher.Stream, outSkip, inSkip int) *xorConn {
	return &xorConn{
		Conn:      conn,
		ctr:       ctr,
		peerCTR:   peerCTR,
		outSkip:   outSkip,
		outHeader: make([]byte, 0, headerLength),
		inSkip:    inSkip,
		inHeader:  make([]byte, 0, headerLength),
	}
}

func (c *xorConn) setPeerCTR(peerCTR cipher.Stream) {
	c.readAccess.Lock()
	c.peerCTR = peerCTR
	c.readAccess.Unlock()
}

func (c *xorConn) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	c.writeAccess.Lock()
	defer c.writeAccess.Unlock()
	if err := c.transformWrite(payload); err != nil {
		return 0, err
	}
	if err := writeAll(c.Conn, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *xorConn) transformWrite(payload []byte) error {
	for len(payload) > 0 {
		if len(payload) <= c.outSkip {
			c.outSkip -= len(payload)
			return nil
		}
		payload = payload[c.outSkip:]
		c.outSkip = 0
		needed := headerLength - len(c.outHeader)
		if len(payload) < needed {
			c.outHeader = append(c.outHeader, payload...)
			c.ctr.XORKeyStream(payload, payload)
			return nil
		}
		header := append(c.outHeader, payload[:needed]...)
		nextSkip, err := decodeHeader(header)
		if err != nil {
			return E.Cause(err, "vless encryption: invalid outgoing xor record")
		}
		c.outSkip = nextSkip
		c.outHeader = c.outHeader[:0]
		c.ctr.XORKeyStream(payload[:needed], payload[:needed])
		payload = payload[needed:]
	}
	return nil
}

func (c *xorConn) Read(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	c.readAccess.Lock()
	defer c.readAccess.Unlock()
	written, readErr := c.Conn.Read(payload)
	if written == 0 {
		return 0, readErr
	}
	if err := c.transformRead(payload[:written]); err != nil {
		return 0, err
	}
	return written, readErr
}

func (c *xorConn) transformRead(payload []byte) error {
	for len(payload) > 0 {
		if len(payload) <= c.inSkip {
			c.inSkip -= len(payload)
			return nil
		}
		payload = payload[c.inSkip:]
		c.inSkip = 0
		if c.peerCTR == nil {
			return E.New("vless encryption: peer xor stream is not initialized")
		}
		needed := headerLength - len(c.inHeader)
		if len(payload) < needed {
			c.peerCTR.XORKeyStream(payload, payload)
			c.inHeader = append(c.inHeader, payload...)
			return nil
		}
		c.peerCTR.XORKeyStream(payload[:needed], payload[:needed])
		header := append(c.inHeader, payload[:needed]...)
		nextSkip, err := decodeHeader(header)
		if err != nil {
			return E.Cause(err, "vless encryption: invalid incoming xor record")
		}
		c.inSkip = nextSkip
		c.inHeader = c.inHeader[:0]
		payload = payload[needed:]
	}
	return nil
}

var _ net.Conn = (*xorConn)(nil)
var _ io.Reader = (*xorConn)(nil)
