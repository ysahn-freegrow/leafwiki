package mcp

import (
	"testing"
	"time"
)

// newTestTicketStore returns a ticket store with a controllable clock.
func newTestTicketStore() (*uploadTicketStore, *time.Time) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newUploadTicketStore()
	s.now = func() time.Time { return now }
	return s, &now
}

func TestUploadTicketStore_RedeemReturnsBinding(t *testing.T) {
	s, _ := newTestTicketStore()
	raw, _, err := s.issue("user-1", "page-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ticket, ok := s.redeem(raw)
	if !ok {
		t.Fatal("fresh ticket should redeem")
	}
	if ticket.userID != "user-1" || ticket.pageID != "page-1" {
		t.Errorf("binding: got user=%q page=%q", ticket.userID, ticket.pageID)
	}
}

func TestUploadTicketStore_ExpiredTicketRejected(t *testing.T) {
	s, now := newTestTicketStore()
	raw, _, err := s.issue("user-1", "page-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	*now = now.Add(uploadTicketTTL + time.Second)
	if _, ok := s.redeem(raw); ok {
		t.Fatal("expired ticket must not redeem")
	}
}

func TestUploadTicketStore_CapsOutstandingTicketsPerUser(t *testing.T) {
	s, _ := newTestTicketStore()
	for i := 0; i < maxUploadTicketsPerUser; i++ {
		if _, _, err := s.issue("user-1", "page-1"); err != nil {
			t.Fatalf("issue #%d: %v", i, err)
		}
	}
	if _, _, err := s.issue("user-1", "page-1"); err == nil {
		t.Fatal("issuing beyond the per-user cap should fail")
	}
	if _, _, err := s.issue("user-2", "page-1"); err != nil {
		t.Fatalf("another user must not be affected by the cap: %v", err)
	}
}

func TestUploadTicketStore_ExpiredTicketsFreeTheCap(t *testing.T) {
	s, now := newTestTicketStore()
	for i := 0; i < maxUploadTicketsPerUser; i++ {
		if _, _, err := s.issue("user-1", "page-1"); err != nil {
			t.Fatalf("issue #%d: %v", i, err)
		}
	}
	*now = now.Add(uploadTicketTTL + time.Second)
	if _, _, err := s.issue("user-1", "page-1"); err != nil {
		t.Fatalf("expired tickets should no longer count toward the cap: %v", err)
	}
}
