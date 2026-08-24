package vless

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing/common/logger"
)

const testUserID = "9f1c6db7-2c40-4c62-9db5-65ce35f50f2f"

func TestProtocolClientEncryptionIsOptIn(t *testing.T) {
	for _, config := range []string{"", "none"} {
		client, err := newProtocolClient(context.Background(), testUserID, "", config, logger.NOP())
		if err != nil {
			t.Fatal(err)
		}
		if client.encryption != nil {
			t.Fatalf("encryption initialized for %q", config)
		}
	}
}

func TestPrepareEncryptedVisionUsesEncryptionLayerAsBase(t *testing.T) {
	protocolConn, protocolPeer := net.Pipe()
	encryptedConn, encryptedPeer := net.Pipe()
	t.Cleanup(func() {
		_ = protocolConn.Close()
		_ = protocolPeer.Close()
		_ = encryptedConn.Close()
		_ = encryptedPeer.Close()
	})

	called := false
	prepared, err := prepareEncryptedVision(
		func(gotProtocolConn, gotBaseConn net.Conn) (net.Conn, error) {
			called = true
			if gotProtocolConn != protocolConn {
				t.Fatal("Vision protocol connection is not the VLESS connection")
			}
			if gotBaseConn != encryptedConn {
				t.Fatal("Vision base connection bypasses the encryption layer")
			}
			return gotProtocolConn, nil
		},
		protocolConn,
		encryptedConn,
	)
	requireNoError(t, err)
	if !called {
		t.Fatal("Vision preparer was not called")
	}
	if prepared != protocolConn {
		t.Fatal("unexpected prepared connection")
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestProtocolClientRejectsInvalidEncryption(t *testing.T) {
	if _, err := newProtocolClient(context.Background(), testUserID, "", "invalid", logger.NOP()); err == nil {
		t.Fatal("expected invalid encryption error")
	}
}
