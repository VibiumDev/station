package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// selfSigned loads or makes the station's own certificate for LAN mode:
// ten years, for every name and address the machine has now. A rig pins
// it after checking the station's key against the enrollment code; a
// browser is told to trust it once.
func selfSigned(dir string) (tls.Certificate, bool, error) {
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err == nil {
		return cert, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return cert, false, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return cert, false, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Vibium Station"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "station.local"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if host != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, host, host+".local")
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() {
				tmpl.IPAddresses = append(tmpl.IPAddresses, n.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return cert, false, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return cert, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return cert, false, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return cert, false, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return cert, false, err
	}
	cert, err = tls.LoadX509KeyPair(certPath, keyPath)
	return cert, true, err
}

// localIPv4 is an address on this machine for the LAN, for the URL rigs
// and browsers use.
func localIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.String()
		}
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}

// acme serves public mode on :443 with a Let's Encrypt certificate for
// host, kept in dir/acme, and answers the HTTP-01 challenge on :80, where
// everything else is redirected to HTTPS. httpAddr and httpsAddr may be
// changed for a machine that cannot bind the usual ports.
func acme(dir, host, httpAddr, httpsAddr string) (net.Listener, func(), error) {
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(host),
		Cache:      autocert.DirCache(filepath.Join(dir, "acme")),
	}
	plain, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("acme: listen %s for the HTTP-01 challenge: %w", httpAddr, err)
	}
	challenge := &http.Server{Handler: m.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}
	go challenge.Serve(plain)
	ln, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		plain.Close()
		return nil, nil, err
	}
	cfg := m.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	return tls.NewListener(ln, cfg), func() { challenge.Close(); plain.Close() }, nil
}
