package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const (
	// uploadTicketTTL bounds how long an issued upload URL stays redeemable.
	uploadTicketTTL = 10 * time.Minute
	// maxUploadTicketsPerUser caps outstanding (unredeemed, unexpired) upload
	// URLs per user so the in-memory store stays bounded.
	maxUploadTicketsPerUser = 20
)

var errTooManyUploadTickets = errors.New("too many outstanding upload URLs; use or let some expire first")

// uploadTicket binds a single-use upload URL to the user who requested it and
// the page the uploaded file is attached to.
type uploadTicket struct {
	userID    string
	pageID    string
	expiresAt time.Time
}

// uploadTicketStore holds short-lived, single-use upload tickets in memory.
// Tickets are keyed by the SHA-256 of the raw value, so the raw ticket (which
// acts as the upload credential) is never retained. Tickets do not survive a
// restart, which is acceptable for a 10-minute lifetime.
type uploadTicketStore struct {
	mu      sync.Mutex
	tickets map[string]uploadTicket
	now     func() time.Time
}

func newUploadTicketStore() *uploadTicketStore {
	return &uploadTicketStore{
		tickets: make(map[string]uploadTicket),
		now:     time.Now,
	}
}

// issue mints a new ticket for userID and pageID and returns the raw ticket
// value together with its expiry.
func (s *uploadTicketStore) issue(userID, pageID string) (string, time.Time, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	outstanding := 0
	for key, t := range s.tickets {
		if !now.Before(t.expiresAt) {
			delete(s.tickets, key)
			continue
		}
		if t.userID == userID {
			outstanding++
		}
	}
	if outstanding >= maxUploadTicketsPerUser {
		return "", time.Time{}, errTooManyUploadTickets
	}

	expiresAt := now.Add(uploadTicketTTL)
	s.tickets[hashTicket(raw)] = uploadTicket{userID: userID, pageID: pageID, expiresAt: expiresAt}
	return raw, expiresAt, nil
}

// redeem consumes a ticket. It reports false when the ticket is unknown,
// already used, or expired. A ticket is consumed even if the upload that
// follows fails, so every URL is good for exactly one request.
func (s *uploadTicketStore) redeem(raw string) (uploadTicket, bool) {
	key := hashTicket(raw)

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tickets[key]
	if !ok {
		return uploadTicket{}, false
	}
	delete(s.tickets, key)
	if !s.now().Before(t.expiresAt) {
		return uploadTicket{}, false
	}
	return t, true
}

func hashTicket(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
