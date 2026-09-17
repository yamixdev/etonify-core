package tls_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	json "github.com/sagernet/sing/common/json"
	"github.com/stretchr/testify/require"
)

func generateTestCert(t *testing.T, isCA bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, dnsNames ...string) (*x509.Certificate, *ecdsa.PrivateKey) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Etonify Test"},
			CommonName:   "Etonify Test Cert",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour * 24),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		DNSNames:              dnsNames,
	}

	signerCert := template
	signerKey := key
	if parent != nil {
		signerCert = parent
		signerKey = parentKey
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, signerCert, &key.PublicKey, signerKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certDER)
	require.NoError(t, err)

	return cert, key
}

func TestVerifyCertificateSHA256_Leaf(t *testing.T) {
	leafCert, _ := generateTestCert(t, false, nil, nil, "example.com")
	leafHash := sha256.Sum256(leafCert.Raw)

	// 1. Exact match on leaf cert
	err := boxTLS.VerifyCertificateSHA256([][]byte{leafHash[:]}, [][]byte{leafCert.Raw}, "example.com", nil)
	require.NoError(t, err)

	// 2. Exact match even with different serverName (leaf cert pin directly trusts the certificate)
	err = boxTLS.VerifyCertificateSHA256([][]byte{leafHash[:]}, [][]byte{leafCert.Raw}, "other.com", nil)
	require.NoError(t, err)

	// 3. Mismatch on leaf cert
	wrongHash := sha256.Sum256([]byte("wrong"))
	err = boxTLS.VerifyCertificateSHA256([][]byte{wrongHash[:]}, [][]byte{leafCert.Raw}, "example.com", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "peer certificate does not match any pinned sha256 hash")
}

func TestVerifyCertificateSHA256_CAChain(t *testing.T) {
	caCert, caKey := generateTestCert(t, true, nil, nil)
	leafCert, _ := generateTestCert(t, false, caCert, caKey, "vpn.etonify.app")

	caHash := sha256.Sum256(caCert.Raw)

	rawCerts := [][]byte{leafCert.Raw, caCert.Raw}

	// 1. CA matches and serverName matches
	err := boxTLS.VerifyCertificateSHA256([][]byte{caHash[:]}, rawCerts, "vpn.etonify.app", nil)
	require.NoError(t, err)

	// 2. CA matches but serverName does not match
	err = boxTLS.VerifyCertificateSHA256([][]byte{caHash[:]}, rawCerts, "wrong.etonify.app", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "peer certificate verification failed against pinned CA")

	// 3. CA does not match
	wrongHash := sha256.Sum256([]byte("wrong-ca"))
	err = boxTLS.VerifyCertificateSHA256([][]byte{wrongHash[:]}, rawCerts, "vpn.etonify.app", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "peer certificate does not match any pinned sha256 hash")
}

func TestVerifyCertificateSHA256_ReversedChain(t *testing.T) {
	caCert, caKey := generateTestCert(t, true, nil, nil)
	leafCert, _ := generateTestCert(t, false, caCert, caKey, "vpn.etonify.app")

	caHash := sha256.Sum256(caCert.Raw)

	// Reversed order: CA first, then leaf
	reversedCerts := [][]byte{caCert.Raw, leafCert.Raw}

	err := boxTLS.VerifyCertificateSHA256([][]byte{caHash[:]}, reversedCerts, "vpn.etonify.app", nil)
	require.NoError(t, err)
}

func TestCertificateHash_JSONUnmarshal(t *testing.T) {
	raw32 := make([]byte, 32)
	for i := range raw32 {
		raw32[i] = byte(i + 1)
	}
	hexWithColons := "01:02:03:04:05:06:07:08:09:0a:0b:0c:0d:0e:0f:10:11:12:13:14:15:16:17:18:19:1a:1b:1c:1d:1e:1f:20"
	plainHex := hex.EncodeToString(raw32)

	ctx := context.Background()

	// 1. Single hex string with colons
	json1 := `{"certificate_sha256": "` + hexWithColons + `"}`
	var opts1 option.OutboundTLSOptions
	err := json.UnmarshalContext(ctx, []byte(json1), &opts1)
	require.NoError(t, err)
	require.Len(t, opts1.CertificateSHA256, 1)
	require.Equal(t, raw32, []byte(opts1.CertificateSHA256[0]))

	// 2. Plain hex string
	json2 := `{"certificate_sha256": "` + plainHex + `"}`
	var opts2 option.OutboundTLSOptions
	err = json.UnmarshalContext(ctx, []byte(json2), &opts2)
	require.NoError(t, err)
	require.Len(t, opts2.CertificateSHA256, 1)
	require.Equal(t, raw32, []byte(opts2.CertificateSHA256[0]))

	// 3. Array of strings (hex and base64)
	json3 := `{"certificate_sha256": ["` + hexWithColons + `", "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="]}`
	var opts3 option.OutboundTLSOptions
	err = json.UnmarshalContext(ctx, []byte(json3), &opts3)
	require.NoError(t, err)
	require.Len(t, opts3.CertificateSHA256, 2)
	require.Equal(t, raw32, []byte(opts3.CertificateSHA256[0]))
	require.Equal(t, raw32, []byte(opts3.CertificateSHA256[1]))
}
