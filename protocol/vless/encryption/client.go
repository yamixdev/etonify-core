package encryption

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/ntp"

	"lukechampine.com/blake3"
)

const (
	maximumEncryptionConfigLength = 16 * 1024
	maximumRelayCount             = 8
	handshakeTimeout              = 12 * time.Second
	pfsKeyExchangeLength          = encryptedLengthSize + mlkem.EncapsulationKeySize768 + x25519KeySize + aeadTagLength
	pfsKeyLength                  = hash256Length + hash256Length
	pfsPublicKeyLength            = mlkem.EncapsulationKeySize768 + x25519KeySize
	encryptedPFSPublicKeyLength   = mlkem.CiphertextSize768 + x25519KeySize + aeadTagLength
	encryptedTicketLength         = hash256Length
)

type xorMode uint8

const (
	xorModeNative xorMode = iota
	xorModePublic
	xorModeRandom
)

// Client implements the client side of Xray-compatible VLESS post-quantum
// encryption. The object is inert until explicitly configured on an outbound.
type Client struct {
	useAES          bool
	nfsPublicKeys   []any
	nfsPublicBytes  [][]byte
	nfsPublicHashes [][hash256Length]byte
	relaysLength    int
	xorMode         xorMode
	zeroRTT         bool
	paddingLengths  [][3]int
	paddingGaps     [][3]int
	timeFunc        func() time.Time

	ticketAccess sync.RWMutex
	expireAt     time.Time
	pfsKey       []byte
	ticket       []byte
}

func NewClient(ctx context.Context, rawConfig string) (*Client, error) {
	mode, zeroRTT, keyBytes, padding, err := parseEncryption(rawConfig)
	if err != nil {
		return nil, err
	}
	paddingLengths, paddingGaps, err := parsePadding(padding)
	if err != nil {
		return nil, err
	}
	client := &Client{
		useAES:          useAESFromContext(ctx),
		nfsPublicKeys:   make([]any, len(keyBytes)),
		nfsPublicBytes:  keyBytes,
		nfsPublicHashes: make([][hash256Length]byte, len(keyBytes)),
		xorMode:         mode,
		zeroRTT:         zeroRTT,
		paddingLengths:  paddingLengths,
		paddingGaps:     paddingGaps,
		timeFunc:        ntp.TimeFuncFromContext(ctx),
	}
	if client.timeFunc == nil {
		client.timeFunc = time.Now
	}
	for index, publicKeyBytes := range keyBytes {
		switch len(publicKeyBytes) {
		case x25519KeySize:
			publicKey, err := ecdh.X25519().NewPublicKey(publicKeyBytes)
			if err != nil {
				return nil, E.Cause(err, "vless encryption: parse X25519 public key ", index)
			}
			if publicKeyBytes[x25519KeySize-1] > 127 {
				return nil, E.New("vless encryption: invalid X25519 public key at ", index)
			}
			validationKey, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				return nil, E.Cause(err, "vless encryption: create X25519 validation key")
			}
			if _, err := validationKey.ECDH(publicKey); err != nil {
				return nil, E.Cause(err, "vless encryption: reject low-order X25519 public key ", index)
			}
			client.nfsPublicKeys[index] = publicKey
			client.relaysLength += x25519KeySize + hash256Length
		case mlkem.EncapsulationKeySize768:
			publicKey, err := mlkem.NewEncapsulationKey768(publicKeyBytes)
			if err != nil {
				return nil, E.Cause(err, "vless encryption: parse ML-KEM-768 public key ", index)
			}
			client.nfsPublicKeys[index] = publicKey
			client.relaysLength += mlkem.CiphertextSize768 + hash256Length
		default:
			return nil, E.New("vless encryption: invalid public key length ", len(publicKeyBytes), " at ", index)
		}
		client.nfsPublicHashes[index] = blake3.Sum256(publicKeyBytes)
	}
	client.relaysLength -= hash256Length
	return client, nil
}

func parseEncryption(rawConfig string) (mode xorMode, zeroRTT bool, publicKeys [][]byte, padding []string, err error) {
	if len(rawConfig) == 0 || len(rawConfig) > maximumEncryptionConfigLength {
		return 0, false, nil, nil, E.New("vless encryption: invalid config length")
	}
	parts := strings.Split(rawConfig, ".")
	if len(parts) < 4 || parts[0] != "mlkem768x25519plus" {
		return 0, false, nil, nil, E.New("vless encryption: unsupported encryption scheme")
	}
	switch parts[1] {
	case "native":
		mode = xorModeNative
	case "xorpub":
		mode = xorModePublic
	case "random":
		mode = xorModeRandom
	default:
		return 0, false, nil, nil, E.New("vless encryption: unsupported xor mode: ", parts[1])
	}
	switch parts[2] {
	case "1rtt":
	case "0rtt":
		zeroRTT = true
	default:
		return 0, false, nil, nil, E.New("vless encryption: unsupported handshake mode: ", parts[2])
	}

	keysStarted := false
	for index, part := range parts[3:] {
		if len(part) < 20 {
			if keysStarted {
				return 0, false, nil, nil, E.New("vless encryption: padding must precede public keys")
			}
			padding = append(padding, part)
			continue
		}
		keysStarted = true
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(part)
		if decodeErr != nil {
			return 0, false, nil, nil, E.Cause(decodeErr, "vless encryption: decode public key ", index)
		}
		if len(decoded) != x25519KeySize && len(decoded) != mlkem.EncapsulationKeySize768 {
			return 0, false, nil, nil, E.New("vless encryption: invalid public key length ", len(decoded), " at ", index)
		}
		if len(publicKeys) >= maximumRelayCount {
			return 0, false, nil, nil, E.New("vless encryption: relay count exceeds ", maximumRelayCount)
		}
		publicKeys = append(publicKeys, bytes.Clone(decoded))
	}
	if len(publicKeys) == 0 {
		return 0, false, nil, nil, E.New("vless encryption: no public keys")
	}
	return mode, zeroRTT, publicKeys, padding, nil
}

func (c *Client) HandshakeContext(ctx context.Context, conn net.Conn) (*CommonConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(handshakeTimeout)
	if contextDeadline, loaded := ctx.Deadline(); loaded && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	handshakeContext, cancelHandshake := context.WithDeadline(ctx, deadline)
	deadlineSupported := conn.SetDeadline(deadline) == nil
	interruptDone := make(chan struct{})
	interrupt := context.AfterFunc(handshakeContext, func() {
		if deadlineSupported {
			_ = conn.SetDeadline(time.Now())
		} else {
			_ = conn.Close()
		}
		close(interruptDone)
	})
	commonConn, err := c.handshake(handshakeContext, conn)
	if !interrupt() {
		<-interruptDone
	}
	contextErr := handshakeContext.Err()
	cancelHandshake()
	if deadlineSupported {
		_ = conn.SetDeadline(time.Time{})
	}
	if contextErr != nil {
		if commonConn != nil {
			_ = commonConn.Close()
		}
		return nil, contextErr
	}
	return commonConn, err
}

func (c *Client) handshake(ctx context.Context, conn net.Conn) (*CommonConn, error) {
	commonConn := newCommonConn(conn, c.useAES)
	ivAndRelaysLength := ivLength + c.relaysLength
	paddingLength, paddingLengths, paddingGaps := createPadding(c.paddingLengths, c.paddingGaps)
	clientHello := make([]byte, ivAndRelaysLength+pfsKeyExchangeLength+paddingLength)
	initializationVector := clientHello[:ivLength]
	if _, err := rand.Read(initializationVector); err != nil {
		return nil, E.Cause(err, "vless encryption: create IV")
	}

	relays := clientHello[ivLength:ivAndRelaysLength]
	var nfsKey []byte
	var previousCTR cipher.Stream
	for index, publicKey := range c.nfsPublicKeys {
		keyLength := x25519KeySize
		switch typedKey := publicKey.(type) {
		case *ecdh.PublicKey:
			privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				return nil, E.Cause(err, "vless encryption: generate X25519 key")
			}
			copy(relays, privateKey.PublicKey().Bytes())
			nfsKey, err = privateKey.ECDH(typedKey)
			if err != nil {
				return nil, E.Cause(err, "vless encryption: X25519 exchange")
			}
		case *mlkem.EncapsulationKey768:
			var ciphertext []byte
			nfsKey, ciphertext = typedKey.Encapsulate()
			copy(relays, ciphertext)
			keyLength = mlkem.CiphertextSize768
		default:
			return nil, E.New("vless encryption: invalid public key state")
		}
		if c.xorMode != xorModeNative {
			newCTR(c.nfsPublicBytes[index], initializationVector).XORKeyStream(relays, relays[:keyLength])
		}
		if previousCTR != nil {
			previousCTR.XORKeyStream(relays, relays[:hash256Length])
		}
		if index == len(c.nfsPublicKeys)-1 {
			break
		}
		previousCTR = newCTR(nfsKey, initializationVector)
		previousCTR.XORKeyStream(relays[keyLength:], c.nfsPublicHashes[index+1][:])
		relays = relays[keyLength+hash256Length:]
	}
	nfsAEAD := newAEAD(initializationVector, nfsKey, commonConn.useAES)

	if c.zeroRTT {
		c.ticketAccess.RLock()
		if c.timeFunc().Before(c.expireAt) && len(c.pfsKey) == pfsKeyLength && len(c.ticket) == ticketLength {
			pfsKey := bytes.Clone(c.pfsKey)
			ticket := bytes.Clone(c.ticket)
			c.ticketAccess.RUnlock()
			commonConn.client = c
			commonConn.unitedKey = joinKeys(pfsKey, nfsKey)
			nfsAEAD.Seal(clientHello[:ivAndRelaysLength], nil, encodeLength(encryptedTicketLength), nil)
			nfsAEAD.Seal(clientHello[:ivAndRelaysLength+encryptedLengthSize], nil, ticket, nil)
			preWriteEnd := ivAndRelaysLength + encryptedLengthSize + encryptedTicketLength
			commonConn.preWrite = bytes.Clone(clientHello[:preWriteEnd])
			commonConn.aead = newAEAD(clientHello[ivAndRelaysLength+encryptedLengthSize:preWriteEnd], commonConn.unitedKey, commonConn.useAES)
			if c.xorMode == xorModeRandom {
				commonConn.conn = newXORConn(conn, newCTR(commonConn.unitedKey, initializationVector), nil, len(commonConn.preWrite), ivLength)
			}
			return commonConn, nil
		}
		c.ticketAccess.RUnlock()
	}

	pfsKeyExchange := clientHello[ivAndRelaysLength : ivAndRelaysLength+pfsKeyExchangeLength]
	nfsAEAD.Seal(pfsKeyExchange[:0], nil, encodeLength(pfsKeyExchangeLength-encryptedLengthSize), nil)
	mlkemPrivateKey, err := mlkem.GenerateKey768()
	if err != nil {
		return nil, E.Cause(err, "vless encryption: generate ML-KEM-768 key")
	}
	x25519PrivateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, E.Cause(err, "vless encryption: generate PFS X25519 key")
	}
	pfsPublicKey := make([]byte, pfsPublicKeyLength)
	copy(pfsPublicKey, mlkemPrivateKey.EncapsulationKey().Bytes())
	copy(pfsPublicKey[mlkem.EncapsulationKeySize768:], x25519PrivateKey.PublicKey().Bytes())
	nfsAEAD.Seal(pfsKeyExchange[:encryptedLengthSize], nil, pfsPublicKey, nil)

	padding := clientHello[ivAndRelaysLength+pfsKeyExchangeLength:]
	nfsAEAD.Seal(padding[:0], nil, encodeLength(paddingLength-encryptedLengthSize), nil)
	nfsAEAD.Seal(padding[:encryptedLengthSize], nil, padding[encryptedLengthSize:paddingLength-aeadTagLength], nil)
	paddingLengths[0] += ivAndRelaysLength + pfsKeyExchangeLength
	remainingHello := clientHello
	for index, length := range paddingLengths {
		if length > 0 {
			if length > len(remainingHello) {
				return nil, E.New("vless encryption: invalid generated padding length")
			}
			if err := writeAll(conn, remainingHello[:length]); err != nil {
				return nil, err
			}
			remainingHello = remainingHello[length:]
		}
		if len(paddingGaps) > index {
			if err := waitContext(ctx, paddingGaps[index]); err != nil {
				return nil, err
			}
		}
	}
	if len(remainingHello) != 0 {
		return nil, E.New("vless encryption: generated padding did not consume client hello")
	}

	encryptedPeerPFSKey := make([]byte, encryptedPFSPublicKeyLength)
	if _, err := io.ReadFull(conn, encryptedPeerPFSKey); err != nil {
		return nil, err
	}
	if _, err := nfsAEAD.Open(encryptedPeerPFSKey[:0], maximumNonce, encryptedPeerPFSKey, nil); err != nil {
		return nil, E.Cause(err, "vless encryption: decrypt peer PFS key")
	}
	mlkemKey, err := mlkemPrivateKey.Decapsulate(encryptedPeerPFSKey[:mlkem.CiphertextSize768])
	if err != nil {
		return nil, E.Cause(err, "vless encryption: decapsulate peer PFS key")
	}
	peerX25519PublicKey, err := ecdh.X25519().NewPublicKey(encryptedPeerPFSKey[mlkem.CiphertextSize768 : mlkem.CiphertextSize768+x25519KeySize])
	if err != nil {
		return nil, E.Cause(err, "vless encryption: parse peer PFS X25519 key")
	}
	x25519Key, err := x25519PrivateKey.ECDH(peerX25519PublicKey)
	if err != nil {
		return nil, E.Cause(err, "vless encryption: exchange peer PFS X25519 key")
	}
	pfsKey := make([]byte, pfsKeyLength)
	copy(pfsKey, mlkemKey)
	copy(pfsKey[hash256Length:], x25519Key)
	commonConn.unitedKey = joinKeys(pfsKey, nfsKey)
	commonConn.aead = newAEAD(pfsPublicKey, commonConn.unitedKey, commonConn.useAES)
	commonConn.peerAEAD = newAEAD(encryptedPeerPFSKey[:mlkem.CiphertextSize768+x25519KeySize], commonConn.unitedKey, commonConn.useAES)

	encryptedTicket := make([]byte, encryptedTicketLength)
	if _, err := io.ReadFull(conn, encryptedTicket); err != nil {
		return nil, err
	}
	if _, err := commonConn.peerAEAD.Open(encryptedTicket[:0], nil, encryptedTicket, nil); err != nil {
		return nil, E.Cause(err, "vless encryption: decrypt session ticket")
	}
	seconds := decodeLength(encryptedTicket)
	if c.zeroRTT && seconds > 0 {
		c.ticketAccess.Lock()
		c.expireAt = c.timeFunc().Add(time.Duration(seconds) * time.Second)
		c.pfsKey = bytes.Clone(pfsKey)
		c.ticket = bytes.Clone(encryptedTicket[:ticketLength])
		c.ticketAccess.Unlock()
	}

	encryptedPaddingLength := make([]byte, encryptedLengthSize)
	if _, err := io.ReadFull(conn, encryptedPaddingLength); err != nil {
		return nil, err
	}
	if _, err := commonConn.peerAEAD.Open(encryptedPaddingLength[:0], nil, encryptedPaddingLength, nil); err != nil {
		return nil, E.Cause(err, "vless encryption: decrypt peer padding length")
	}
	peerPaddingLength := decodeLength(encryptedPaddingLength[:2])
	commonConn.peerPadding = make([]byte, peerPaddingLength)
	if c.xorMode == xorModeRandom {
		commonConn.conn = newXORConn(
			conn,
			newCTR(commonConn.unitedKey, initializationVector),
			newCTR(commonConn.unitedKey, encryptedTicket[:ticketLength]),
			0,
			peerPaddingLength,
		)
	}
	return commonConn, nil
}

func (c *Client) expireTicketIfCurrent(unitedKey []byte) {
	c.ticketAccess.Lock()
	if len(c.pfsKey) > 0 && bytes.HasPrefix(unitedKey, c.pfsKey) {
		c.expireAt = c.timeFunc()
	}
	c.ticketAccess.Unlock()
}

func joinKeys(first, second []byte) []byte {
	joined := make([]byte, len(first)+len(second))
	copy(joined, first)
	copy(joined[len(first):], second)
	return joined
}

func waitContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
