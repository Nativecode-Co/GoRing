package notification

import "context"

// Notification type constants
const (
	NotifTypeIncomingCall  = "incoming_call"
	NotifTypeCallCancelled = "call_cancelled"
)

// CallNotification holds all data needed to send a call push notification.
type CallNotification struct {
	DeviceToken    string // FCM registration token or APNs device token
	OS             string // "android" or "ios"
	SessionID      string
	CallerID       string
	CallerName     string
	CallerUsername string
	CallerImage    string
	Type           string // "incoming_call" or "call_cancelled"; empty defaults to "incoming_call"
}

// EffectiveType returns the notification type, defaulting to "incoming_call" for backward compatibility.
func (n CallNotification) EffectiveType() string {
	if n.Type == "" {
		return NotifTypeIncomingCall
	}
	return n.Type
}

// Service is the interface implemented by all push notification backends.
// SendCallNotification must be safe to call concurrently.
type Service interface {
	SendCallNotification(ctx context.Context, notif CallNotification) error
}

// NoopService silently discards all notifications.
// Used when no push notification credentials are configured.
type NoopService struct{}

func (n *NoopService) SendCallNotification(_ context.Context, _ CallNotification) error {
	return nil
}

// RoutingNotifier dispatches to FCM or APNs based on the notification OS field.
// Either backend may be nil, in which case that platform is silently skipped.
type RoutingNotifier struct {
	FCM  Service // used when OS == "android"
	APNs Service // used when OS == "ios"
}

func (r *RoutingNotifier) SendCallNotification(ctx context.Context, notif CallNotification) error {
	switch notif.OS {
	case "android":
		if r.FCM != nil {
			return r.FCM.SendCallNotification(ctx, notif)
		}
	case "ios":
		if r.APNs != nil {
			return r.APNs.SendCallNotification(ctx, notif)
		}
	}
	return nil
}
