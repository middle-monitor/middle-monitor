package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
)

// BasicAuth is the username/password pair presented to a target.
type BasicAuth struct {
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	PasswordFile string `yaml:"password_file"`
}

// TLSConfig tunes the TLS handshake with a target. Internal exporters are
// routinely served behind a self-signed or a private-CA certificate.
type TLSConfig struct {
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	CAFile             string `yaml:"ca_file"`
	CertFile           string `yaml:"cert_file"`
	KeyFile            string `yaml:"key_file"`
	ServerName         string `yaml:"server_name"`
}

// clientFor builds the HTTP client of one target. Targets never share a client:
// TLS settings are per target, and so is the timeout.
func clientFor(cfg ScrapeConfig, target ScrapeTarget) (*http.Client, error) {
	client := &http.Client{Timeout: cfg.timeoutFor(target)}
	if target.TLSConfig == nil {
		return client, nil
	}

	tlsConfig, err := target.TLSConfig.build()
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	client.Transport = transport
	return client, nil
}

func (t *TLSConfig) build() (*tls.Config, error) {
	out := &tls.Config{
		InsecureSkipVerify: t.InsecureSkipVerify,
		ServerName:         t.ServerName,
	}

	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errTLSCAInvalid
		}
		out.RootCAs = pool
	}

	// A client certificate needs both halves; one alone is a config mistake
	// that would otherwise show up as an opaque handshake failure.
	if (t.CertFile == "") != (t.KeyFile == "") {
		return nil, errTLSKeyPairIncomplete
	}
	if t.CertFile != "" {
		pair, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, err
		}
		out.Certificates = []tls.Certificate{pair}
	}
	return out, nil
}

// applyAuth adds the credentials of a target to one request. Files are read at
// every scrape so a rotated token is picked up without restarting the agent.
func applyAuth(req *http.Request, target ScrapeTarget) error {
	for key, value := range target.Headers {
		req.Header.Set(key, expandEnv(value))
	}

	if target.BasicAuth != nil {
		password := expandEnv(target.BasicAuth.Password)
		if target.BasicAuth.PasswordFile != "" {
			read, err := readSecretFile(target.BasicAuth.PasswordFile)
			if err != nil {
				return err
			}
			password = read
		}
		req.SetBasicAuth(expandEnv(target.BasicAuth.Username), password)
	}

	token := expandEnv(target.BearerToken)
	if target.BearerTokenFile != "" {
		read, err := readSecretFile(target.BearerTokenFile)
		if err != nil {
			return err
		}
		token = read
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// A token file written by a config-management tool almost always ends with
	// a newline, which would be sent as part of the credential.
	return strings.TrimSpace(string(data)), nil
}

// expandEnv resolves ${VAR} in a credential so a systemd unit can carry the
// secret in its environment instead of leaving it in a readable config file.
// Only credential fields go through it: a metric value is never expanded.
func expandEnv(value string) string {
	if !strings.Contains(value, "${") {
		return value
	}
	return os.ExpandEnv(value)
}
