package steward

import (
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestNotificationRoutingEscalatesWithoutDuplicatingLowPriority(t *testing.T) {
	endpoints := []notificationEndpointRecord{
		{StewardNotificationEndpoint: endpoint("system")},
		{StewardNotificationEndpoint: endpoint("ntfy")},
		{StewardNotificationEndpoint: endpoint("email")},
	}
	low := routeNotificationEndpoints(endpoints, "low", false, nil)
	if len(low) != 1 || low[0].Endpoint.Channel != "system" || low[0].Delay != 0 {
		t.Fatalf("low-priority routes = %+v", low)
	}
	normal := routeNotificationEndpoints(endpoints, "normal", false, nil)
	if len(normal) != 3 || normal[0].Endpoint.Channel != "system" || normal[1].Delay != 10*time.Minute || normal[2].Delay != time.Hour {
		t.Fatalf("normal routes = %+v", normal)
	}
	urgent := routeNotificationEndpoints(endpoints, "urgent", false, nil)
	for _, route := range urgent {
		if route.Delay != 0 {
			t.Fatalf("urgent route has delay: %+v", route)
		}
	}
}

func TestNotificationRoutingHonorsExplicitChannel(t *testing.T) {
	endpoints := []notificationEndpointRecord{
		{StewardNotificationEndpoint: endpoint("system")},
		{StewardNotificationEndpoint: endpoint("ntfy")},
	}
	routes := routeNotificationEndpoints(endpoints, "normal", true, map[string]bool{"ntfy": true})
	if len(routes) != 1 || routes[0].Endpoint.Channel != "ntfy" || routes[0].Delay != 0 {
		t.Fatalf("explicit routes = %+v", routes)
	}
}

func TestNotificationRoutingUsesCrossDeviceOrEmailWhenNoDesktopSessionExists(t *testing.T) {
	ntfyAndEmail := []notificationEndpointRecord{
		{StewardNotificationEndpoint: endpoint("ntfy")},
		{StewardNotificationEndpoint: endpoint("email")},
	}
	low := routeNotificationEndpoints(ntfyAndEmail, "low", false, nil)
	if len(low) != 1 || low[0].Endpoint.Channel != "ntfy" || low[0].Delay != 0 {
		t.Fatalf("headless low-priority routes = %+v", low)
	}
	normal := routeNotificationEndpoints(ntfyAndEmail, "normal", false, nil)
	if len(normal) != 2 || normal[0].Endpoint.Channel != "ntfy" || normal[0].Delay != 0 || normal[1].Delay != time.Hour {
		t.Fatalf("headless normal routes = %+v", normal)
	}
	emailOnly := routeNotificationEndpoints([]notificationEndpointRecord{{StewardNotificationEndpoint: endpoint("email")}}, "low", false, nil)
	if len(emailOnly) != 1 || emailOnly[0].Endpoint.Channel != "email" || emailOnly[0].Delay != 0 {
		t.Fatalf("email-only routes = %+v", emailOnly)
	}
}

func endpoint(channel string) domain.StewardNotificationEndpoint {
	return domain.StewardNotificationEndpoint{ID: channel, Channel: channel, Name: channel, Enabled: true}
}
