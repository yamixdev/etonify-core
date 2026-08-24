package encryption

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
)

type referenceSession struct {
	pfsKey []byte
}

type referenceServer struct {
	privateKey any
	publicKey  []byte
	relaySize  int
	mode       xorMode
	useAES     bool
	allow0RTT  bool

	access       sync.Mutex
	sessions     map[[ticketLength]byte]*referenceSession
	zeroRTTCount atomic.Int32
}

func newReferenceServer(t testing.TB, mode xorMode, useAES, allow0RTT bool) *referenceServer {
	t.Helper()
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &referenceServer{
		privateKey: privateKey,
		publicKey:  privateKey.PublicKey().Bytes(),
		relaySize:  x25519KeySize,
		mode:       mode,
		useAES:     useAES,
		allow0RTT:  allow0RTT,
		sessions:   make(map[[ticketLength]byte]*referenceSession),
	}
}

func newMLKEMReferenceServer(t testing.TB, mode xorMode, useAES bool) *referenceServer {
	t.Helper()
	privateKey, err := mlkem.GenerateKey768()
	if err != nil {
		t.Fatal(err)
	}
	return &referenceServer{
		privateKey: privateKey,
		publicKey:  privateKey.EncapsulationKey().Bytes(),
		relaySize:  mlkem.CiphertextSize768,
		mode:       mode,
		useAES:     useAES,
		sessions:   make(map[[ticketLength]byte]*referenceSession),
	}
}

func (s *referenceServer) config() string {
	mode := "native"
	switch s.mode {
	case xorModePublic:
		mode = "xorpub"
	case xorModeRandom:
		mode = "random"
	}
	handshakeMode := "1rtt"
	if s.allow0RTT {
		handshakeMode = "0rtt"
	}
	return "mlkem768x25519plus." + mode + "." + handshakeMode + ".100-35-35." + base64.RawURLEncoding.EncodeToString(s.publicKey)
}

func (s *referenceServer) handshake(conn net.Conn) (*CommonConn, error) {
	commonConn := newCommonConn(conn, s.useAES)
	ivAndRelay := make([]byte, ivLength+s.relaySize)
	if _, err := io.ReadFull(conn, ivAndRelay); err != nil {
		return nil, err
	}
	initializationVector := ivAndRelay[:ivLength]
	relay := ivAndRelay[ivLength:]
	if s.mode != xorModeNative {
		newCTR(s.publicKey, initializationVector).XORKeyStream(relay, relay)
	}
	var (
		nfsKey []byte
		err    error
	)
	switch privateKey := s.privateKey.(type) {
	case *ecdh.PrivateKey:
		peerPublicKey, parseErr := ecdh.X25519().NewPublicKey(relay)
		if parseErr != nil {
			return nil, parseErr
		}
		nfsKey, err = privateKey.ECDH(peerPublicKey)
		if err != nil {
			return nil, err
		}
	case *mlkem.DecapsulationKey768:
		nfsKey, err = privateKey.Decapsulate(relay)
		if err != nil {
			return nil, err
		}
	default:
		return nil, io.ErrUnexpectedEOF
	}
	nfsAEAD := newAEAD(initializationVector, nfsKey, s.useAES)

	encryptedLength := make([]byte, encryptedLengthSize)
	if _, err := io.ReadFull(conn, encryptedLength); err != nil {
		return nil, err
	}
	decryptedLength, err := nfsAEAD.Open(nil, nil, encryptedLength, nil)
	if err != nil {
		return nil, err
	}
	length := decodeLength(decryptedLength)
	if length == encryptedTicketLength {
		return s.handshakeZeroRTT(conn, commonConn, nfsAEAD, nfsKey, initializationVector)
	}
	return s.handshakeOneRTT(conn, commonConn, nfsAEAD, nfsKey, initializationVector, length)
}

func (s *referenceServer) handshakeZeroRTT(
	conn net.Conn,
	commonConn *CommonConn,
	nfsAEAD *aeadState,
	nfsKey, initializationVector []byte,
) (*CommonConn, error) {
	if !s.allow0RTT {
		return nil, io.ErrUnexpectedEOF
	}
	encryptedTicket := make([]byte, encryptedTicketLength)
	if _, err := io.ReadFull(conn, encryptedTicket); err != nil {
		return nil, err
	}
	ticketBytes, err := nfsAEAD.Open(nil, nil, encryptedTicket, nil)
	if err != nil {
		return nil, err
	}
	var ticket [ticketLength]byte
	copy(ticket[:], ticketBytes)
	s.access.Lock()
	session := s.sessions[ticket]
	s.access.Unlock()
	if session == nil {
		return nil, io.ErrUnexpectedEOF
	}
	commonConn.unitedKey = joinKeys(session.pfsKey, nfsKey)
	commonConn.preWrite = make([]byte, ivLength)
	if _, err := rand.Read(commonConn.preWrite); err != nil {
		return nil, err
	}
	commonConn.aead = newAEAD(commonConn.preWrite, commonConn.unitedKey, commonConn.useAES)
	commonConn.peerAEAD = newAEAD(encryptedTicket, commonConn.unitedKey, commonConn.useAES)
	if s.mode == xorModeRandom {
		commonConn.conn = newXORConn(
			conn,
			newCTR(commonConn.unitedKey, commonConn.preWrite),
			newCTR(commonConn.unitedKey, initializationVector),
			ivLength,
			0,
		)
	}
	s.zeroRTTCount.Add(1)
	return commonConn, nil
}

func (s *referenceServer) handshakeOneRTT(
	conn net.Conn,
	commonConn *CommonConn,
	nfsAEAD *aeadState,
	nfsKey, initializationVector []byte,
	length int,
) (*CommonConn, error) {
	if length < pfsPublicKeyLength+aeadTagLength || length > pfsPublicKeyLength+aeadTagLength+4096 {
		return nil, io.ErrUnexpectedEOF
	}
	encryptedClientPFSKey := make([]byte, length)
	if _, err := io.ReadFull(conn, encryptedClientPFSKey); err != nil {
		return nil, err
	}
	clientPFSKey, err := nfsAEAD.Open(nil, nil, encryptedClientPFSKey, nil)
	if err != nil {
		return nil, err
	}
	if len(clientPFSKey) < pfsPublicKeyLength {
		return nil, io.ErrUnexpectedEOF
	}
	clientMLKEMKey, err := mlkem.NewEncapsulationKey768(clientPFSKey[:mlkem.EncapsulationKeySize768])
	if err != nil {
		return nil, err
	}
	mlkemKey, mlkemCiphertext := clientMLKEMKey.Encapsulate()
	clientX25519Key, err := ecdh.X25519().NewPublicKey(clientPFSKey[mlkem.EncapsulationKeySize768:pfsPublicKeyLength])
	if err != nil {
		return nil, err
	}
	serverX25519Key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	x25519Key, err := serverX25519Key.ECDH(clientX25519Key)
	if err != nil {
		return nil, err
	}
	pfsKey := make([]byte, pfsKeyLength)
	copy(pfsKey, mlkemKey)
	copy(pfsKey[hash256Length:], x25519Key)
	serverPFSKey := make([]byte, mlkem.CiphertextSize768+x25519KeySize)
	copy(serverPFSKey, mlkemCiphertext)
	copy(serverPFSKey[mlkem.CiphertextSize768:], serverX25519Key.PublicKey().Bytes())
	commonConn.unitedKey = joinKeys(pfsKey, nfsKey)
	commonConn.aead = newAEAD(serverPFSKey, commonConn.unitedKey, commonConn.useAES)
	commonConn.peerAEAD = newAEAD(clientPFSKey[:pfsPublicKeyLength], commonConn.unitedKey, commonConn.useAES)

	// Read the already-sent client padding before replying. This ordering keeps
	// net.Pipe tests deadlock-free while remaining valid for a real TCP peer.
	clientPaddingLengthCiphertext := make([]byte, encryptedLengthSize)
	if _, err := io.ReadFull(conn, clientPaddingLengthCiphertext); err != nil {
		return nil, err
	}
	clientPaddingLengthBytes, err := nfsAEAD.Open(nil, nil, clientPaddingLengthCiphertext, nil)
	if err != nil {
		return nil, err
	}
	clientPadding := make([]byte, decodeLength(clientPaddingLengthBytes))
	if _, err := io.ReadFull(conn, clientPadding); err != nil {
		return nil, err
	}
	if _, err := nfsAEAD.Open(nil, nil, clientPadding, nil); err != nil {
		return nil, err
	}

	var ticket [ticketLength]byte
	if _, err := rand.Read(ticket[:]); err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint16(ticket[:2], 60)
	if s.allow0RTT {
		s.access.Lock()
		s.sessions[ticket] = &referenceSession{pfsKey: bytes.Clone(pfsKey)}
		s.access.Unlock()
	}
	const serverPaddingLength = minPaddingLength
	serverHello := nfsAEAD.Seal(nil, maximumNonce, serverPFSKey, nil)
	serverHello = commonConn.aead.Seal(serverHello, nil, ticket[:], nil)
	serverHello = commonConn.aead.Seal(serverHello, nil, encodeLength(serverPaddingLength-encryptedLengthSize), nil)
	serverHello = commonConn.aead.Seal(serverHello, nil, make([]byte, serverPaddingLength-encryptedLengthSize-aeadTagLength), nil)
	if err := writeAll(conn, serverHello); err != nil {
		return nil, err
	}
	if s.mode == xorModeRandom {
		commonConn.conn = newXORConn(
			conn,
			newCTR(commonConn.unitedKey, ticket[:]),
			newCTR(commonConn.unitedKey, initializationVector),
			0,
			0,
		)
	}
	return commonConn, nil
}

func TestEncryptedRoundTrip(t *testing.T) {
	for _, useAES := range []bool{false, true} {
		for _, mode := range []xorMode{xorModeNative, xorModePublic, xorModeRandom} {
			name := map[xorMode]string{xorModeNative: "native", xorModePublic: "xorpub", xorModeRandom: "random"}[mode]
			if useAES {
				name += "/aes"
			} else {
				name += "/chacha20"
			}
			t.Run(name, func(t *testing.T) {
				server := newReferenceServer(t, mode, useAES, false)
				ctx := OverrideUseAES(context.Background(), useAES)
				client, err := NewClient(ctx, server.config())
				if err != nil {
					t.Fatal(err)
				}
				payload := bytes.Repeat([]byte("etonify"), 3000)
				roundTrip(t, ctx, client, server, payload)
			})
		}
	}
}

func TestZeroRTTTicketRoundTrip(t *testing.T) {
	for _, mode := range []xorMode{xorModeNative, xorModeRandom} {
		t.Run(map[xorMode]string{xorModeNative: "native", xorModeRandom: "random"}[mode], func(t *testing.T) {
			server := newReferenceServer(t, mode, false, true)
			ctx := OverrideUseAES(context.Background(), false)
			client, err := NewClient(ctx, server.config())
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, ctx, client, server, []byte("first handshake"))
			roundTrip(t, ctx, client, server, []byte("cached handshake"))
			if count := server.zeroRTTCount.Load(); count != 1 {
				t.Fatalf("zero RTT count = %d, want 1", count)
			}
		})
	}
}

func TestMLKEMRelayRoundTrip(t *testing.T) {
	for _, mode := range []xorMode{xorModeNative, xorModePublic, xorModeRandom} {
		t.Run(map[xorMode]string{xorModeNative: "native", xorModePublic: "xorpub", xorModeRandom: "random"}[mode], func(t *testing.T) {
			server := newMLKEMReferenceServer(t, mode, false)
			ctx := OverrideUseAES(context.Background(), false)
			client, err := NewClient(ctx, server.config())
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, ctx, client, server, []byte("ML-KEM relay"))
		})
	}
}

func TestHandshakeWithoutNativeDeadlineSupport(t *testing.T) {
	server := newReferenceServer(t, xorModeNative, false, false)
	ctx := OverrideUseAES(context.Background(), false)
	client, err := NewClient(ctx, server.config())
	if err != nil {
		t.Fatal(err)
	}
	roundTripWithConnection(t, ctx, client, server, []byte("stream transport"), true)
}

func TestHandshakeRejectsCorruptedServerHello(t *testing.T) {
	server := newReferenceServer(t, xorModeNative, false, false)
	ctx := OverrideUseAES(context.Background(), false)
	client, err := NewClient(ctx, server.config())
	if err != nil {
		t.Fatal(err)
	}
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	serverDone := make(chan error, 1)
	go func() {
		_, err := server.handshake(serverSide)
		serverDone <- err
	}()
	if _, err := client.HandshakeContext(ctx, &tamperReadConn{Conn: clientSide}); err == nil {
		t.Fatal("expected authenticated handshake failure")
	}
	_ = clientSide.Close()
	<-serverDone
}

type tamperReadConn struct {
	net.Conn
	once sync.Once
}

func (c *tamperReadConn) Read(payload []byte) (int, error) {
	read, err := c.Conn.Read(payload)
	if read > 0 {
		c.once.Do(func() { payload[0] ^= 0xff })
	}
	return read, err
}

func roundTrip(t testing.TB, ctx context.Context, client *Client, server *referenceServer, payload []byte) {
	roundTripWithConnection(t, ctx, client, server, payload, false)
}

func roundTripWithConnection(t testing.TB, ctx context.Context, client *Client, server *referenceServer, payload []byte, unsupportedDeadlines bool) {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	serverResult := make(chan error, 1)
	go func() {
		serverConn, err := server.handshake(serverSide)
		if err != nil {
			serverResult <- err
			return
		}
		input := make([]byte, len(payload))
		if _, err := io.ReadFull(serverConn, input); err != nil {
			serverResult <- err
			return
		}
		if !bytes.Equal(input, payload) {
			serverResult <- io.ErrUnexpectedEOF
			return
		}
		_, err = serverConn.Write(input)
		serverResult <- err
	}()
	var handshakeConn net.Conn = clientSide
	if unsupportedDeadlines {
		handshakeConn = unsupportedDeadlineConn{clientSide}
	}
	clientConn, err := client.HandshakeContext(ctx, handshakeConn)
	if err != nil {
		t.Fatal(err)
	}
	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))
	_ = serverSide.SetDeadline(time.Now().Add(5 * time.Second))
	writeResult := make(chan error, 1)
	go func() {
		if written, err := clientConn.Write(payload); err != nil {
			writeResult <- err
		} else if written != len(payload) {
			writeResult <- io.ErrShortWrite
		} else {
			writeResult <- nil
		}
	}()
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(clientConn, response); err != nil {
		t.Fatal(err)
	}
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, payload) {
		t.Fatal("round-trip payload differs")
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestEncryptionConfigValidation(t *testing.T) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	valid := "mlkem768x25519plus.native.1rtt.100-35-35." + key
	if _, err := NewClient(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	hugeInteger := strconv.FormatInt(1<<62, 10)
	zeroKey := base64.RawURLEncoding.EncodeToString(make([]byte, x25519KeySize))
	highBitKeyBytes := bytes.Clone(privateKey.PublicKey().Bytes())
	highBitKeyBytes[x25519KeySize-1] |= 0x80
	highBitKey := base64.RawURLEncoding.EncodeToString(highBitKeyBytes)
	invalid := map[string]string{
		"scheme":            "unknown.native.1rtt." + key,
		"mode":              "mlkem768x25519plus.bad.1rtt." + key,
		"handshake":         "mlkem768x25519plus.native.bad." + key,
		"missing key":       "mlkem768x25519plus.native.1rtt.100-35-35",
		"bad first padding": "mlkem768x25519plus.native.1rtt.99-35-35." + key,
		"gap too large":     "mlkem768x25519plus.native.1rtt.100-35-35.100-0-5001.100-0-0." + key,
		"length overflow":   "mlkem768x25519plus.native.1rtt.100-35-" + hugeInteger + "." + key,
		"gap overflow":      "mlkem768x25519plus.native.1rtt.100-35-35.100-0-" + hugeInteger + ".100-0-0." + key,
		"padding after key": valid + ".100-35-35",
		"low order key":     "mlkem768x25519plus.native.1rtt." + zeroKey,
		"high bit key":      "mlkem768x25519plus.native.1rtt." + highBitKey,
		"oversized":         strings.Repeat("x", maximumEncryptionConfigLength+1),
	}
	for name, config := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := NewClient(context.Background(), config); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestHandshakeCancellationInterruptsPaddingGap(t *testing.T) {
	for _, deadlinesSupported := range []bool{true, false} {
		name := "deadlines"
		if !deadlinesSupported {
			name = "close-fallback"
		}
		t.Run(name, func(t *testing.T) {
			server := newReferenceServer(t, xorModeNative, false, false)
			config := "mlkem768x25519plus.native.1rtt.100-35-35.100-5000-5000.100-0-0." + base64.RawURLEncoding.EncodeToString(server.publicKey)
			client, err := NewClient(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			clientSide, serverSide := net.Pipe()
			defer clientSide.Close()
			defer serverSide.Close()
			go io.Copy(io.Discard, serverSide)
			var handshakeConn net.Conn = clientSide
			if !deadlinesSupported {
				handshakeConn = unsupportedDeadlineConn{clientSide}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			if _, err := client.HandshakeContext(ctx, handshakeConn); err == nil {
				t.Fatal("expected cancellation error")
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("cancellation took %s", elapsed)
			}
		})
	}
}

type unsupportedDeadlineConn struct{ net.Conn }

func (unsupportedDeadlineConn) SetDeadline(time.Time) error     { return os.ErrInvalid }
func (unsupportedDeadlineConn) SetReadDeadline(time.Time) error { return os.ErrInvalid }
func (unsupportedDeadlineConn) SetWriteDeadline(time.Time) error {
	return os.ErrInvalid
}

func TestConcurrentZeroRTTHandshakes(t *testing.T) {
	server := newReferenceServer(t, xorModeNative, false, true)
	ctx := OverrideUseAES(context.Background(), false)
	client, err := NewClient(ctx, server.config())
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, ctx, client, server, []byte("establish ticket"))
	const parallel = 8
	var waitGroup sync.WaitGroup
	errors := make(chan error, parallel)
	for index := 0; index < parallel; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			clientSide, serverSide := net.Pipe()
			defer clientSide.Close()
			defer serverSide.Close()
			serverDone := make(chan error, 1)
			go func() {
				serverConn, err := server.handshake(serverSide)
				if err == nil {
					buffer := make([]byte, 1)
					_, err = io.ReadFull(serverConn, buffer)
				}
				serverDone <- err
			}()
			clientConn, err := client.HandshakeContext(ctx, clientSide)
			if err == nil {
				_, err = clientConn.Write([]byte{1})
			}
			if err == nil {
				err = <-serverDone
			}
			if err != nil {
				errors <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

func TestWrappedXORHeaderErrorExpiresZeroRTTTicket(t *testing.T) {
	server := newReferenceServer(t, xorModeRandom, false, true)
	client, err := NewClient(context.Background(), server.config())
	if err != nil {
		t.Fatal(err)
	}
	pfsKey := bytes.Repeat([]byte{1}, pfsKeyLength)
	client.ticketAccess.Lock()
	client.pfsKey = bytes.Clone(pfsKey)
	client.expireAt = time.Now().Add(time.Hour)
	client.ticketAccess.Unlock()
	connection := &CommonConn{client: client, unitedKey: joinKeys(pfsKey, []byte{2})}
	if err := connection.handlePeerHeaderError(E.Cause(errInvalidHeader, "xor record")); err == nil {
		t.Fatal("expected retry error")
	}
	client.ticketAccess.RLock()
	expireAt := client.expireAt
	client.ticketAccess.RUnlock()
	if expireAt.After(time.Now()) {
		t.Fatal("ticket was not expired")
	}
}

func BenchmarkEncryptedRoundTrip(b *testing.B) {
	server := newReferenceServer(b, xorModeNative, false, false)
	ctx := OverrideUseAES(context.Background(), false)
	client, err := NewClient(ctx, server.config())
	if err != nil {
		b.Fatal(err)
	}
	payload := bytes.Repeat([]byte{1}, dataChunkSize)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		roundTrip(b, ctx, client, server, payload)
	}
}
