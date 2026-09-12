package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestIsDirectChat(t *testing.T) {
	pn := func(user string) types.MessageInfo {
		return types.MessageInfo{Chat: types.NewJID(user, types.DefaultUserServer)}
	}
	cases := []struct {
		name string
		info types.MessageInfo
		want bool
	}{
		{"direct", pn("919876543210"), true},
		{"group", types.MessageInfo{Chat: types.NewJID("12345-678", "g.us"), IsGroup: true}, false},
		{"broadcast-list", types.MessageInfo{Chat: types.NewJID("12345", "broadcast"), IsGroup: true}, false},
		{"status", types.MessageInfo{Chat: types.StatusBroadcastJID}, false},
		{"newsletter", types.MessageInfo{Chat: types.NewJID("chan", types.NewsletterServer)}, false},
		{"empty-chat", types.MessageInfo{}, false},
	}
	for _, tc := range cases {
		if got := isDirectChat(tc.info); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
