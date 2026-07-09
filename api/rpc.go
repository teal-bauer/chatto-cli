package api

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	apiv1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1"
)

// GetViewer returns the authenticated user's profile.
func (c *Client) GetViewer(ctx context.Context) (*apiv1.ViewerUser, error) {
	resp, err := c.viewer.GetViewer(ctx, connect.NewRequest(&apiv1.GetViewerRequest{}))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetUser(), nil
}

// ListRooms returns every room visible to the current user (both channels
// and their active DMs). RoomDirectoryService.ListRooms takes no
// PageRequest/PageInfo -- unlike most other list RPCs it returns the full
// matching snapshot in one response, so there is nothing to paginate.
func (c *Client) ListRooms(ctx context.Context) ([]*apiv1.RoomWithViewerState, error) {
	req := &apiv1.ListRoomsRequest{Scope: apiv1.RoomDirectoryScope_ROOM_DIRECTORY_SCOPE_ALL}
	resp, err := c.roomDirectory.ListRooms(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetRooms(), nil
}

// GetRoom fetches one room by ID, with the viewer's membership state.
func (c *Client) GetRoom(ctx context.Context, roomID string) (*apiv1.RoomWithViewerState, error) {
	req := &apiv1.GetRoomRequest{RoomId: roomID}
	resp, err := c.roomDirectory.GetRoom(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetRoom(), nil
}

// GetRoomEvents fetches one page of a room's timeline. before/after are
// opaque server-issued cursors (RoomTimelinePage.start_cursor/end_cursor),
// not event IDs -- pass "" for both to get the first (most recent) page.
// Passing both before and after is meaningless; before takes precedence.
func (c *Client) GetRoomEvents(ctx context.Context, roomID string, limit int32, before, after string) (*apiv1.RoomTimelinePage, error) {
	req := &apiv1.GetRoomEventsRequest{RoomId: roomID, Limit: limit}
	switch {
	case before != "":
		req.Cursor = &apiv1.GetRoomEventsRequest_Before{Before: before}
	case after != "":
		req.Cursor = &apiv1.GetRoomEventsRequest_After{After: after}
	}
	resp, err := c.room.GetRoomEvents(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetPage(), nil
}

// GetMessage fetches one message. Returns (nil, nil) on NOT_FOUND -- this
// includes a message retracted between an event signal and this fetch, so
// callers should treat a nil result as "drop", not as an error.
func (c *Client) GetMessage(ctx context.Context, roomID, eventID string) (*apiv1.Message, error) {
	req := &apiv1.GetMessageRequest{RoomId: roomID, EventId: eventID}
	resp, err := c.message.GetMessage(ctx, connect.NewRequest(req))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, mapError(err)
	}
	return resp.Msg.GetMessage(), nil
}

// BatchGetMessages fetches up to 100 messages from one room. Missing or
// retracted IDs are silently omitted from the result by the server, not
// raised as errors.
func (c *Client) BatchGetMessages(ctx context.Context, roomID string, eventIDs []string) ([]*apiv1.Message, error) {
	req := &apiv1.BatchGetMessagesRequest{RoomId: roomID, EventIds: eventIDs}
	resp, err := c.message.BatchGetMessages(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetMessages(), nil
}

// CreateMessage posts a message to a room. threadRootEventID and inReplyTo
// are optional; pass "" when not applicable.
func (c *Client) CreateMessage(ctx context.Context, roomID, body, threadRootEventID, inReplyTo string) (*apiv1.Message, error) {
	req := &apiv1.CreateMessageRequest{
		RoomId:            roomID,
		Body:              body,
		ThreadRootEventId: threadRootEventID,
		InReplyTo:         inReplyTo,
	}
	resp, err := c.message.CreateMessage(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetMessage(), nil
}

// JoinRoom joins the current user to a room.
func (c *Client) JoinRoom(ctx context.Context, roomID string) (*apiv1.Room, error) {
	req := &apiv1.JoinRoomRequest{RoomId: roomID}
	resp, err := c.room.JoinRoom(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetRoom(), nil
}

// LeaveRoom removes the current user from a room. Returns a *ChattoError
// (typically CodeFailedPrecondition) for DM and universal rooms, which
// cannot be left; callers should catch it to message the user rather than
// treat it as unexpected.
func (c *Client) LeaveRoom(ctx context.Context, roomID string) (bool, error) {
	req := &apiv1.LeaveRoomRequest{RoomId: roomID}
	resp, err := c.room.LeaveRoom(ctx, connect.NewRequest(req))
	if err != nil {
		return false, mapError(err)
	}
	return resp.Msg.GetLeft(), nil
}

// ListUsers searches the server's member directory.
func (c *Client) ListUsers(ctx context.Context, search string, limit int32) ([]*apiv1.DirectoryMember, error) {
	req := &apiv1.ListUsersRequest{Search: search, Page: &apiv1.PageRequest{Limit: limit}}
	resp, err := c.user.ListUsers(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetUsers(), nil
}

// BatchGetUsers resolves user IDs to directory members.
func (c *Client) BatchGetUsers(ctx context.Context, userIDs []string) ([]*apiv1.DirectoryMember, error) {
	req := &apiv1.BatchGetUsersRequest{UserIds: userIDs}
	resp, err := c.user.BatchGetUsers(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, mapError(err)
	}
	return resp.Msg.GetUsers(), nil
}

// UpdatePresence sets the current user's live presence status. This is
// transient -- callers must keep refreshing on an interval to stay "online".
// PRESENCE_STATUS_OFFLINE cannot be set explicitly; the server rejects it.
func (c *Client) UpdatePresence(ctx context.Context, status apiv1.PresenceStatus) error {
	req := &apiv1.UpdatePresenceRequest{Status: status}
	_, err := c.account.UpdatePresence(ctx, connect.NewRequest(req))
	return mapError(err)
}

// AddReaction adds a reaction to a message. emoji is a shortcode (e.g.
// "thumbsup"), not a literal glyph.
func (c *Client) AddReaction(ctx context.Context, roomID, messageEventID, emoji string) error {
	req := &apiv1.AddReactionRequest{RoomId: roomID, MessageEventId: messageEventID, Emoji: emoji}
	_, err := c.message.AddReaction(ctx, connect.NewRequest(req))
	return mapError(err)
}

// RemoveReaction removes a reaction from a message.
func (c *Client) RemoveReaction(ctx context.Context, roomID, messageEventID, emoji string) error {
	req := &apiv1.RemoveReactionRequest{RoomId: roomID, MessageEventId: messageEventID, Emoji: emoji}
	_, err := c.message.RemoveReaction(ctx, connect.NewRequest(req))
	return mapError(err)
}

// UpdateTypingIndicator refreshes the live-only typing indicator for a room
// or thread (pass "" for threadRootEventID to indicate the room itself). It
// expires on its own via a server-side TTL; there is no RPC to clear it
// early.
func (c *Client) UpdateTypingIndicator(ctx context.Context, roomID, threadRootEventID string) error {
	req := &apiv1.UpdateTypingIndicatorRequest{RoomId: roomID, ThreadRootEventId: threadRootEventID}
	_, err := c.room.UpdateTypingIndicator(ctx, connect.NewRequest(req))
	return mapError(err)
}

// MarkRoomAsRead marks a room read up to (and including) upToEventID.
func (c *Client) MarkRoomAsRead(ctx context.Context, roomID, upToEventID string) error {
	req := &apiv1.MarkRoomAsReadRequest{RoomId: roomID, UpToEventId: upToEventID}
	_, err := c.room.MarkRoomAsRead(ctx, connect.NewRequest(req))
	return mapError(err)
}

// ResolveRoomID looks up a room by ID, exact name, or "#name" among rooms
// visible to the current user (via ListRooms). Returns the room ID on
// success.
func (c *Client) ResolveRoomID(ctx context.Context, ref string) (string, error) {
	rooms, err := c.ListRooms(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range rooms {
		room := r.GetRoom()
		if room == nil {
			continue
		}
		if room.GetId() == ref || strings.EqualFold(room.GetName(), ref) || strings.EqualFold("#"+room.GetName(), ref) {
			return room.GetId(), nil
		}
	}
	return "", fmt.Errorf("room %q not found", ref)
}
