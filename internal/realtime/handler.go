package realtime

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

const (
	// jwtTimeout is how long a client has to send a valid JWT after connecting.
	jwtTimeout = 5 * time.Second
	// writeTimeout is the maximum time to write a single message to the client.
	writeTimeout = 10 * time.Second
	// sendBufferSize is the number of queued messages per subscriber.
	sendBufferSize = 64
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// clientMessage is the JSON structure expected from WebSocket clients.
type clientMessage struct {
	Event   string          `json:"event"`
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload,omitempty"`
	// For auth event:
	Token string `json:"token,omitempty"`
}

// Handler returns an http.Handler for the /realtime/v1/ endpoint.
// It upgrades incoming requests to WebSocket and handles:
//   - "auth" event: client sends JWT; verified before routing begins
//   - "subscribe" event: client subscribes to a Postgres NOTIFY channel
//   - "broadcast" event: client sends a message to all subscribers of a topic
//   - "presence:join" event: client joins the presence set of a topic
//   - "presence:leave" event: client leaves the presence set
//
// jwtSecret is the GoTrue JWT secret used to verify access tokens.
// hub routes notifications to subscribers. presenceTrackers is a map of
// channel → *PresenceTracker (created lazily per channel in the calling code).
func Handler(hub *Hub, jwtSecret string, presenceTrackers map[string]*PresenceTracker, mu interface{ Lock(); Unlock() }) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logrus.WithError(err).Debug("realtime: websocket upgrade failed")
			return
		}
		defer conn.Close() //nolint:errcheck

		// Require JWT auth within jwtTimeout.
		conn.SetReadDeadline(time.Now().Add(jwtTimeout)) //nolint:errcheck

		var claims jwt.MapClaims
		for {
			var msg clientMessage
			if err := conn.ReadJSON(&msg); err != nil {
				logrus.WithError(err).Debug("realtime: read auth message failed")
				return
			}

			if msg.Event == "auth" {
				tok, err := jwt.Parse(msg.Token,
					func(t *jwt.Token) (interface{}, error) { return []byte(jwtSecret), nil },
					jwt.WithValidMethods([]string{"HS256"}),
				)
				if err != nil || !tok.Valid {
					conn.WriteJSON(map[string]string{"event": "error", "message": "invalid token"}) //nolint:errcheck
					return
				}
				claims = tok.Claims.(jwt.MapClaims)
				conn.WriteJSON(map[string]string{"event": "auth_ok"}) //nolint:errcheck
				conn.SetReadDeadline(time.Time{})                     //nolint:errcheck
				break
			}
		}

		role, _ := claims["role"].(string)
		if role == "" {
			role = "anon"
		}

		sub := &Subscriber{
			send: make(chan []byte, sendBufferSize),
		}

		// Pump outbound messages to WebSocket.
		go func() {
			for msg := range sub.send {
				conn.SetWriteDeadline(time.Now().Add(writeTimeout)) //nolint:errcheck
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			}
		}()

		// Handle inbound messages.
		for {
			var msg clientMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}

			switch msg.Event {
			case "subscribe":
				hub.Subscribe(sub, msg.Topic)
				conn.WriteJSON(map[string]string{"event": "subscribed", "topic": msg.Topic}) //nolint:errcheck

			case "unsubscribe":
				hub.Unsubscribe(sub)

			case "broadcast":
				b, _ := json.Marshal(map[string]interface{}{
					"event":   "broadcast",
					"topic":   msg.Topic,
					"payload": msg.Payload,
					"role":    role,
				})
				hub.Broadcast(msg.Topic, b)

			case "presence:join":
				var state PresenceState
				json.Unmarshal(msg.Payload, &state) //nolint:errcheck
				pt := getOrCreatePresenceTracker(presenceTrackers, mu, msg.Topic, hub)
				pt.Join(role, state)

			case "presence:leave":
				pt := getOrCreatePresenceTracker(presenceTrackers, mu, msg.Topic, hub)
				pt.Leave(role)
			}
		}

		hub.Unsubscribe(sub)
		close(sub.send)
	})
}

func getOrCreatePresenceTracker(m map[string]*PresenceTracker, mu interface{ Lock(); Unlock() }, channel string, hub *Hub) *PresenceTracker {
	mu.Lock()
	defer mu.Unlock()
	if pt, ok := m[channel]; ok {
		return pt
	}
	pt := NewPresenceTracker(channel, hub)
	m[channel] = pt
	return pt
}
