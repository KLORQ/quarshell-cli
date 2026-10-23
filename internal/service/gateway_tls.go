package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type gatewayTLSFiles struct {
	ServerCert string
	ServerKey  string
	CA         string
	ClientCert string
	ClientKey  string
}

// ensureGatewayTLS provisions a local CA, server certificate and client
// certificate for mutual TLS. Existing files are reused.
func ensureGatewayTLS(dataDir string) (gatewayTLSFiles, error) {
	dir := filepath.Join(dataDir, "tls")
	files := gatewayTLSFiles{
		ServerCert: filepath.Join(dir, "server.crt"),
		ServerKey:  filepath.Join(dir, "server.key"),
		CA:         filepath.Join(dir, "ca.crt"),
		ClientCert: filepath.Join(dir, "client", "client.crt"),
		ClientKey:  filepath.Join(dir, "client", "client.key"),
	}
	if completeTLSFiles(files) {
		return files, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "client"), 0o700); err != nil {
		return gatewayTLSFiles{}, err
	}
	caKey, caCert, caPEM, err := newLocalCA()
	if err != nil {
		return gatewayTLSFiles{}, err
	}
	serverCertPEM, serverKeyPEM, err := signedCert(caCert, caKey, "quarshell-local-gateway", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, true)
	if err != nil {
		return gatewayTLSFiles{}, err
	}
	clientCertPEM, clientKeyPEM, err := signedCert(caCert, caKey, "quarshell-local-client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, false)
	if err != nil {
		return gatewayTLSFiles{}, err
	}
	for path, data := range map[string][]byte{
		files.CA: caPEM, files.ServerCert: serverCertPEM, files.ServerKey: serverKeyPEM,
		files.ClientCert: clientCertPEM, files.ClientKey: clientKeyPEM,
	} {
		if err := writePrivateFile(path, data); err != nil {
			return gatewayTLSFiles{}, err
		}
	}
	return files, nil
}

func completeTLSFiles(files gatewayTLSFiles) bool {
	for _, path := range []string{files.ServerCert, files.ServerKey, files.CA, files.ClientCert, files.ClientKey} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func newLocalCA() (*ecdsa.PrivateKey, *x509.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "quarshell-local-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return key, cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func signedCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string, usage []x509.ExtKeyUsage, server bool) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  usage,
	}
	if server {
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tls-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
