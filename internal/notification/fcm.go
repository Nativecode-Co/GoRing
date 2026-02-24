package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/oauth2/google"
)

const (
	fcmScope    = "https://www.googleapis.com/auth/firebase.messaging"
	fcmEndpoint = "https://fcm.googleapis.com/v1/projects/%s/messages:send"
)

// FCMConfig holds credentials for Firebase Cloud Messaging.
type FCMConfig struct {
	ServiceAccountJSON []byte // Full contents of the service account .json file
	ProjectID          string // Firebase project ID (e.g., "my-firebase-project")
}

// FCMNotifier sends push notifications via Firebase Cloud Messaging v1 API.
type FCMNotifier struct {
	projectID string
	creds     *google.Credentials
	httpClient *http.Client
	logger    zerolog.Logger
}

// NewFCMNotifier creates a new FCM notifier from a service account JSON file.
func NewFCMNotifier(cfg FCMConfig, logger zerolog.Logger) (*FCMNotifier, error) {
	creds, err := google.CredentialsFromJSON(context.Background(), cfg.ServiceAccountJSON, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse service account: %w", err)
	}

	return &FCMNotifier{
		projectID:  cfg.ProjectID,
		creds:      creds,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger.With().Str("component", "fcm_notifier").Logger(),
	}, nil
}

type fcmMessage struct {
	Message fcmMessageBody `json:"message"`
}

type fcmMessageBody struct {
	Token   string            `json:"token"`
	Data    map[string]string `json:"data"`
	Android fcmAndroidConfig  `json:"android"`
}

type fcmAndroidConfig struct {
	Priority string `json:"priority"` // "high" wakes the device from Doze mode
}

// SendCallNotification sends a data-only FCM push notification to an Android device.
// The notification is handled in the background by the mobile app (no visible banner).
func (f *FCMNotifier) SendCallNotification(ctx context.Context, notif CallNotification) error {
	payload := fcmMessage{
		Message: fcmMessageBody{
			Token: notif.DeviceToken,
			Data: map[string]string{
				"type":            "incoming_call",
				"session_id":      notif.SessionID,
				"caller_id":       notif.CallerID,
				"caller_name":     notif.CallerName,
				"caller_username": notif.CallerUsername,
				"caller_image":    notif.CallerImage,
			},
			Android: fcmAndroidConfig{Priority: "high"},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("fcm: marshal payload: %w", err)
	}

	endpoint := fmt.Sprintf(fcmEndpoint, f.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fcm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Fetch OAuth2 token (cached and refreshed automatically by TokenSource)
	token, err := f.creds.TokenSource.Token()
	if err != nil {
		return fmt.Errorf("fcm: get oauth2 token: %w", err)
	}
	token.SetAuthHeader(req)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fcm: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var fcmErr struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&fcmErr) //nolint:errcheck
		return fmt.Errorf("fcm: HTTP %d: %s", resp.StatusCode, fcmErr.Error.Message)
	}

	f.logger.Info().
		Str("session_id", notif.SessionID).
		Str("caller_id", notif.CallerID).
		Msg("FCM push notification sent")

	return nil
}
