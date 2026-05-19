package realtime

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v4"
	"github.com/sirupsen/logrus"
)

// Notification is a single LISTEN/NOTIFY payload received from Postgres.
type Notification struct {
	Channel string
	Payload string
}

// ListenPool maintains a dedicated set of pgx connections for LISTEN/NOTIFY.
// These connections are NOT shared with the main request pool because
// LISTEN state is per-connection and must not be reused for regular queries.
type ListenPool struct {
	connString string
	size       int
	conns      []*pgx.Conn
	out        chan Notification
}

// NewListenPool creates a ListenPool with size dedicated connections.
// connString is a libpq-compatible DSN (e.g. "postgres://user:pass@host/db").
// Call Start(ctx) to open connections and begin listening.
func NewListenPool(connString string, size int) *ListenPool {
	return &ListenPool{
		connString: connString,
		size:       size,
		out:        make(chan Notification, 256),
	}
}

// Notifications returns the channel on which received notifications are sent.
// Consumers should read from this channel continuously; a slow consumer will
// cause the channel buffer to fill and notifications to be dropped with a log warning.
func (p *ListenPool) Notifications() <-chan Notification {
	return p.out
}

// Start opens the pool connections and begins listening.
// Returns an error if any connection fails to open.
// All connections are closed when ctx is cancelled.
func (p *ListenPool) Start(ctx context.Context, channels []string) error {
	p.conns = make([]*pgx.Conn, 0, p.size)

	for i := 0; i < p.size; i++ {
		conn, err := pgx.Connect(ctx, p.connString)
		if err != nil {
			p.closeAll()
			return fmt.Errorf("realtime pool: connect %d: %w", i, err)
		}
		p.conns = append(p.conns, conn)

		// Register LISTEN on this connection.
		for _, ch := range channels {
			if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
				p.closeAll()
				return fmt.Errorf("realtime pool: LISTEN %q: %w", ch, err)
			}
		}

		go p.waitLoop(ctx, conn)
	}

	return nil
}

// Listen adds a LISTEN on channel across all pool connections.
func (p *ListenPool) Listen(ctx context.Context, channel string) error {
	sanitized := pgx.Identifier{channel}.Sanitize()
	for _, conn := range p.conns {
		if _, err := conn.Exec(ctx, "LISTEN "+sanitized); err != nil {
			return fmt.Errorf("realtime pool: LISTEN %q: %w", channel, err)
		}
	}
	return nil
}

func (p *ListenPool) waitLoop(ctx context.Context, conn *pgx.Conn) {
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logrus.WithError(err).Warn("realtime pool: WaitForNotification error")
			return
		}

		notif := Notification{
			Channel: n.Channel,
			Payload: n.Payload,
		}

		select {
		case p.out <- notif:
		default:
			logrus.WithField("channel", n.Channel).Warn("realtime pool: notification channel full, dropping")
		}
	}
}

func (p *ListenPool) closeAll() {
	for _, conn := range p.conns {
		if err := conn.Close(context.Background()); err != nil {
			logrus.WithError(err).Warn("realtime pool: close connection failed")
		}
	}
}
