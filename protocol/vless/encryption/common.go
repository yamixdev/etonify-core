package encryption

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/sys/cpu"
	"lukechampine.com/blake3"
)

const (
	headerLength        = 5
	ivLength            = 16
	nonceLength         = 12
	ticketLength        = 16
	aeadTagLength       = 16
	hash256Length       = 32
	encryptedLengthSize = 2 + aeadTagLength
	dataChunkSize       = 8192
	minPacketLength     = 1 + aeadTagLength
	maxPacketLength     = 16384 + 256
	minPaddingLength    = encryptedLengthSize + minPacketLength
	maxTotalPadding     = encryptedLengthSize + 65535
	maxPaddingSegments  = 16
	maxPaddingGap       = 5 * time.Second
	maxPaddingGapTotal  = 10 * time.Second
	x25519KeySize       = 32
)

var hasAESGCMHardwareSupport = func() bool {
	hasAMD64 := cpu.X86.HasAES && cpu.X86.HasPCLMULQDQ && cpu.X86.HasSSE41 && cpu.X86.HasSSSE3
	hasARM64 := cpu.ARM64.HasAES && cpu.ARM64.HasPMULL
	hasS390X := cpu.S390X.HasAES && cpu.S390X.HasAESCTR && cpu.S390X.HasGHASH
	hasPPC64 := runtime.GOARCH == "ppc64" || runtime.GOARCH == "ppc64le"
	return hasAMD64 || hasARM64 || hasS390X || hasPPC64
}()

type overrideAESKey struct{}

// OverrideUseAES is intended for deterministic interoperability tests.
func OverrideUseAES(ctx context.Context, useAES bool) context.Context {
	return context.WithValue(ctx, overrideAESKey{}, useAES)
}

func useAESFromContext(ctx context.Context) bool {
	if value, loaded := ctx.Value(overrideAESKey{}).(bool); loaded {
		return value
	}
	return hasAESGCMHardwareSupport
}

var (
	_ net.Conn             = (*CommonConn)(nil)
	_ N.ReaderWithUpstream = (*CommonConn)(nil)
	_ N.WriterWithUpstream = (*CommonConn)(nil)
)

// CommonConn is the authenticated record layer used after the key exchange.
// Read and Write are independently serialized, matching net.Conn's concurrent
// reader/writer contract without racing the per-direction AEAD nonces.
type CommonConn struct {
	conn        net.Conn
	useAES      bool
	client      *Client
	unitedKey   []byte
	preWrite    []byte
	aead        *aeadState
	peerAEAD    *aeadState
	peerPadding []byte

	writeAccess sync.Mutex
	readAccess  sync.Mutex
	rawInput    bytes.Buffer
	input       bytes.Reader
}

func newCommonConn(conn net.Conn, useAES bool) *CommonConn {
	return &CommonConn{conn: conn, useAES: useAES}
}

func (c *CommonConn) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	c.writeAccess.Lock()
	defer c.writeAccess.Unlock()

	total := len(payload)
	for len(payload) > 0 {
		chunk := payload
		if len(chunk) > dataChunkSize {
			chunk = chunk[:dataChunkSize]
		}
		payload = payload[len(chunk):]

		preWrite := c.preWrite
		output := buf.NewSize(len(preWrite) + headerLength + len(chunk) + c.aead.Overhead())
		outputBytes := output.Extend(output.Cap())
		position := copy(outputBytes, preWrite)
		c.preWrite = nil
		encodeHeader(outputBytes[position:position+headerLength], len(chunk)+c.aead.Overhead())
		nonceAtMaximum := bytes.Equal(c.aead.nonce[:], maximumNonce)
		sealed := c.aead.Seal(
			outputBytes[position+headerLength:position+headerLength],
			nil,
			chunk,
			outputBytes[position:position+headerLength],
		)
		if len(sealed) != len(chunk)+c.aead.Overhead() {
			output.Release()
			return total - len(payload) - len(chunk), E.New("vless encryption: unexpected sealed record length")
		}
		if nonceAtMaximum {
			c.aead = newAEAD(outputBytes, c.unitedKey, c.useAES)
		}
		err := writeAll(c.conn, outputBytes)
		output.Release()
		if err != nil {
			return total - len(payload) - len(chunk), err
		}
	}
	return total, nil
}

func (c *CommonConn) Read(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	c.readAccess.Lock()
	defer c.readAccess.Unlock()

	if c.peerAEAD == nil {
		serverRandom := make([]byte, ivLength)
		if _, err := io.ReadFull(c.conn, serverRandom); err != nil {
			return 0, err
		}
		c.peerAEAD = newAEAD(serverRandom, c.unitedKey, c.useAES)
		if xorConnection, loaded := c.conn.(*xorConn); loaded {
			xorConnection.setPeerCTR(newCTR(c.unitedKey, serverRandom))
		}
	}
	if c.peerPadding != nil {
		if _, err := io.ReadFull(c.conn, c.peerPadding); err != nil {
			return 0, err
		}
		if _, err := c.peerAEAD.Open(c.peerPadding[:0], nil, c.peerPadding, nil); err != nil {
			return 0, E.Cause(err, "vless encryption: decrypt peer padding")
		}
		c.peerPadding = nil
	}
	if c.input.Len() > 0 {
		return c.input.Read(payload)
	}

	peerHeader := [headerLength]byte{}
	if _, err := io.ReadFull(c.conn, peerHeader[:]); err != nil {
		return 0, c.handlePeerHeaderError(err)
	}
	recordLength, err := decodeHeader(peerHeader[:])
	if err != nil {
		return 0, c.handlePeerHeaderError(err)
	}
	c.client = nil
	if c.rawInput.Cap() < recordLength {
		c.rawInput.Grow(recordLength)
	}
	peerData := c.rawInput.Bytes()[:recordLength]
	if _, err := io.ReadFull(c.conn, peerData); err != nil {
		return 0, err
	}

	plaintext := peerData[:recordLength-aeadTagLength]
	if len(plaintext) <= len(payload) {
		plaintext = payload[:len(plaintext)]
	}
	var nextAEAD *aeadState
	if bytes.Equal(c.peerAEAD.nonce[:], maximumNonce) {
		contextBytes := make([]byte, 0, len(peerHeader)+len(peerData))
		contextBytes = append(contextBytes, peerHeader[:]...)
		contextBytes = append(contextBytes, peerData...)
		nextAEAD = newAEAD(contextBytes, c.unitedKey, c.useAES)
	}
	if _, err := c.peerAEAD.Open(plaintext[:0], nil, peerData, peerHeader[:]); err != nil {
		return 0, E.Cause(err, "vless encryption: decrypt record")
	}
	if nextAEAD != nil {
		c.peerAEAD = nextAEAD
	}
	if len(plaintext) > len(payload) {
		copied := copy(payload, plaintext)
		c.input.Reset(plaintext[copied:])
		return copied, nil
	}
	return len(plaintext), nil
}

func (c *CommonConn) handlePeerHeaderError(err error) error {
	if c.client != nil && E.IsMulti(err, errInvalidHeader) {
		c.client.expireTicketIfCurrent(c.unitedKey)
		return E.Extend(err, "new handshake needed")
	}
	return err
}

func (c *CommonConn) Close() error                      { return common.Close(c.conn) }
func (c *CommonConn) LocalAddr() net.Addr               { return c.conn.LocalAddr() }
func (c *CommonConn) RemoteAddr() net.Addr              { return c.conn.RemoteAddr() }
func (c *CommonConn) SetDeadline(t time.Time) error     { return c.conn.SetDeadline(t) }
func (c *CommonConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
func (c *CommonConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
func (c *CommonConn) WriterReplaceable() bool { return false }
func (c *CommonConn) ReaderReplaceable() bool { return false }
func (c *CommonConn) Upstream() any           { return c.conn }

type aeadState struct {
	cipher.AEAD
	nonce [nonceLength]byte
}

func newAEAD(contextBytes, key []byte, useAES bool) *aeadState {
	subkey := make([]byte, chacha20poly1305.KeySize)
	blake3.DeriveKey(subkey, string(contextBytes), key)
	var instance cipher.AEAD
	if useAES {
		block, _ := aes.NewCipher(subkey)
		instance, _ = cipher.NewGCM(block)
	} else {
		instance, _ = chacha20poly1305.New(subkey)
	}
	clear(subkey)
	return &aeadState{AEAD: instance}
}

func (a *aeadState) Seal(dst, nonce, plaintext, additionalData []byte) []byte {
	if nonce == nil {
		nonce = increaseNonce(a.nonce[:])
	}
	return a.AEAD.Seal(dst, nonce, plaintext, additionalData)
}

func (a *aeadState) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if nonce == nil {
		nonce = increaseNonce(a.nonce[:])
	}
	return a.AEAD.Open(dst, nonce, ciphertext, additionalData)
}

func increaseNonce(nonce []byte) []byte {
	for index := range nonceLength {
		nonce[nonceLength-1-index]++
		if nonce[nonceLength-1-index] != 0 {
			break
		}
	}
	return nonce
}

var maximumNonce = bytes.Repeat([]byte{0xff}, nonceLength)

func encodeLength(length int) []byte {
	return []byte{byte(length >> 8), byte(length)}
}

func decodeLength(value []byte) int {
	return int(binary.BigEndian.Uint16(value))
}

func encodeHeader(header []byte, length int) {
	header[0] = 23
	header[1] = 3
	header[2] = 3
	binary.BigEndian.PutUint16(header[3:], uint16(length))
}

var errInvalidHeader = E.New("invalid encrypted record header")

func decodeHeader(header []byte) (int, error) {
	if len(header) < headerLength {
		return 0, io.ErrUnexpectedEOF
	}
	length := int(binary.BigEndian.Uint16(header[3:]))
	if header[0] != 23 || header[1] != 3 || header[2] != 3 || length < minPacketLength || length > maxPacketLength {
		return 0, E.Extend(errInvalidHeader, fmt.Sprint(header[:headerLength]))
	}
	return length, nil
}

func parsePadding(segments []string) (paddingLengths, paddingGaps [][3]int, err error) {
	if len(segments) == 0 {
		return nil, nil, nil
	}
	if len(segments) > maxPaddingSegments {
		return nil, nil, E.New("vless encryption: too many padding segments")
	}
	maxLength := 0
	maxGapTotal := time.Duration(0)
	for index, segment := range segments {
		parts := strings.Split(segment, "-")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return nil, nil, E.New("vless encryption: invalid padding length/gap parameter: ", segment)
		}
		values := [3]int{}
		for valueIndex := range values {
			values[valueIndex], err = strconv.Atoi(parts[valueIndex])
			if err != nil {
				return nil, nil, E.Cause(err, "vless encryption: parse padding parameter")
			}
		}
		if values[0] < 0 || values[0] > 100 || values[1] < 0 || values[2] < values[1] {
			return nil, nil, E.New("vless encryption: padding values are outside supported bounds: ", segment)
		}
		if index == 0 && (values[0] != 100 || values[1] < minPaddingLength || values[2] < minPaddingLength) {
			return nil, nil, E.New("vless encryption: first padding length must be unconditional and at least ", minPaddingLength)
		}
		if index%2 == 0 {
			if values[1] > maxTotalPadding || values[2] > maxTotalPadding || maxLength > maxTotalPadding-values[2] {
				return nil, nil, E.New("vless encryption: total padding length exceeds ", maxTotalPadding)
			}
			paddingLengths = append(paddingLengths, values)
			maxLength += values[2]
		} else {
			if values[1] > int(maxPaddingGap/time.Millisecond) || values[2] > int(maxPaddingGap/time.Millisecond) {
				return nil, nil, E.New("vless encryption: a padding gap exceeds ", maxPaddingGap)
			}
			maximumGap := time.Duration(values[2]) * time.Millisecond
			if maxGapTotal > maxPaddingGapTotal-maximumGap {
				return nil, nil, E.New("vless encryption: total padding gaps exceed ", maxPaddingGapTotal)
			}
			maxGapTotal += maximumGap
			paddingGaps = append(paddingGaps, values)
		}
	}
	if maxLength > maxTotalPadding {
		return nil, nil, E.New("vless encryption: total padding length exceeds ", maxTotalPadding)
	}
	if maxGapTotal > maxPaddingGapTotal {
		return nil, nil, E.New("vless encryption: total padding gaps exceed ", maxPaddingGapTotal)
	}
	return paddingLengths, paddingGaps, nil
}

func createPadding(paddingLengths, paddingGaps [][3]int) (length int, lengths []int, gaps []time.Duration) {
	if len(paddingLengths) == 0 {
		paddingLengths = [][3]int{{100, 111, 1111}, {50, 0, 3333}}
		paddingGaps = [][3]int{{75, 0, 111}}
	}
	for _, values := range paddingLengths {
		value := 0
		if values[0] >= randomBetween(0, 100) {
			value = randomBetween(values[1], values[2])
		}
		lengths = append(lengths, value)
		length += value
	}
	for _, values := range paddingGaps {
		value := 0
		if values[0] >= randomBetween(0, 100) {
			value = randomBetween(values[1], values[2])
		}
		gaps = append(gaps, time.Duration(value)*time.Millisecond)
	}
	return length, lengths, gaps
}

func randomBetween(from, to int) int {
	if from == to {
		return from
	}
	if to < from {
		from, to = to, from
	}
	return from + rand.IntN(to-from+1)
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if written > 0 {
			payload = payload[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
