package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/moneybags/backend/domain"
	"time"
)

var ErrNotFound = errors.New("record not found")

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Current any    `json:"current,omitempty"`
}

func (e *Error) Error() string      { return e.Message }
func E(code, message string) *Error { return &Error{Code: code, Message: message} }

type Store interface {
	InTx(context.Context, string, func(Tx) error) error
}

// Tx is bound to one family, or the explicit global identity directory. Implementations serialize writers and roll back all changes on errors.
type Tx interface {
	Get(kind, id string, out any) error
	List(kind string) ([]json.RawMessage, error)
	Put(kind, id string, value any) error
	Delete(kind, id string) error
	Balance(bagID string) (int64, error)
}
type BlobStore interface {
	PutBlob(context.Context, string, []byte) error
	GetBlob(context.Context, string) ([]byte, error)
	DeleteBlob(context.Context, string) error
}
type BlobInfo struct {
	Key        string
	ModifiedAt time.Time
}
type BlobLister interface {
	ListBlobs(context.Context) ([]BlobInfo, error)
}
type FileFetcher interface {
	Fetch(context.Context, string) ([]byte, string, error)
}
type OAuthClient struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}
type OAuthClientResolver interface {
	Resolve(context.Context, string) (OAuthClient, error)
}
type Principal struct {
	SessionID       string    `json:"session_id,omitempty"`
	UserID          string    `json:"user_id"`
	FamilyID        string    `json:"family_id"`
	Scopes          []string  `json:"scopes"`
	Source          string    `json:"source"`
	AuthenticatedAt time.Time `json:"authenticated_at"`
	Admin           bool      `json:"-"`
}
type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type Config struct {
	Store             Store
	Blobs             BlobStore
	Fetcher           FileFetcher
	OAuthResolver     OAuthClientResolver
	Clock             func() time.Time
	ID                func() string
	Token             func() string
	Origin            string
	RPID              string
	RPName            string
	StorageQuotaBytes int64
}
type ActionInfo struct {
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Public          bool           `json:"public"`
	Mutation        bool           `json:"mutation"`
	Permission      string         `json:"permission"`
	Web             bool           `json:"web"`
	MCP             bool           `json:"mcp"`
	Administrative  bool           `json:"administrative"`
	MaxPayloadBytes int            `json:"max_payload_bytes"`
	InputSchema     map[string]any `json:"input_schema"`
	NewParams       func() any     `json:"-"`
}
type actionRegistration struct {
	info    ActionInfo
	handler func(context.Context, json.RawMessage) (any, error)
}
type FileSource struct {
	DownloadURL string `json:"download_url,omitempty"`
	FileID      string `json:"file_id,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	Data        []byte `json:"data,omitempty"`
}

// FamilyLister supports administrative reporting of orphaned family databases.
type FamilyLister interface {
	FamilyIDs(context.Context) ([]string, error)
}

// QueryTx keeps filtered, bounded financial reads in persistence adapters.
type QueryTx interface {
	QueryBags(archived *bool, offset, limit int) ([]BagBalance, error)
	QueryEntries(query EntryQuery) ([]domain.Entry, error)
	Balances(bagIDs []string) (map[string]int64, error)
	EntryAttachments(entryID string) ([]domain.Attachment, error)
}
type BagBalance struct {
	Bag          domain.Bag
	BalanceCents int64
}
type EntryQuery struct {
	BagID, FromDate, ToDate string
	Offset, Limit           int
}

// FamilyProvisioner makes a new family file explicit. Ordinary reads never provision missing files.
type FamilyProvisioner interface {
	CreateFamily(context.Context, string) error
}
