package cmd

import (
	"testing"

	apiv1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1"
)

func TestRoomLabel(t *testing.T) {
	channel := &apiv1.Room{Name: "general", Kind: apiv1.RoomKind_ROOM_KIND_CHANNEL}
	if got := roomLabel(channel); got != "#general" {
		t.Errorf("roomLabel(channel) = %q, want %q", got, "#general")
	}

	dm := &apiv1.Room{Name: "alice", Kind: apiv1.RoomKind_ROOM_KIND_DM}
	if got := roomLabel(dm); got != "alice" {
		t.Errorf("roomLabel(dm) = %q, want %q", got, "alice")
	}
}

func TestRoomKindLabel(t *testing.T) {
	cases := []struct {
		kind apiv1.RoomKind
		want string
	}{
		{apiv1.RoomKind_ROOM_KIND_CHANNEL, "channel"},
		{apiv1.RoomKind_ROOM_KIND_DM, "dm"},
		{apiv1.RoomKind_ROOM_KIND_UNSPECIFIED, "unknown"},
	}
	for _, tc := range cases {
		if got := roomKindLabel(tc.kind); got != tc.want {
			t.Errorf("roomKindLabel(%v) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}
