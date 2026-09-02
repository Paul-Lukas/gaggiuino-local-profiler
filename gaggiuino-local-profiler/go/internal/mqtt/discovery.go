package mqtt

import "context"

// This file ports lib/mqtt-discovery.js (#608): Supervisor MQTT service
// auto-discovery via the Supervisor's own /services/mqtt endpoint (a
// different root than the Core API — lib/constants.js's SUPERVISOR_API,
// reached through ha.Client.SupervisorGet). Requires `services: [mqtt:want]`
// in config.yaml. An install with no MQTT service registered gets a 4xx
// here, treated the same as "not available" (manual entry is always a valid
// fallback) rather than an error.

// Broker is the discovered broker connection (nil when nothing is registered).
type Broker struct {
	Host     string
	Port     int
	Username string
	Password string
}

// DiscoverSupervisorMQTT ports discoverSupervisorMqtt(): returns nil on any
// failure (no token, unreachable Supervisor, no MQTT service), matching the
// Node original's `catch -> null` and `if (!d || !d.host) return null`.
func DiscoverSupervisorMQTT(ctx context.Context, ha SupervisorAPI) *Broker {
	if ha == nil {
		return nil
	}
	var resp struct {
		Data struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"data"`
	}
	if err := ha.SupervisorGet(ctx, "/services/mqtt", &resp); err != nil {
		return nil
	}
	if resp.Data.Host == "" {
		return nil
	}
	port := resp.Data.Port
	if port == 0 {
		port = 1883
	}
	return &Broker{Host: resp.Data.Host, Port: port, Username: resp.Data.Username, Password: resp.Data.Password}
}
