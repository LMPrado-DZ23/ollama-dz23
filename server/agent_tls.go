package server

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func configureAgentTLS(server *http.Server) (bool, error) {
	certFile := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_TLS_KEY_FILE"))
	requireMTLS := os.Getenv("OLLAMA_AGENT_REQUIRE_MTLS") == "1"
	if certFile == "" || keyFile == "" {
		if requireMTLS {
			return false, errors.New("mTLS requires OLLAMA_AGENT_TLS_CERT_FILE and OLLAMA_AGENT_TLS_KEY_FILE")
		}
		return false, nil
	}
	load := func() (tls.Certificate, error) {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}
	certificate, err := load()
	if err != nil {
		return false, fmt.Errorf("load agent TLS certificate: %w", err)
	}
	config := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}
	config.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		fresh, err := load()
		if err != nil {
			return nil, err
		}
		return &fresh, nil
	}
	if requireMTLS {
		caFile := strings.TrimSpace(os.Getenv("OLLAMA_AGENT_TLS_CLIENT_CA_FILE"))
		if caFile == "" {
			return false, errors.New("mTLS requires OLLAMA_AGENT_TLS_CLIENT_CA_FILE")
		}
		caData, err := os.ReadFile(caFile)
		if err != nil {
			return false, fmt.Errorf("read agent mTLS client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caData) {
			return false, errors.New("agent mTLS client CA is not valid PEM")
		}
		config.ClientCAs = pool
		config.ClientAuth = tls.RequireAndVerifyClientCert
	}
	server.TLSConfig = config
	return true, nil
}
