package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/crypto/pkcs12"
)

const (
	apnsProductionHost = "https://api.push.apple.com"
	apnsSandboxHost    = "https://api.sandbox.push.apple.com"
)

// APNsConfig holds Apple Push Notification service credentials (certificate-based auth).
type APNsConfig struct {
	CertPath   string // Path to the .p12 VoIP certificate file
	Passphrase string // Certificate passphrase
	BundleID   string // App bundle ID; apns-topic will be "<bundle_id>.voip"
	Production bool   // true = production APNs endpoint, false = sandbox
}

// APNsNotifier sends VoIP push notifications via Apple Push Notification service.
// Uses HTTP/2 with TLS client certificate authentication (.p12).
type APNsNotifier struct {
	cfg        APNsConfig
	httpClient *http.Client
	logger     zerolog.Logger
}

// NewAPNsNotifier creates a new APNs notifier by loading and parsing the .p12 certificate.
func NewAPNsNotifier(cfg APNsConfig, logger zerolog.Logger) (*APNsNotifier, error) {
	p12Data, err := os.ReadFile(cfg.CertPath)
	if err != nil {
		return nil, fmt.Errorf("apns: read certificate %q: %w", cfg.CertPath, err)
	}

	tlsCert, err := loadP12(p12Data, cfg.Passphrase)
	if err != nil {
		return nil, fmt.Errorf("apns: load certificate: %w", err)
	}

	// ForceAttemptHTTP2 is required when providing a custom TLSClientConfig,
	// otherwise Go's http.Transport won't negotiate HTTP/2, which APNs mandates.
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{Certificates: []tls.Certificate{tlsCert}},
		ForceAttemptHTTP2: true,
	}

	return &APNsNotifier{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second, Transport: transport},
		logger:     logger.With().Str("component", "apns_notifier").Logger(),
	}, nil
}

// loadP12 decodes a PKCS#12 (.p12) file and returns a tls.Certificate.
func loadP12(p12Data []byte, passphrase string) (tls.Certificate, error) {
	privateKey, cert, err := pkcs12.Decode(p12Data, passphrase)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("decode p12: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{cert.Raw},
		PrivateKey:  privateKey,
		Leaf:        cert,
	}, nil
}

type apnsPayload struct {
	APS            apnsAPS `json:"aps"`
	Type           string  `json:"type"`
	SessionID      string  `json:"session_id"`
	CallerID       string  `json:"caller_id"`
	CallerName     string  `json:"callerName"`
	Handle         string  `json:"handle"`
	CallerUsername string  `json:"caller_username,omitempty"`
	CallerImage    string  `json:"caller_image,omitempty"`
}

type apnsAPS struct {
	Alert            apnsAlert `json:"alert"`
	ContentAvailable int       `json:"content-available"`
}

type apnsAlert struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// SendCallNotification sends a VoIP push notification to an iOS device.
// The apns-push-type: voip header causes APNs to wake the app via PushKit,
// allowing it to display a native CallKit incoming call UI.
func (a *APNsNotifier) SendCallNotification(ctx context.Context, notif CallNotification) error {
	displayName := notif.CallerName
	if displayName == "" {
		displayName = notif.CallerUsername
	}
	if displayName == "" {
		displayName = "Unknown"
	}

	payload := apnsPayload{
		APS: apnsAPS{
			Alert: apnsAlert{
				Title: "Incoming Call",
				Body:  displayName + " is calling...",
			},
			ContentAvailable: 1,
		},
		Type:           "incoming_call",
		SessionID:      notif.SessionID,
		CallerID:       notif.CallerID,
		CallerName:     notif.CallerName,
		Handle:         displayName,
		CallerUsername: notif.CallerUsername,
		CallerImage:    notif.CallerImage,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("apns: marshal payload: %w", err)
	}

	host := apnsSandboxHost
	if a.cfg.Production {
		host = apnsProductionHost
	}
	url := fmt.Sprintf("%s/3/device/%s", host, notif.DeviceToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("apns: create request: %w", err)
	}

	// TLS client certificate handles authentication — no Authorization header needed.
	req.Header.Set("apns-topic", a.cfg.BundleID+".voip")
	req.Header.Set("apns-push-type", "voip")
	req.Header.Set("content-type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("apns: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apnsErr struct {
			Reason    string `json:"reason"`
			Timestamp int64  `json:"timestamp"`
		}
		json.NewDecoder(resp.Body).Decode(&apnsErr) //nolint:errcheck
		return fmt.Errorf("apns: HTTP %d: %s", resp.StatusCode, apnsErr.Reason)
	}

	a.logger.Info().
		Str("session_id", notif.SessionID).
		Str("caller_id", notif.CallerID).
		Msg("APNs push notification sent")

	return nil
}