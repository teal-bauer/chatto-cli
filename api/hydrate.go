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

// HydratedEvent is a realtime envelope plus whatever Hydrator.Hydrate fetched
// to render it: the current Message for message_posted/message_updated, and
// the resolved actor for envelope.ActorId. Message and Actor are nil when
// hydration wasn't applicable (most event kinds) or didn't resolve anything
// (e.g. a system actor).
type HydratedEvent struct {
	Envelope  *realtimev1.RealtimeEventEnvelope
	EventName string
	Message   *apiv1.Message
	Actor     *apiv1.User
}

// Hydrator turns realtime invalidation-signal envelopes into renderable
// data. Realtime events carry only IDs and small inline hints, not full
// renderable objects (see the hydration notes on each message in
// realtime.proto); Hydrate fetches the full Message and resolves the actor
// before a caller renders the event.
type Hydrator struct {
	client *Client
	users  *UserCache
}

// NewHydrator creates a Hydrator backed by client, with its own UserCache.
func NewHydrator(client *Client) *Hydrator {
	return &Hydrator{client: client, users: NewUserCache(client)}
}

// Hydrate resolves one envelope into a HydratedEvent, or returns (nil, nil)
// if the event should be dropped entirely -- currently only for
// message_posted/message_updated whose Message came back NOT_FOUND, i.e. a
// message retracted between the signal and this fetch.
func (h *Hydrator) Hydrate(ctx context.Context, env *realtimev1.RealtimeEventEnvelope) (*HydratedEvent, error) {
	name := EventName(env)

	var roomID, eventID string
	switch payload := env.GetEvent().(type) {
	case *realtimev1.RealtimeEventEnvelope_MessagePosted:
		roomID, eventID = payload.MessagePosted.GetRoomId(), payload.MessagePosted.GetMessageEventId()
	case *realtimev1.RealtimeEventEnvelope_MessageEdited:
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

	return &HydratedEvent{Envelope: env, EventName: name, Message: message, Actor: actor}, nil
}

// EventName returns the public snake_case name for a realtime event
// envelope's oneof case, matching realtime.proto 1:1 except for three
// renames kept for GraphQL-era naming compatibility: message_edited ->
// message_updated, message_retracted -> message_deleted, and
// asset_processing_succeeded -> video_processing_completed. Returns
// "unknown" if the envelope carries no event or an oneof case added to the
// proto after this was written.
func EventName(env *realtimev1.RealtimeEventEnvelope) string {
	switch env.GetEvent().(type) {
	case *realtimev1.RealtimeEventEnvelope_MessagePosted:
		return "message_posted"
	case *realtimev1.RealtimeEventEnvelope_MessageEdited:
		return "message_updated"
	case *realtimev1.RealtimeEventEnvelope_MessageRetracted:
		return "message_deleted"
	case *realtimev1.RealtimeEventEnvelope_ReactionAdded:
		return "reaction_added"
	case *realtimev1.RealtimeEventEnvelope_ReactionRemoved:
		return "reaction_removed"
	case *realtimev1.RealtimeEventEnvelope_UserTyping:
		return "user_typing"
	case *realtimev1.RealtimeEventEnvelope_PresenceChanged:
		return "presence_changed"
	case *realtimev1.RealtimeEventEnvelope_RoomCreated:
		return "room_created"
	case *realtimev1.RealtimeEventEnvelope_RoomUpdated:
		return "room_updated"
	case *realtimev1.RealtimeEventEnvelope_RoomDeleted:
		return "room_deleted"
	case *realtimev1.RealtimeEventEnvelope_RoomArchived:
		return "room_archived"
	case *realtimev1.RealtimeEventEnvelope_RoomUnarchived:
		return "room_unarchived"
	case *realtimev1.RealtimeEventEnvelope_UserJoinedRoom:
		return "user_joined_room"
	case *realtimev1.RealtimeEventEnvelope_UserLeftRoom:
		return "user_left_room"
	case *realtimev1.RealtimeEventEnvelope_RoomUniversalChanged:
		return "room_universal_changed"
	case *realtimev1.RealtimeEventEnvelope_NotificationCreated:
		return "notification_created"
	case *realtimev1.RealtimeEventEnvelope_NotificationDismissed:
		return "notification_dismissed"
	case *realtimev1.RealtimeEventEnvelope_NotificationLevelChanged:
		return "notification_level_changed"
	case *realtimev1.RealtimeEventEnvelope_ThreadFollowChanged:
		return "thread_follow_changed"
	case *realtimev1.RealtimeEventEnvelope_RoomMarkedAsRead:
		return "room_marked_as_read"
	case *realtimev1.RealtimeEventEnvelope_ThreadCreated:
		return "thread_created"
	case *realtimev1.RealtimeEventEnvelope_ServerUpdated:
		return "server_updated"
	case *realtimev1.RealtimeEventEnvelope_UserProfileUpdated:
		return "user_profile_updated"
	case *realtimev1.RealtimeEventEnvelope_UserCustomStatusSet:
		return "user_custom_status_set"
	case *realtimev1.RealtimeEventEnvelope_UserCustomStatusCleared:
		return "user_custom_status_cleared"
	case *realtimev1.RealtimeEventEnvelope_ServerUserPreferencesUpdated:
		return "server_user_preferences_updated"
	case *realtimev1.RealtimeEventEnvelope_RoomGroupsUpdated:
		return "room_groups_updated"
	case *realtimev1.RealtimeEventEnvelope_ServerMemberDeleted:
		return "server_member_deleted"
	case *realtimev1.RealtimeEventEnvelope_AssetProcessingStarted:
		return "asset_processing_started"
	case *realtimev1.RealtimeEventEnvelope_AssetProcessingSucceeded:
		return "video_processing_completed"
	case *realtimev1.RealtimeEventEnvelope_AssetProcessingFailed:
		return "asset_processing_failed"
	case *realtimev1.RealtimeEventEnvelope_AssetDeleted:
		return "asset_deleted"
	case *realtimev1.RealtimeEventEnvelope_CallStarted:
		return "call_started"
	case *realtimev1.RealtimeEventEnvelope_CallParticipantJoined:
		return "call_participant_joined"
	case *realtimev1.RealtimeEventEnvelope_CallParticipantLeft:
		return "call_participant_left"
	case *realtimev1.RealtimeEventEnvelope_CallEnded:
		return "call_ended"
	case *realtimev1.RealtimeEventEnvelope_MentionNotification:
		return "mention_notification"
	case *realtimev1.RealtimeEventEnvelope_NewDirectMessageNotification:
		return "new_direct_message_notification"
	case *realtimev1.RealtimeEventEnvelope_SessionTerminated:
		return "session_terminated"
	default:
		return "unknown"
	}
}
