package internal

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestMail_ListClaim(t *testing.T) {
	store := newFakeMailStore()
	store.Insert(nil, &MailDoc{To: 10001, From: 1, Type: MailTypeNormal,
		Attachments: []Attachment{{Kind: "gold", Count: 50}}}, func(error) {})
	m := NewMail(10001, store)

	var id primitive.ObjectID
	m.List(nil, 50, func(ms []MailDoc, _ error) {
		if len(ms) != 1 {
			t.Fatalf("len=%d", len(ms))
		}
		id = ms[0].ID
	})
	m.Claim(nil, id, func(ok bool, doc *MailDoc, _ error) {
		if !ok || doc.Attachments[0].Kind != "gold" {
			t.Fatalf("claim: %v %+v", ok, doc)
		}
	})
}

func TestPlayer_AttachMail(t *testing.T) {
	p := buildPlayer(10001, NewPlayerDoc(10001))
	if p.Mail() != nil {
		t.Fatal("mail should be nil before attach")
	}
	p.attachMail(newFakeMailStore())
	if p.Mail() == nil {
		t.Fatal("mail should be set after attach")
	}
}
