package tls

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"slices"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
)

// VerifyCertificateSHA256 verifies peer certificates against a set of pinned SHA-256 certificate hashes.
// It supports both leaf certificate pinning (trusted directly) and intermediate/root CA certificate pinning
// (verifies the leaf against the pinned CA and the target server name).
func VerifyCertificateSHA256(knownHashes [][]byte, rawCerts [][]byte, serverName string, roots *x509.CertPool) error {
	if len(rawCerts) == 0 {
		return E.New("empty peer certificates")
	}

	certs := make([]*x509.Certificate, len(rawCerts))
	for i, asn1Data := range rawCerts {
		cert, err := x509.ParseCertificate(asn1Data)
		if err != nil {
			return E.Cause(err, "failed to parse peer certificate [", i, "]")
		}
		certs[i] = cert
	}

	// Reverse if the first certificate is a CA and the last is not (handles reversed server chains).
	if certs[0].IsCA && len(certs) > 1 && !certs[len(certs)-1].IsCA {
		slices.Reverse(certs)
	}

	// 1. Direct leaf match:
	// If the leaf certificate sent by the server matches any pinned hash,
	// trust it directly without requiring a public CA.
	leafHash := sha256.Sum256(certs[0].Raw)
	for _, known := range knownHashes {
		if hmac.Equal(leafHash[:], known) {
			return nil
		}
	}

	// 2. Intermediate or Root CA match:
	// If an intermediate or root certificate in the chain matches a pinned hash,
	// use it as a pinned trust anchor to verify the leaf certificate.
	if len(certs) > 1 {
		for _, cert := range certs[1:] {
			certHash := sha256.Sum256(cert.Raw)
			for _, known := range knownHashes {
				if hmac.Equal(certHash[:], known) {
					pinnedRoots := x509.NewCertPool()
					pinnedRoots.AddCert(cert)

					intermediates := x509.NewCertPool()
					for _, c := range certs[1:] {
						intermediates.AddCert(c)
					}

					opts := x509.VerifyOptions{
						Roots:         pinnedRoots,
						Intermediates: intermediates,
						CurrentTime:   time.Now(),
					}
					if serverName != "" {
						opts.DNSName = serverName
					}

					if _, err := certs[0].Verify(opts); err != nil {
						return E.Cause(err, "peer certificate verification failed against pinned CA")
					}
					return nil
				}
			}
		}
	}

	return E.New("peer certificate does not match any pinned sha256 hash (leaf sha256: ", hex.EncodeToString(leafHash[:]), ")")
}
