package cmd

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1"
	realtimev1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/realtime/v1"
)

func TestRenderAttachment(t *testing.T) {
	cases := []struct {
		name     string
		att      *apiv1.MessageAttachment
		instance string
		want     string
	}{
		{
			name: "image with relative URL is resolved and shown as markdown",
			att: &apiv1.MessageAttachment{
				Filename:    "cat.png",
				ContentType: "image/png",
				AssetUrl:    &apiv1.MessageAssetUrl{Url: "/files/cat.png"},
			},
			instance: "https://chat.example.com/",
			want:     "![cat.png](https://chat.example.com/files/cat.png)",
		},
		{
			name: "non-image attachment renders as a bare resolved URL",
			att: &apiv1.MessageAttachment{
				Filename:    "report.pdf",
				ContentType: "application/pdf",
				AssetUrl:    &apiv1.MessageAssetUrl{Url: "https://cdn.example.com/report.pdf"},
			},
			instance: "https://chat.example.com",
			want:     "https://cdn.example.com/report.pdf",
		},
		{
			name: "completed video processing uses the first variant instead of the raw asset",
			att: &apiv1.MessageAttachment{
				Filename:    "clip.mov",
				ContentType: "video/quicktime",
				AssetUrl:    &apiv1.MessageAssetUrl{Url: "/files/clip.mov"},
				VideoProcessing: &apiv1.MessageVideoProcessing{
					Status: apiv1.MessageVideoProcessingStatus_MESSAGE_VIDEO_PROCESSING_STATUS_COMPLETED,
					Variants: []*apiv1.MessageVideoVariant{
						{AssetUrl: &apiv1.MessageAssetUrl{Url: "/files/clip.mp4"}},
					},
				},
			},
			instance: "https://chat.example.com",
			want:     "https://chat.example.com/files/clip.mp4",
		},
		{
			name: "in-progress video processing falls back to the raw asset",
			att: &apiv1.MessageAttachment{
				Filename:    "clip.mov",
				ContentType: "video/quicktime",
				AssetUrl:    &apiv1.MessageAssetUrl{Url: "/files/clip.mov"},
				VideoProcessing: &apiv1.MessageVideoProcessing{
					Status: apiv1.MessageVideoProcessingStatus_MESSAGE_VIDEO_PROCESSING_STATUS_PROCESSING,
				},
			},
			instance: "https://chat.example.com",
			want:     "https://chat.example.com/files/clip.mov",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderAttachment(tc.att, tc.instance)
			if got != tc.want {
				t.Errorf("renderAttachment() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderBody(t *testing.T) {
	msg := &apiv1.Message{
		Body: proto.String("look at this"),
		Attachments: []*apiv1.MessageAttachment{
			{Filename: "a.png", ContentType: "image/png", AssetUrl: &apiv1.MessageAssetUrl{Url: "/a.png"}},
		},
	}
	got := renderBody(msg, "https://chat.example.com")
	want := "look at this\n![a.png](https://chat.example.com/a.png)"
	if got != want {
		t.Errorf("renderBody() = %q, want %q", got, want)
	}
}

// Every path that puts a body on screen has to expand timestamp tokens, not
// just the main body: a reference preview showing a raw <t:...:F> is exactly
// the thing FDR-030 rendering is meant to avoid.
func TestRenderedBodiesExpandTimestampTokens(t *testing.T) {
	body := "ship at <t:1745764200:F>"
	if got := renderBody(&apiv1.Message{Body: proto.String(body)}, "https://chat.example.com"); strings.Contains(got, "<t:") {
		t.Errorf("renderBody() left a raw token: %q", got)
	}

	r := &eventRenderer{}
	if got := r.formatRef(&apiv1.Message{Body: proto.String(body)}); strings.Contains(got, "<t:") {
		t.Errorf("formatRef() left a raw token: %q", got)
	}
}

func TestFormatTimestamp(t *testing.T) {
	if got := formatTimestamp(nil); got != "[]" {
		t.Errorf("formatTimestamp(nil) = %q, want %q", got, "[]")
	}

	// formatTimestamp renders ts.AsTime() (always UTC); for "now" just check
	// the shape (bracketed HH:MM) rather than an exact string, to stay
	// independent of the test host's local timezone.
	got := formatTimestamp(timestamppb.New(time.Now()))
	if len(got) != len("[15:04]") {
		t.Errorf("formatTimestamp(now) = %q, want [HH:MM]-shaped", got)
	}

	// A timestamp built from a UTC time.Time round-trips through
	// ts.AsTime() (also UTC), so the full "date time" rendering can be
	// asserted exactly here.
	past := time.Date(2020, time.March, 1, 9, 30, 0, 0, time.UTC)
	got = formatTimestamp(timestamppb.New(past))
	want := "[2020-03-01 09:30]"
	if got != want {
		t.Errorf("formatTimestamp(past) = %q, want %q", got, want)
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName(nil, "u1"); got != "u1" {
		t.Errorf("displayName(nil) = %q, want %q", got, "u1")
	}
	u := &apiv1.User{Login: "tealb"}
	if got := displayName(u, "u1"); got != "tealb" {
		t.Errorf("displayName(login only) = %q, want %q", got, "tealb")
	}
	u.DisplayName = "Teal B"
	if got := displayName(u, "u1"); got != "Teal B" {
		t.Errorf("displayName(display name set) = %q, want %q", got, "Teal B")
	}
}

func TestEnvelopeRoomID(t *testing.T) {
	cases := []struct {
		name string
		env  *realtimev1.RealtimeEventEnvelope
		want string
	}{
		{
			name: "message_posted carries its room id",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_MessagePosted{
					MessagePosted: &realtimev1.RealtimeMessagePostedEvent{RoomId: "room-1"},
				},
			},
			want: "room-1",
		},
		{
			name: "user_left_room carries its room id",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_UserLeftRoom{
					UserLeftRoom: &realtimev1.RealtimeRoomEvent{RoomId: "room-2"},
				},
			},
			want: "room-2",
		},
		{
			name: "event kinds with no room (e.g. session_terminated) resolve to empty",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_SessionTerminated{
					SessionTerminated: &realtimev1.RealtimeSessionTerminatedEvent{},
				},
			},
			want: "",
		},
		{
			name: "thread_created carries its room id (previously unhandled)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_ThreadCreated{
					ThreadCreated: &realtimev1.RealtimeThreadCreatedEvent{RoomId: "room-3"},
				},
			},
			want: "room-3",
		},
		{
			name: "room_universal_changed carries its room id (previously unhandled)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_RoomUniversalChanged{
					RoomUniversalChanged: &realtimev1.RealtimeRoomUniversalChangedEvent{RoomId: "room-4"},
				},
			},
			want: "room-4",
		},
		{
			name: "room_marked_as_read carries its room id (previously unhandled)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_RoomMarkedAsRead{
					RoomMarkedAsRead: &realtimev1.RealtimeRoomMarkedAsReadEvent{RoomId: "room-5"},
				},
			},
			want: "room-5",
		},
		{
			name: "notification_created with room_id set (previously unhandled, optional string)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_NotificationCreated{
					NotificationCreated: &realtimev1.RealtimeNotificationCreatedEvent{
						NotificationId: "notif-1",
						RoomId:         proto.String("room-6"),
					},
				},
			},
			want: "room-6",
		},
		{
			name: "notification_created with room_id unset resolves to empty (optional string, absent)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_NotificationCreated{
					NotificationCreated: &realtimev1.RealtimeNotificationCreatedEvent{
						NotificationId: "notif-2",
					},
				},
			},
			want: "",
		},
		{
			name: "mention_notification carries its room id (previously unhandled)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_MentionNotification{
					MentionNotification: &realtimev1.RealtimeMentionNotificationEvent{RoomId: "room-7"},
				},
			},
			want: "room-7",
		},
		{
			name: "call_started carries its room id (previously unhandled)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_CallStarted{
					CallStarted: &realtimev1.RealtimeCallEvent{RoomId: "room-8"},
				},
			},
			want: "room-8",
		},
		{
			name: "asset_processing_started carries its room id (previously unhandled, optional string)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_AssetProcessingStarted{
					AssetProcessingStarted: &realtimev1.RealtimeAssetProcessingEvent{RoomId: proto.String("room-9")},
				},
			},
			want: "room-9",
		},
		{
			name: "asset_deleted carries its room id (previously unhandled, optional string)",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_AssetDeleted{
					AssetDeleted: &realtimev1.RealtimeAssetDeletedEvent{RoomId: proto.String("room-10")},
				},
			},
			want: "room-10",
		},
		{
			name: "server_updated has no room_id field at all",
			env: &realtimev1.RealtimeEventEnvelope{
				Event: &realtimev1.RealtimeEventEnvelope_ServerUpdated{
					ServerUpdated: &realtimev1.RealtimeServerUpdatedEvent{Name: "test"},
				},
			},
			want: "",
		},
		{
			name: "no event set",
			env:  &realtimev1.RealtimeEventEnvelope{},
			want: "",
		},
		{
			name: "nil envelope",
			env:  nil,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := envelopeRoomID(tc.env); got != tc.want {
				t.Errorf("envelopeRoomID() = %q, want %q", got, tc.want)
			}
		})
	}
}
