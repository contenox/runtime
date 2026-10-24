package libacp

import "context"

func FilterSessionUpdates(live SessionID, inner Client) Client {
	return sessionUpdateFilter{Client: inner, live: live}
}

type sessionUpdateFilter struct {
	Client
	live SessionID
}

func (f sessionUpdateFilter) SessionUpdate(ctx context.Context, n SessionNotification) error {
	if n.SessionID != f.live {
		return nil
	}
	return f.Client.SessionUpdate(ctx, n)
}
