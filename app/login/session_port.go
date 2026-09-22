package login

import (
	"context"

	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/interfaces/ports"
)

// SessionProbe adapts the SDK check to the account component's plain port.
// Options are read per call so configuration changes do not retain old secrets.
type SessionProbe struct{ Options func() SessionOptions }

func (p SessionProbe) Probe(ctx context.Context) (*ports.AccountIdentity, error) {
	opts := p.Options()
	opts.Checker = nil
	user, err := CheckSession(ctx, opts)
	if err != nil || user == nil {
		return nil, err
	}
	return &ports.AccountIdentity{
		ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName,
		Phone: user.Phone, Bot: user.Bot, Premium: user.Premium, Restricted: user.Restricted, Verified: user.Verified,
	}, nil
}

func protocolIdentity(user *ports.AccountIdentity) *tg.User {
	if user == nil {
		return nil
	}
	return &tg.User{
		ID: user.ID, Username: user.Username, FirstName: user.FirstName, LastName: user.LastName,
		Phone: user.Phone, Bot: user.Bot, Premium: user.Premium, Restricted: user.Restricted, Verified: user.Verified,
	}
}
