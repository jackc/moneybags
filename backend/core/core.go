// Package core is the common application boundary for every transport.
package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"time"
)

type Core struct {
	allowRegistration    bool
	store                Store
	blobs                BlobStore
	fetcher              FileFetcher
	oauthResolver        OAuthClientResolver
	clock                func() time.Time
	id                   func() string
	token                func() string
	origin, rpID, rpName string
	quota                int64
	actions              map[string]actionRegistration
}

func New(cfg Config) *Core {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.ID == nil {
		cfg.ID = func() string {
			var b [16]byte
			if _, e := rand.Read(b[:]); e != nil {
				panic(e)
			}
			return hex.EncodeToString(b[:])
		}
	}
	if cfg.Token == nil {
		cfg.Token = func() string {
			var b [32]byte
			if _, e := rand.Read(b[:]); e != nil {
				panic(e)
			}
			return base64.RawURLEncoding.EncodeToString(b[:])
		}
	}
	if cfg.StorageQuotaBytes <= 0 {
		cfg.StorageQuotaBytes = 1 << 30
	}
	c := &Core{store: cfg.Store, blobs: cfg.Blobs, fetcher: cfg.Fetcher, oauthResolver: cfg.OAuthResolver, clock: cfg.Clock, id: cfg.ID, token: cfg.Token, origin: cfg.Origin, rpID: cfg.RPID, rpName: cfg.RPName, quota: cfg.StorageQuotaBytes, actions: map[string]actionRegistration{}}
	c.registerFinanceActions()
	c.allowRegistration = cfg.AllowRegistration
	c.registerAuthActions()
	return c
}
func (c *Core) now() time.Time { return c.clock().UTC().Truncate(time.Microsecond) }

// AllowRegistration reports whether new families may be created. Invitations
// to existing families remain available regardless of this setting.
func (c *Core) AllowRegistration() bool { return c.allowRegistration }
func (c *Core) register(name string, info ActionInfo, handler func(context.Context, json.RawMessage) (any, error)) {
	if _, ok := c.actions[name]; ok {
		panic("duplicate action: " + name)
	}
	info.Name = name
	if info.MaxPayloadBytes == 0 {
		info.MaxPayloadBytes = 1 << 20
	}
	c.actions[name] = actionRegistration{info: info, handler: handler}
}
func (c *Core) Actions() []ActionInfo {
	a := make([]ActionInfo, 0, len(c.actions))
	for _, v := range c.actions {
		a = append(a, v.info)
	}
	sort.Slice(a, func(i, j int) bool { return a[i].Name < a[j].Name })
	return a
}
func (c *Core) InvokeJSON(ctx context.Context, name string, raw json.RawMessage) ([]byte, error) {
	a, ok := c.actions[name]
	if !ok {
		return nil, E("not_found", "Unknown action")
	}
	if len(raw) > a.info.MaxPayloadBytes {
		return nil, E("validation_error", "Request exceeds action payload limit")
	}
	if !a.info.Public {
		if a.info.Administrative {
			p, ok := PrincipalFromContext(ctx)
			if !ok || !p.Admin {
				return nil, E("permission_denied", "Administrative access required")
			}
		} else if _, err := c.authorize(ctx, a.info.Permission); err != nil {
			return nil, err
		}
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	r, e := a.handler(ctx, raw)
	if e != nil {
		if errors.Is(e, ErrNotFound) {
			return nil, E("not_found", "Record not found")
		}
		var app *Error
		if errors.As(e, &app) {
			return nil, app
		}
		return nil, e
	}
	return json.Marshal(r)
}
func Decode[T any](raw json.RawMessage) (T, error) {
	var p T
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return p, E("validation_error", "Expected one JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&p); e != nil {
		return p, E("validation_error", "Invalid request fields or types")
	}
	var more any
	if e := d.Decode(&more); e != io.EOF {
		return p, E("validation_error", "Expected one JSON object")
	}
	return p, nil
}

// Action provides typed in-process invocation through the identical catalog pipeline.
type Action[P, R any] struct {
	Core *Core
	Name string
}

func (a Action[P, R]) Call(ctx context.Context, p P) (R, error) {
	var result R
	raw, e := json.Marshal(p)
	if e != nil {
		return result, e
	}
	out, e := a.Core.InvokeJSON(ctx, a.Name, raw)
	if e != nil {
		return result, e
	}
	e = json.Unmarshal(out, &result)
	return result, e
}
func Register[P, R any](c *Core, name string, info ActionInfo, handler func(context.Context, P) (R, error)) {
	info.NewParams = func() any { return new(P) }
	c.register(name, info, func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, e := Decode[P](raw)
		if e != nil {
			return nil, e
		}
		return handler(ctx, p)
	})
}
func listRecords[T any](tx Tx, kind string) ([]T, error) {
	rows, e := tx.List(kind)
	if e != nil {
		return nil, e
	}
	out := make([]T, 0, len(rows))
	for _, raw := range rows {
		var v T
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
