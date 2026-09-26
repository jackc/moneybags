// Package domain contains Money Bags' pure values and validation rules.
package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const MaxSafeCents int64 = 9007199254740991
const MaxNotesBytes = 65536
const MaxFileBytes = 5 << 20

func ValidCents(n int64) bool { return n >= -MaxSafeCents && n <= MaxSafeCents }
func AddCents(a, b int64) (int64, error) {
	if !ValidCents(a) || !ValidCents(b) || (b > 0 && a > MaxSafeCents-b) || (b < 0 && a < -MaxSafeCents-b) {
		return 0, errors.New("amount or balance exceeds the exact integer range")
	}
	return a + b, nil
}

// ParseDollars accepts decimal dollars without ever converting through a float.
func ParseDollars(s string) (int64, error) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	negative := strings.HasPrefix(s, "-")
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	p := strings.Split(s, ".")
	if len(p) > 2 || p[0] == "" {
		return 0, errors.New("invalid dollar amount")
	}
	for _, r := range p[0] {
		if r < '0' || r > '9' {
			return 0, errors.New("invalid dollar amount")
		}
	}
	whole, e := strconv.ParseInt(p[0], 10, 64)
	if e != nil || whole > MaxSafeCents/100 {
		return 0, errors.New("amount too large")
	}
	fraction := int64(0)
	if len(p) == 2 {
		if len(p[1]) > 2 || len(p[1]) == 0 {
			return 0, errors.New("use at most two decimal places")
		}
		for _, r := range p[1] {
			if r < '0' || r > '9' {
				return 0, errors.New("invalid dollar amount")
			}
		}
		f := p[1]
		if len(f) == 1 {
			f += "0"
		}
		fraction, _ = strconv.ParseInt(f, 10, 64)
	}
	n := whole*100 + fraction
	if negative {
		n = -n
	}
	if !ValidCents(n) {
		return 0, errors.New("amount too large")
	}
	return n, nil
}
func NormalizeName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
func ValidateDate(date string, now time.Time, zone string) (string, error) {
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return "", errors.New("invalid family time zone")
	}
	today := now.In(loc).Format("2006-01-02")
	if date == "" {
		date = today
	}
	d, e := time.Parse("2006-01-02", date)
	if e != nil || d.Format("2006-01-02") != date {
		return "", errors.New("date must be YYYY-MM-DD")
	}
	if date > today {
		return "", errors.New("future dates are not allowed")
	}
	return date, nil
}
func ValidateNotes(notes string) error {
	if len(notes) > MaxNotesBytes {
		return fmt.Errorf("notes exceed %d bytes", MaxNotesBytes)
	}
	return nil
}

type Family struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TimeZone  string    `json:"time_zone"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type User struct {
	ID          string    `json:"id"`
	FamilyID    string    `json:"family_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}
type Bag struct {
	ID             string    `json:"id"`
	FamilyID       string    `json:"family_id"`
	Name           string    `json:"name"`
	NormalizedName string    `json:"normalized_name"`
	Description    string    `json:"description"`
	Archived       bool      `json:"archived"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
type Entry struct {
	ID          string    `json:"id"`
	FamilyID    string    `json:"family_id"`
	BagID       string    `json:"bag_id"`
	AmountCents int64     `json:"amount_cents"`
	Date        string    `json:"date"`
	Notes       string    `json:"notes"`
	AuthorID    string    `json:"author_id"`
	AuthorName  string    `json:"author_name"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type Attachment struct {
	ID        string    `json:"id"`
	FamilyID  string    `json:"family_id"`
	EntryID   string    `json:"entry_id"`
	BlobKey   string    `json:"blob_key,omitempty"`
	FileName  string    `json:"file_name"`
	MIMEType  string    `json:"mime_type"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}
type Upload struct {
	ID              string     `json:"id"`
	FamilyID        string     `json:"family_id"`
	ActorID         string     `json:"actor_id"`
	Attachment      Attachment `json:"attachment"`
	ExpiresAt       time.Time  `json:"expires_at"`
	ConsumedEntryID string     `json:"consumed_entry_id,omitempty"`
}
type EntrySnapshot struct {
	Entry       Entry        `json:"entry"`
	Attachments []Attachment `json:"attachments"`
}
type Revision struct {
	ID        string         `json:"id"`
	FamilyID  string         `json:"family_id"`
	EntryID   string         `json:"entry_id"`
	Version   int64          `json:"version"`
	ActorID   string         `json:"actor_id"`
	Source    string         `json:"source"`
	CreatedAt time.Time      `json:"created_at"`
	Previous  *EntrySnapshot `json:"previous,omitempty"`
	Current   EntrySnapshot  `json:"current"`
}
type Audit struct {
	ID          string    `json:"id"`
	FamilyID    string    `json:"family_id"`
	ActorID     string    `json:"actor_id"`
	Action      string    `json:"action"`
	AffectedIDs []string  `json:"affected_ids"`
	Source      string    `json:"source"`
	RequestID   string    `json:"request_id"`
	CreatedAt   time.Time `json:"created_at"`
}
