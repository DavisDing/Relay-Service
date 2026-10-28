package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Leadership pins a PostgreSQL session lock. This only protects processes using
// this database; operators must still isolate independently restored databases.
type Leadership struct{ connection *pgxpool.Conn }

type Leader interface {
	Watch(context.Context) error
	Close()
}

func (p *Postgres) AcquireLeadership(ctx context.Context) (Leader, error) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext('relayservice:leader'))").Scan(&acquired); err != nil {
		// The request may have acquired the session lock before cancellation.
		// Discard the session rather than returning an uncertain lock to the pool.
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = conn.Conn().Close(closeCtx)
		cancel()
		conn.Release()
		return nil, err
	}
	if !acquired {
		conn.Release()
		return nil, fmt.Errorf("another Relay process owns this database's scheduler lock")
	}
	return &Leadership{connection: conn}, nil
}

func (l *Leadership) Watch(ctx context.Context) error {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := l.connection.Conn().Ping(checkCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("scheduler database lock lost: %w", err)
			}
		}
	}
}

func (l *Leadership) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := l.connection.Exec(ctx, "SELECT pg_advisory_unlock(hashtext('relayservice:leader'))"); err != nil {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = l.connection.Conn().Close(closeCtx)
		closeCancel()
	}
	l.connection.Release()
}
