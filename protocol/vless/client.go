package vless

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/protocol/vless/encryption"
	vmess "github.com/sagernet/sing-vmess"
	vlessProtocol "github.com/sagernet/sing-vmess/vless"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/gofrs/uuid/v5"
)

// protocolClient keeps the upstream sing-vmess implementation as the default
// path and only inserts the optional record layer when encryption is present.
// Vision must inspect that protected layer too: using the original transport
// would bypass authenticated records for encrypted VLESS connections.
type protocolClient struct {
	base       *vlessProtocol.Client
	key        [16]byte
	flow       string
	logger     logger.Logger
	encryption *encryption.Client
}

func newProtocolClient(ctx context.Context, userID, flow, encryptionConfig string, logger logger.Logger) (*protocolClient, error) {
	base, err := vlessProtocol.NewClient(userID, flow, logger)
	if err != nil {
		return nil, err
	}
	user, err := uuid.FromString(userID)
	if err != nil {
		user = uuid.NewV5(uuid.Nil, userID)
	}
	client := &protocolClient{
		base:   base,
		key:    user,
		flow:   flow,
		logger: logger,
	}
	switch encryptionConfig {
	case "", "none":
		return client, nil
	default:
		client.encryption, err = encryption.NewClient(ctx, encryptionConfig)
		if err != nil {
			return nil, E.Cause(err, "initialize VLESS encryption")
		}
		logger.Info("VLESS post-quantum encryption enabled")
		return client, nil
	}
}

func (c *protocolClient) encrypt(ctx context.Context, conn net.Conn) (net.Conn, error) {
	if c.encryption == nil {
		return conn, nil
	}
	encryptedConn, err := c.encryption.HandshakeContext(ctx, conn)
	if err != nil {
		common.Close(conn)
		return nil, E.Cause(err, "VLESS encryption handshake")
	}
	return encryptedConn, nil
}

func (c *protocolClient) prepareVision(conn, tlsConn net.Conn) (net.Conn, error) {
	if c.flow != vlessProtocol.FlowVision {
		return conn, nil
	}
	visionConn, err := vlessProtocol.NewVisionConn(conn, tlsConn, c.key, c.logger)
	if err != nil {
		common.Close(tlsConn)
		return nil, E.Cause(err, "initialize vision")
	}
	return visionConn, nil
}

type visionPreparer func(protocolConn, baseConn net.Conn) (net.Conn, error)

// prepareEncryptedVision keeps Vision entirely above the VLESS encryption
// record layer. The base connection must never point at the pre-encryption
// TLS/Reality or V2Ray transport, otherwise Vision can read around it.
func prepareEncryptedVision(prepare visionPreparer, protocolConn, encryptedConn net.Conn) (net.Conn, error) {
	return prepare(protocolConn, encryptedConn)
}

func (c *protocolClient) DialEarlyConn(ctx context.Context, conn net.Conn, destination M.Socksaddr) (net.Conn, error) {
	if c.encryption == nil {
		return c.base.DialEarlyConn(conn, destination)
	}
	encryptedConn, err := c.encrypt(ctx, conn)
	if err != nil {
		return nil, err
	}
	remoteConn := vlessProtocol.NewConn(encryptedConn, c.key, vmess.CommandTCP, destination, c.flow)
	return prepareEncryptedVision(c.prepareVision, remoteConn, encryptedConn)
}

func (c *protocolClient) DialEarlyPacketConn(ctx context.Context, conn net.Conn, destination M.Socksaddr) (*vlessProtocol.PacketConn, error) {
	if c.encryption == nil {
		return c.base.DialEarlyPacketConn(conn, destination)
	}
	encryptedConn, err := c.encrypt(ctx, conn)
	if err != nil {
		return nil, err
	}
	packetConn, err := c.base.DialEarlyPacketConn(encryptedConn, destination)
	if err != nil {
		common.Close(conn)
		return nil, err
	}
	return packetConn, nil
}

func (c *protocolClient) DialEarlyXUDPPacketConn(ctx context.Context, conn net.Conn, destination M.Socksaddr) (vmess.PacketConn, error) {
	if c.encryption == nil {
		return c.base.DialEarlyXUDPPacketConn(conn, destination)
	}
	encryptedConn, err := c.encrypt(ctx, conn)
	if err != nil {
		return nil, err
	}
	remoteConn := vlessProtocol.NewConn(encryptedConn, c.key, vmess.CommandMux, destination, c.flow)
	protocolConn, err := prepareEncryptedVision(c.prepareVision, remoteConn, encryptedConn)
	if err != nil {
		return nil, err
	}
	packetConn := vmess.NewXUDPConn(protocolConn, destination)
	if err := common.Error(remoteConn.Write(nil)); err != nil {
		common.Close(encryptedConn)
		return nil, err
	}
	return packetConn, nil
}
