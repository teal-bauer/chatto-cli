package api

import (
	"context"
	"sync"

	apiv1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1"
	realtimev1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/realtime/v1"
)

// UserCache resolves user IDs to their public profile, caching results so a
// burst of realtime events from the same actor only fetches once. Safe for
// concurrent use.
type UserCache struct {
	client *Client

	mu    sync.Mutex
	users map[string]*apiv1.User
}

// NewUserCache creates an empty UserCache backed by client.
func NewUserCache(client *Client) *UserCache {
	return &UserCache{client: client, users: make(map[string]*apiv1.User)}
}

// Get resolves userID, using the cache when possible and BatchGetUsers on a
// cache miss. Returns (nil, nil) for an empty userID or one that no longer
// resolves to a user.
func (uc *UserCache) Get(ctx context.Context, userID string) (*apiv1.User, error) {
	if userID == "" {
		return nil, nil
	}

	uc.mu.Lock()
	if u, ok := uc.users[userID]; ok {
		uc.mu.Unlock()
		return u, nil
	}
	uc.mu.Unlock()

	members, err := uc.client.BatchGetUsers(ctx, []string{userID})
	if err != nil {
		return nil, err
	}
	var user *apiv1.User
	if len(members) > 0 {
		user = members[0].GetUser()
	}

	uc.mu.Lock()
	uc.users[userID] = user
	uc.mu.Unlock()
	return user, nil
}

// HydratedEvent is a realtime event plus whatever Hydrator.Hydrate fetched
// to render it: the current Message for message_posted/message_updated, and
// the resolved actor for Event.ActorId. Message and Actor are nil when
// hydration wasn't applicable (most event kinds) or didn't resolve anything
// (e.g. a system actor).
type HydratedEvent struct {
	Event     *realtimev1.RealtimeEvent
	EventName string
	Message   *apiv1.Message
	Actor     *apiv1.User
}

// Hydrator turns realtime invalidation-signal events into renderable
// data. Realtime events carry only IDs and small inline hints, not full
// renderable objects (see the hydration notes on each message in
// realtime.proto/events.proto); Hydrate fetches the full Message and
// resolves the actor before a caller renders the event.
type Hydrator struct {
	client *Client
	users  *UserCache
}

// NewHydrator creates a Hydrator backed by client, with its own UserCache.
func NewHydrator(client *Client) *Hydrator {
	return &Hydrator{client: client, users: NewUserCache(client)}
}

// Hydrate resolves one realtime event into a HydratedEvent, or returns (nil, nil)
// if the event should be dropped entirely -- currently only for
// message_posted/message_updated whose Message came back NOT_FOUND, i.e. a
// message retracted between the signal and this fetch.
func (h *Hydrator) Hydrate(ctx context.Context, env *realtimev1.RealtimeEvent) (*HydratedEvent, error) {
	name := EventName(env)

	var roomID, eventID string
	switch payload := env.GetEvent().(type) {
	case *realtimev1.RealtimeEvent_MessagePosted:
		// The realtime event ID is the posted message's own event ID.
		roomID, eventID = payload.MessagePosted.GetRoomId(), env.GetId()
	case *realtimev1.RealtimeEvent_MessageEdited:
		roomID, eventID = payload.MessageEdited.GetRoomId(), payload.MessageEdited.GetMessageEventId()
	}

	var message *apiv1.Message
	if roomID != "" && eventID != "" {
		var err error
		message, err = h.client.GetMessage(ctx, roomID, eventID)
		if err != nil {
			return nil, err
		}
		if message == nil {
			return nil, nil // retracted between signal and fetch
		}
	}

	var actor *apiv1.User
	if env.GetActorId() != "" {
		var err error
		actor, err = h.users.Get(ctx, env.GetActorId())
		if err != nil {
			return nil, err
		}
	}

	return &HydratedEvent{Event: env, EventName: name, Message: message, Actor: actor}, nil
}

// EventName returns the public snake_case name for a realtime event's oneof
// case, matching realtime.proto 1:1 except for three renames kept for
// GraphQL-era naming compatibility: message_edited -> message_updated,
// message_retracted -> message_deleted, and
// asset_processing_succeeded -> video_processing_completed. Returns
// "unknown" if the event carries no payload or an oneof case added to the
// proto after this was written.
func EventName(env *realtimev1.RealtimeEvent) string {
	switch env.GetEvent().(type) {
	case *realtimev1.RealtimeEvent_MessagePosted:
		return "message_posted"
	case *realtimev1.RealtimeEvent_MessageEdited:
		return "message_updated"
	case *realtimev1.RealtimeEvent_MessageRetracted:
		return "message_deleted"
	case *realtimev1.RealtimeEvent_MessagePinned:
		return "message_pinned"
	case *realtimev1.RealtimeEvent_MessageUnpinned:
		return "message_unpinned"
	case *realtimev1.RealtimeEvent_ReactionAdded:
		return "reaction_added"
	case *realtimev1.RealtimeEvent_ReactionRemoved:
		return "reaction_removed"
	case *realtimev1.RealtimeEvent_UserTyping:
		return "user_typing"
	case *realtimev1.RealtimeEvent_PresenceChanged:
		return "presence_changed"
	case *realtimev1.RealtimeEvent_RoomCreated:
		return "room_created"
	case *realtimev1.RealtimeEvent_RoomUpdated:
		return "room_updated"
	case *realtimev1.RealtimeEvent_RoomDeleted:
		return "room_deleted"
	case *realtimev1.RealtimeEvent_RoomArchived:
		return "room_archived"
	case *realtimev1.RealtimeEvent_RoomUnarchived:
		return "room_unarchived"
	case *realtimev1.RealtimeEvent_RoomUniversalChanged:
		return "room_universal_changed"
	case *realtimev1.RealtimeEvent_RoomSlowModeChanged:
		return "room_slow_mode_changed"
	case *realtimev1.RealtimeEvent_RoomThreadingModeChanged:
		return "room_threading_mode_changed"
	case *realtimev1.RealtimeEvent_UserJoinedRoom:
		return "user_joined_room"
	case *realtimev1.RealtimeEvent_UserLeftRoom:
		return "user_left_room"
	case *realtimev1.RealtimeEvent_UserAccountCreated:
		return "user_account_created"
	case *realtimev1.RealtimeEvent_UserProfileChanged:
		return "user_profile_changed"
	case *realtimev1.RealtimeEvent_UserAccountDeleted:
		return "user_account_deleted"
	case *realtimev1.RealtimeEvent_ThreadCreated:
		return "thread_created"
	case *realtimev1.RealtimeEvent_ThreadViewerStateChanged:
		return "thread_viewer_state_changed"
	case *realtimev1.RealtimeEvent_ViewerPreferencesChanged:
		return "viewer_preferences_changed"
	case *realtimev1.RealtimeEvent_ViewerPermissionsChanged:
		return "viewer_permissions_changed"
	case *realtimev1.RealtimeEvent_ViewerPresencePreferenceChanged:
		return "viewer_presence_preference_changed"
	case *realtimev1.RealtimeEvent_ServerProfileChanged:
		return "server_profile_changed"
	case *realtimev1.RealtimeEvent_ServerMotdChanged:
		return "server_motd_changed"
	case *realtimev1.RealtimeEvent_RoomReadStateChanged:
		return "room_read_state_changed"
	case *realtimev1.RealtimeEvent_RoomLayoutChanged:
		return "room_layout_changed"
	case *realtimev1.RealtimeEvent_NotificationOccurrencesChanged:
		return "notification_occurrences_changed"
	case *realtimev1.RealtimeEvent_NotificationUnreadStateChanged:
		return "notification_unread_state_changed"
	case *realtimev1.RealtimeEvent_AssetProcessingStarted:
		return "asset_processing_started"
	case *realtimev1.RealtimeEvent_AssetProcessingSucceeded:
		return "video_processing_completed"
	case *realtimev1.RealtimeEvent_AssetProcessingFailed:
		return "asset_processing_failed"
	case *realtimev1.RealtimeEvent_AssetDeleted:
		return "asset_deleted"
	case *realtimev1.RealtimeEvent_VoiceCallStarted:
		return "call_started"
	case *realtimev1.RealtimeEvent_VoiceCallParticipantJoined:
		return "call_participant_joined"
	case *realtimev1.RealtimeEvent_VoiceCallParticipantLeft:
		return "call_participant_left"
	case *realtimev1.RealtimeEvent_VoiceCallEnded:
		return "call_ended"
	case *realtimev1.RealtimeEvent_RoleCreated:
		return "role_created"
	case *realtimev1.RealtimeEvent_RoleUpdated:
		return "role_updated"
	case *realtimev1.RealtimeEvent_RoleDeleted:
		return "role_deleted"
	case *realtimev1.RealtimeEvent_RolesReordered:
		return "roles_reordered"
	case *realtimev1.RealtimeEvent_RoleAssigned:
		return "role_assigned"
	case *realtimev1.RealtimeEvent_RoleRevoked:
		return "role_revoked"
	case *realtimev1.RealtimeEvent_RolePermissionsChanged:
		return "role_permissions_changed"
	default:
		return "unknown"
	}
}
