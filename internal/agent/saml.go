package agent

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

type SAMLProviderConfig struct {
	Name               string
	EntityID           string
	IDPMetadataURL     string
	MetadataURL        string
	ACSURL             string
	SPPrivateKeyFile   string
	SPCertificateFile  string
	DefaultRedirectURI string
	AllowIDPInitiated  bool
}

type SAMLService struct {
	Provider SAMLProviderConfig
	SP       saml.ServiceProvider
	Client   *http.Client
	mu       sync.Mutex
	requests map[string]trackedSAMLRequest
}

type trackedSAMLRequest struct {
	RequestID string
	Target    string
	ExpiresAt time.Time
}

func NewSAMLService(ctx context.Context, config SAMLProviderConfig, client *http.Client) (*SAMLService, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if strings.TrimSpace(config.Name) == "" {
		return nil, errors.New("saml provider name is required")
	}
	metadataURL, err := requireHTTPSURL(config.IDPMetadataURL, "idp metadata URL")
	if err != nil {
		return nil, err
	}
	metadata, err := samlsp.FetchMetadata(ctx, client, metadataURL)
	if err != nil {
		return nil, fmt.Errorf("fetch SAML IdP metadata: %w", err)
	}
	entityID, err := requireHTTPSURL(config.MetadataURL, "SP metadata URL")
	if err != nil {
		return nil, err
	}
	acsURL, err := requireHTTPSURL(config.ACSURL, "SAML ACS URL")
	if err != nil {
		return nil, err
	}
	keyData, err := os.ReadFile(config.SPPrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read SAML SP private key: %w", err)
	}
	certificateData, err := os.ReadFile(config.SPCertificateFile)
	if err != nil {
		return nil, fmt.Errorf("read SAML SP certificate: %w", err)
	}
	key, err := parseRSAKey(keyData)
	if err != nil {
		return nil, err
	}
	certificate, err := parseCertificate(certificateData)
	if err != nil {
		return nil, err
	}
	return &SAMLService{
		Provider: config,
		Client:   client,
		SP: saml.ServiceProvider{
			EntityID:              strings.TrimSpace(config.EntityID),
			Key:                   key,
			Certificate:           certificate,
			HTTPClient:            client,
			MetadataURL:           entityID,
			AcsURL:                acsURL,
			IDPMetadata:           metadata,
			AuthnNameIDFormat:     saml.EmailAddressNameIDFormat,
			MetadataValidDuration: 24 * time.Hour,
			AllowIDPInitiated:     config.AllowIDPInitiated,
			DefaultRedirectURI:    config.DefaultRedirectURI,
			SignatureMethod:       "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256",
		},
		requests: make(map[string]trackedSAMLRequest),
	}, nil
}

func (s *SAMLService) Start(target string) (string, string, error) {
	if s == nil {
		return "", "", errors.New("saml service is unavailable")
	}
	if strings.TrimSpace(target) == "" {
		target = s.Provider.DefaultRedirectURI
	}
	if !validRedirectTarget(target) {
		return "", "", errors.New("saml redirect target must be relative or HTTPS")
	}
	bindingLocation := s.SP.GetSSOBindingLocation(saml.HTTPRedirectBinding)
	request, err := s.SP.MakeAuthenticationRequest(bindingLocation, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return "", "", err
	}
	relay, err := randomSecret(24)
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	s.requests[relay] = trackedSAMLRequest{RequestID: request.ID, Target: target, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	s.mu.Unlock()
	redirect, err := request.Redirect(relay, &s.SP)
	if err != nil {
		return "", "", err
	}
	return redirect.String(), relay, nil
}

func (s *SAMLService) Metadata(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.Error(w, "saml service is unavailable", http.StatusNotImplemented)
		return
	}
	middleware := samlsp.Middleware{ServiceProvider: s.SP}
	middleware.ServeMetadata(w, r)
}

func (s *SAMLService) ParseACS(r *http.Request) (map[string]any, error) {
	if s == nil {
		return nil, errors.New("saml service is unavailable")
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	relay := strings.TrimSpace(r.FormValue("RelayState"))
	s.mu.Lock()
	tracked, ok := s.requests[relay]
	if ok {
		delete(s.requests, relay)
	}
	s.mu.Unlock()
	possibleIDs := []string{}
	if ok && time.Now().UTC().Before(tracked.ExpiresAt) {
		possibleIDs = append(possibleIDs, tracked.RequestID)
	}
	if s.SP.AllowIDPInitiated {
		possibleIDs = append(possibleIDs, "")
	}
	assertion, err := s.SP.ParseResponse(r, possibleIDs)
	if err != nil {
		return nil, err
	}
	claims := assertionClaims(assertion)
	if ok && tracked.Target != "" {
		claims["redirect_uri"] = tracked.Target
	}
	return claims, nil
}

func assertionClaims(assertion *saml.Assertion) map[string]any {
	claims := map[string]any{"issuer": assertion.Issuer.Value, "attributes": map[string][]string{}}
	attributes := claims["attributes"].(map[string][]string)
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		claims["sub"] = assertion.Subject.NameID.Value
	}
	for _, statement := range assertion.AttributeStatements {
		for _, attribute := range statement.Attributes {
			name := strings.TrimSpace(attribute.Name)
			if name == "" {
				name = strings.TrimSpace(attribute.FriendlyName)
			}
			if name == "" {
				continue
			}
			for _, value := range attribute.Values {
				attributes[name] = append(attributes[name], strings.TrimSpace(value.Value))
			}
		}
	}
	for name, values := range attributes {
		key := strings.ToLower(name)
		if len(values) == 0 {
			continue
		}
		switch key {
		case "email", "mail", "emailaddress", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress":
			claims["email"] = values[0]
		case "displayname", "name", "cn", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name":
			claims["name"] = values[0]
		case "group", "groups", "memberof":
			claims["groups"] = values
		}
	}
	return claims
}

func parseRSAKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("SAML SP private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse SAML SP private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("SAML SP private key is not RSA")
	}
	return rsaKey, nil
}

func parseCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("SAML SP certificate is not PEM encoded")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse SAML SP certificate: %w", err)
	}
	return certificate, nil
}

func requireHTTPSURL(raw, label string) (url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return url.URL{}, fmt.Errorf("%s must be an HTTPS URL", label)
	}
	return *parsed, nil
}

func validRedirectTarget(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil {
		return false
	}
	return parsed.IsAbs() && parsed.Scheme == "https" && parsed.Host != "" || !parsed.IsAbs() && strings.HasPrefix(parsed.Path, "/")
}
